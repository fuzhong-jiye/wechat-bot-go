package wechat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultBaseURL = "https://ilinkai.weixin.qq.com"
	cdnBaseURL     = "https://novac2c.cdn.weixin.qq.com/c2c"
	channelVersion = "1.0.2"
	apiTimeout     = 15 * time.Second
	pollTimeout    = 40 * time.Second
	qrPollInterval = 2 * time.Second
)

// Upload media type constants (different from ItemType).
const (
	uploadMediaImage = 1
	uploadMediaVideo = 2
	uploadMediaFile  = 3
	uploadMediaVoice = 4
)

func uploadMediaType(t ItemType) int {
	switch t {
	case ItemImage:
		return uploadMediaImage
	case ItemVideo:
		return uploadMediaVideo
	case ItemFile:
		return uploadMediaFile
	case ItemVoice:
		return uploadMediaVoice
	default:
		return uploadMediaFile
	}
}

// --- Wire format structs ---

type baseInfo struct {
	ChannelVersion string `json:"channel_version"`
}

func newBaseInfo() baseInfo {
	return baseInfo{ChannelVersion: channelVersion}
}

type getUpdatesRequest struct {
	GetUpdatesBuf string   `json:"get_updates_buf"`
	BaseInfo      baseInfo `json:"base_info"`
}

type getUpdatesResponse struct {
	Ret           int           `json:"ret"`
	ErrCode       int           `json:"errcode"`
	ErrMsg        string        `json:"errmsg"`
	Msgs          []wireMessage `json:"msgs"`
	GetUpdatesBuf string        `json:"get_updates_buf"`
}

type wireMessage struct {
	Seq          int        `json:"seq"`
	MessageID    int        `json:"message_id"`
	FromUserID   string     `json:"from_user_id"`
	ToUserID     string     `json:"to_user_id"`
	ClientID     string     `json:"client_id"`
	CreateTimeMs int64      `json:"create_time_ms"`
	GroupID      string     `json:"group_id"`
	MessageType  int        `json:"message_type"`
	MessageState int        `json:"message_state"`
	ItemList     []wireItem `json:"item_list"`
	ContextToken string     `json:"context_token"`
}

type wireItem struct {
	Type      int            `json:"type"`
	TextItem  *wireTextItem  `json:"text_item,omitempty"`
	ImageItem *wireImageItem `json:"image_item,omitempty"`
	VoiceItem *wireVoiceItem `json:"voice_item,omitempty"`
	FileItem  *wireFileItem  `json:"file_item,omitempty"`
	VideoItem *wireVideoItem `json:"video_item,omitempty"`
}

type wireTextItem struct {
	Text string `json:"text"`
}

type wireCDNMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param,omitempty"`
	AESKey            string `json:"aes_key,omitempty"`
	EncryptType       int    `json:"encrypt_type,omitempty"`
}

type wireImageItem struct {
	Media       *wireCDNMedia `json:"media,omitempty"`
	AESKey      string        `json:"aeskey,omitempty"`
	MidSize     int           `json:"mid_size,omitempty"`
	ThumbWidth  int           `json:"thumb_width,omitempty"`
	ThumbHeight int           `json:"thumb_height,omitempty"`
}

type wireVoiceItem struct {
	Media      *wireCDNMedia `json:"media,omitempty"`
	EncodeType int           `json:"encode_type,omitempty"`
	Playtime   int           `json:"playtime,omitempty"`
	Text       string        `json:"text,omitempty"`
}

type wireFileItem struct {
	Media    *wireCDNMedia `json:"media,omitempty"`
	FileName string        `json:"file_name,omitempty"`
	Len      string        `json:"len,omitempty"`
}

type wireVideoItem struct {
	Media       *wireCDNMedia `json:"media,omitempty"`
	VideoSize   int           `json:"video_size,omitempty"`
	PlayLength  int           `json:"play_length,omitempty"`
	ThumbWidth  int           `json:"thumb_width,omitempty"`
	ThumbHeight int           `json:"thumb_height,omitempty"`
}

type wireSendRequest struct {
	BaseInfo baseInfo    `json:"base_info"`
	Msg      wireSendMsg `json:"msg"`
}

type wireSendMsg struct {
	FromUserID   string     `json:"from_user_id"`
	ToUserID     string     `json:"to_user_id"`
	ClientID     string     `json:"client_id"`
	MessageType  int        `json:"message_type"`
	MessageState int        `json:"message_state"`
	ContextToken string     `json:"context_token"`
	ItemList     []wireItem `json:"item_list"`
}

type wireSendResponse struct {
	Ret     int    `json:"ret"`
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

type wireUploadURLRequest struct {
	FileKey     string   `json:"filekey"`
	MediaType   int      `json:"media_type"`
	ToUserID    string   `json:"to_user_id"`
	RawSize     int      `json:"rawsize"`
	RawFileMD5  string   `json:"rawfilemd5"`
	FileSize    int      `json:"filesize"`
	NoNeedThumb bool     `json:"no_need_thumb"`
	AESKey      string   `json:"aeskey"`
	BaseInfo    baseInfo `json:"base_info"`
}

type wireUploadURLResponse struct {
	UploadParam string `json:"upload_param"`
}

type wireQRCodeResponse struct {
	QRCode           string `json:"qrcode"`
	QRCodeImgContent string `json:"qrcode_img_content"`
}

type wireQRCodeStatusResponse struct {
	Status     string `json:"status"`
	BotToken   string `json:"bot_token"`
	ILinkBotID string `json:"ilink_bot_id"`
	BaseURL    string `json:"baseurl"`
}

// --- CDN URL builders ---

func buildCDNDownloadURL(encryptQueryParam, cdnBase string) string {
	return cdnBase + "/download?encrypted_query_param=" + url.QueryEscape(encryptQueryParam)
}

func buildCDNUploadURL(cdnBase, uploadParam, filekey string) string {
	return cdnBase + "/upload?encrypted_query_param=" + url.QueryEscape(uploadParam) +
		"&filekey=" + url.QueryEscape(filekey)
}

// --- ilinkClient ---

type ilinkClient struct {
	baseURL    string
	botToken   string
	httpClient *http.Client
	logger     Logger
	logLevel   LogLevel
	sessionID  string
	peerUserID func() string
}

func newIlinkClient(baseURL, botToken string) *ilinkClient {
	return &ilinkClient{
		baseURL:    baseURL,
		botToken:   botToken,
		httpClient: &http.Client{},
		logger:     NopLogger{},
		logLevel:   LogInfo,
	}
}

func (c *ilinkClient) log(ctx context.Context, level LogLevel, msg string, fields ...Field) {
	if c.logger == nil || !shouldLog(c.logLevel, level) {
		return
	}
	fields = appendFields(fields,
		Field{Key: "base_url", Value: c.baseURL},
		Field{Key: "session_id", Value: c.sessionID},
	)
	if c.peerUserID != nil {
		if peer := c.peerUserID(); peer != "" {
			fields = append(fields, Field{Key: "peer_user_id", Value: maskID(peer)})
		}
	}
	c.logger.Log(ctx, level, msg, fields...)
}

func (c *ilinkClient) setAuth(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")

	var buf [4]byte
	rand.Read(buf[:])
	uin := uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3])
	req.Header.Set("X-WECHAT-UIN", base64.StdEncoding.EncodeToString(
		[]byte(strconv.FormatUint(uint64(uin), 10)),
	))

	if c.botToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.botToken)
	}
}

func (c *ilinkClient) do(ctx context.Context, method, path string, body, result any) error {
	start := time.Now()
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			c.log(ctx, LogError, "request.failed",
				Field{Key: "op", Value: path},
				Field{Key: "http_method", Value: method},
				Field{Key: "error", Value: sanitizeError(err)},
			)
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	reqURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		c.log(ctx, LogError, "request.failed",
			Field{Key: "op", Value: path},
			Field{Key: "http_method", Value: method},
			Field{Key: "error", Value: sanitizeError(err)},
		)
		return fmt.Errorf("create request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.log(ctx, LogError, "request.failed",
			Field{Key: "op", Value: path},
			Field{Key: "http_method", Value: method},
			Field{Key: "duration_ms", Value: time.Since(start).Milliseconds()},
			Field{Key: "error", Value: sanitizeError(err)},
		)
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.log(ctx, LogError, "request.failed",
			Field{Key: "op", Value: path},
			Field{Key: "http_method", Value: method},
			Field{Key: "http_status", Value: resp.StatusCode},
			Field{Key: "duration_ms", Value: time.Since(start).Milliseconds()},
			Field{Key: "error", Value: sanitizeError(err)},
		)
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &APIError{
			HTTPStatus: resp.StatusCode,
			ErrMsg:     string(respBody),
		}
		c.log(ctx, LogError, "request.failed",
			Field{Key: "op", Value: path},
			Field{Key: "http_method", Value: method},
			Field{Key: "http_status", Value: resp.StatusCode},
			Field{Key: "duration_ms", Value: time.Since(start).Milliseconds()},
			Field{Key: "error", Value: sanitizeError(apiErr)},
		)
		return &APIError{
			HTTPStatus: resp.StatusCode,
			ErrMsg:     string(respBody),
		}
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			c.log(ctx, LogError, "request.failed",
				Field{Key: "op", Value: path},
				Field{Key: "http_method", Value: method},
				Field{Key: "http_status", Value: resp.StatusCode},
				Field{Key: "duration_ms", Value: time.Since(start).Milliseconds()},
				Field{Key: "error", Value: sanitizeError(err)},
			)
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}

	// Check ret/errcode in response
	var envelope struct {
		Ret     int    `json:"ret"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if len(respBody) > 0 {
		json.Unmarshal(respBody, &envelope)
	}
	if envelope.Ret != 0 || envelope.ErrCode != 0 {
		apiErr := &APIError{
			HTTPStatus: resp.StatusCode,
			Ret:        envelope.Ret,
			ErrCode:    envelope.ErrCode,
			ErrMsg:     envelope.ErrMsg,
		}
		c.log(ctx, LogError, "request.failed",
			Field{Key: "op", Value: path},
			Field{Key: "http_method", Value: method},
			Field{Key: "http_status", Value: resp.StatusCode},
			Field{Key: "ret", Value: envelope.Ret},
			Field{Key: "errcode", Value: envelope.ErrCode},
			Field{Key: "duration_ms", Value: time.Since(start).Milliseconds()},
			Field{Key: "error", Value: sanitizeError(apiErr)},
		)
		return &APIError{
			HTTPStatus: resp.StatusCode,
			Ret:        envelope.Ret,
			ErrCode:    envelope.ErrCode,
			ErrMsg:     envelope.ErrMsg,
		}
	}

	c.log(ctx, LogDebug, "request.succeeded",
		Field{Key: "op", Value: path},
		Field{Key: "http_method", Value: method},
		Field{Key: "http_status", Value: resp.StatusCode},
		Field{Key: "duration_ms", Value: time.Since(start).Milliseconds()},
	)
	return nil
}

// --- API methods ---

func (c *ilinkClient) getBotQRCode(ctx context.Context) (*wireQRCodeResponse, error) {
	noAuth := &ilinkClient{
		baseURL:    c.baseURL,
		httpClient: c.httpClient,
		logger:     c.logger,
		logLevel:   c.logLevel,
		sessionID:  c.sessionID,
		peerUserID: c.peerUserID,
	}
	var resp wireQRCodeResponse
	err := noAuth.do(ctx, http.MethodGet, "/ilink/bot/get_bot_qrcode?bot_type=3", nil, &resp)
	return &resp, err
}

func (c *ilinkClient) getQRCodeStatus(ctx context.Context, qrCode string) (*wireQRCodeStatusResponse, error) {
	path := "/ilink/bot/get_qrcode_status?qrcode=" + url.QueryEscape(qrCode)

	reqURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	noAuth := &ilinkClient{baseURL: c.baseURL, httpClient: c.httpClient}
	noAuth.setAuth(req)
	req.Header.Set("iLink-App-ClientVersion", "1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.log(ctx, LogError, "request.failed",
			Field{Key: "op", Value: path},
			Field{Key: "http_method", Value: http.MethodGet},
			Field{Key: "error", Value: sanitizeError(err)},
		)
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		c.log(ctx, LogError, "request.failed",
			Field{Key: "op", Value: path},
			Field{Key: "http_method", Value: http.MethodGet},
			Field{Key: "http_status", Value: resp.StatusCode},
			Field{Key: "error", Value: sanitizeError(&APIError{HTTPStatus: resp.StatusCode, ErrMsg: string(body)})},
		)
		return nil, &APIError{HTTPStatus: resp.StatusCode, ErrMsg: string(body)}
	}

	var result wireQRCodeStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.log(ctx, LogError, "request.failed",
			Field{Key: "op", Value: path},
			Field{Key: "http_method", Value: http.MethodGet},
			Field{Key: "http_status", Value: resp.StatusCode},
			Field{Key: "error", Value: sanitizeError(err)},
		)
		return nil, fmt.Errorf("decode response: %w", err)
	}
	c.log(ctx, LogDebug, "request.succeeded",
		Field{Key: "op", Value: path},
		Field{Key: "http_method", Value: http.MethodGet},
		Field{Key: "http_status", Value: resp.StatusCode},
	)
	return &result, nil
}

func (c *ilinkClient) getUpdates(ctx context.Context, cursor string) (*getUpdatesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	req := getUpdatesRequest{
		GetUpdatesBuf: cursor,
		BaseInfo:      newBaseInfo(),
	}
	var resp getUpdatesResponse
	err := c.do(ctx, http.MethodPost, "/ilink/bot/getupdates", req, &resp)
	return &resp, err
}

func (c *ilinkClient) sendMessage(ctx context.Context, req wireSendRequest) error {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	var resp wireSendResponse
	return c.do(ctx, http.MethodPost, "/ilink/bot/sendmessage", req, &resp)
}

func (c *ilinkClient) getUploadURL(ctx context.Context, req wireUploadURLRequest) (*wireUploadURLResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	var resp wireUploadURLResponse
	err := c.do(ctx, http.MethodPost, "/ilink/bot/getuploadurl", req, &resp)
	return &resp, err
}

func (c *ilinkClient) uploadToCDN(ctx context.Context, cdnURL string, data []byte) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cdnURL, bytes.NewReader(data))
		if err != nil {
			return "", fmt.Errorf("create CDN request: %w", err)
		}
		req.Header.Set("Content-Type", "application/octet-stream")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			c.log(ctx, LogWarn, "cdn.upload.retry",
				Field{Key: "attempt", Value: attempt},
				Field{Key: "error", Value: sanitizeError(err)},
			)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			errMsg := resp.Header.Get("x-error-message")
			c.log(ctx, LogError, "cdn.upload.failed",
				Field{Key: "attempt", Value: attempt},
				Field{Key: "http_status", Value: resp.StatusCode},
				Field{Key: "error", Value: sanitizeString(errMsg)},
			)
			return "", fmt.Errorf("CDN upload client error %d: %s", resp.StatusCode, errMsg)
		}
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("CDN upload server error: status %d", resp.StatusCode)
			c.log(ctx, LogWarn, "cdn.upload.retry",
				Field{Key: "attempt", Value: attempt},
				Field{Key: "http_status", Value: resp.StatusCode},
				Field{Key: "error", Value: sanitizeError(lastErr)},
			)
			continue
		}

		downloadParam := resp.Header.Get("x-encrypted-param")
		if downloadParam == "" {
			c.log(ctx, LogError, "cdn.upload.failed",
				Field{Key: "attempt", Value: attempt},
				Field{Key: "error", Value: "missing download parameter"},
			)
			return "", fmt.Errorf("CDN upload response missing x-encrypted-param header")
		}
		c.log(ctx, LogInfo, "cdn.upload.succeeded",
			Field{Key: "attempt", Value: attempt},
			Field{Key: "size", Value: len(data)},
		)
		return downloadParam, nil
	}
	c.log(ctx, LogError, "cdn.upload.failed", Field{Key: "error", Value: sanitizeError(lastErr)})
	return "", fmt.Errorf("CDN upload failed after 3 attempts: %w", lastErr)
}

func (c *ilinkClient) downloadFromCDN(ctx context.Context, encryptQueryParam, aesKeyBase64 string) ([]byte, error) {
	cdnURL := buildCDNDownloadURL(encryptQueryParam, cdnBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cdnURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create CDN download request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.log(ctx, LogError, "cdn.download.failed", Field{Key: "error", Value: sanitizeError(err)})
		return nil, fmt.Errorf("CDN download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		c.log(ctx, LogError, "cdn.download.failed", Field{Key: "http_status", Value: resp.StatusCode})
		return nil, fmt.Errorf("CDN download failed: status %d", resp.StatusCode)
	}

	encrypted, err := io.ReadAll(resp.Body)
	if err != nil {
		c.log(ctx, LogError, "cdn.download.failed", Field{Key: "error", Value: sanitizeError(err)})
		return nil, fmt.Errorf("read CDN response: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(aesKeyBase64)
	if err != nil {
		c.log(ctx, LogError, "cdn.download.failed", Field{Key: "error", Value: sanitizeError(err)})
		return nil, fmt.Errorf("decode aes_key: %w", err)
	}

	var aesKey []byte
	switch len(decoded) {
	case 16:
		aesKey = decoded
	case 32:
		aesKey, err = hexDecode(decoded)
		if err != nil {
			c.log(ctx, LogError, "cdn.download.failed", Field{Key: "error", Value: sanitizeError(err)})
			return nil, fmt.Errorf("hex decode aes_key: %w", err)
		}
	default:
		c.log(ctx, LogError, "cdn.download.failed", Field{Key: "error", Value: "unexpected aes key length"})
		return nil, fmt.Errorf("unexpected aes_key length after base64: %d", len(decoded))
	}

	data, err := aesECBDecrypt(encrypted, aesKey)
	if err != nil {
		c.log(ctx, LogError, "cdn.download.failed", Field{Key: "error", Value: sanitizeError(err)})
		return nil, err
	}
	c.log(ctx, LogDebug, "cdn.download.succeeded", Field{Key: "size", Value: len(data)})
	return data, nil
}

func hexDecode(src []byte) ([]byte, error) {
	if len(src)%2 != 0 {
		return nil, fmt.Errorf("odd hex length: %d", len(src))
	}
	dst := make([]byte, len(src)/2)
	for i := 0; i < len(dst); i++ {
		a, ok1 := hexVal(src[i*2])
		b, ok2 := hexVal(src[i*2+1])
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("invalid hex byte at position %d", i*2)
		}
		dst[i] = a<<4 | b
	}
	return dst, nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func generateClientID() string {
	var buf [4]byte
	rand.Read(buf[:])
	return fmt.Sprintf("bot:%d-%x", time.Now().UnixMilli(), buf[:])
}
