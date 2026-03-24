# WeChat Bot Go SDK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a Go SDK for the WeChat iLink Bot API that manages login, message polling, sending (text + media), and session persistence.

**Architecture:** Single `package wechat` with bottom-up layers: crypto -> HTTP client -> storage -> message types -> bot orchestrator. Each layer is independently testable. All iLink API communication goes through an unexported `ilinkClient`.

**Tech Stack:** Go 1.26+, stdlib (`crypto/aes`, `net/http`, `encoding/json`), `modernc.org/sqlite` (pure Go SQLite for optional persistence)

**Spec:** `docs/superpowers/specs/2026-03-24-wechat-sdk-implementation-design.md`

**TS Reference:** `~/Documents/workspace/projects/wechat-bot` — the source of truth for all wire format details

---

## File Structure

| File | Responsibility |
|------|---------------|
| `errors.go` | Sentinel errors (`ErrSessionExpired`, `ErrNoContextToken`, `ErrContextTokenExpired`) and `APIError` type |
| `crypto.go` | AES-128-ECB encrypt/decrypt with PKCS7 padding, padded size calculation (all unexported) |
| `crypto_test.go` | Round-trip, known vectors, padding edge cases, key encoding |
| `client.go` | Wire format structs, constants, CDN URL builders, `ilinkClient` with auth headers and all API methods (all unexported) |
| `client_test.go` | `do()` against httptest, header verification, CDN URL builders, error handling |
| `storage.go` | `Storage` interface, `Session` struct, `MemoryStorage` |
| `storage_test.go` | Save/Load round-trip, missing key behavior |
| `sqlite.go` | `SQLiteStorage` implementation |
| `sqlite_test.go` | Same tests as MemoryStorage plus concurrent access |
| `message.go` | `Message`, `Item`, `ItemType`, all `*Item` types, `Text()`, download closures, `parseMessage` |
| `bot.go` | `Bot`, `Option` funcs, `QRCode`, `QRHandler`, `Start`/`Stop`/`Send*`/`OnMessage` |
| `bot_test.go` | Login flow, polling, send, stop — all against httptest mock server |
| `example_test.go` | Compilable testable example from design doc section 8 |

---

### Task 1: Errors

**Files:**
- Create: `errors.go`

- [ ] **Step 1: Write errors.go**

```go
package wechat

import (
	"errors"
	"fmt"
)

var ErrSessionExpired = errors.New("wechat: session expired, re-login required")

var ErrNoContextToken = errors.New(
	"wechat: no context token, the user must message the bot first",
)

var ErrContextTokenExpired = errors.New(
	"wechat: context token expired, the user has not messaged the bot in the last 24 hours",
)

type APIError struct {
	HTTPStatus int
	Ret        int
	ErrCode    int
	ErrMsg     string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("wechat: API error (http=%d ret=%d errcode=%d): %s",
		e.HTTPStatus, e.Ret, e.ErrCode, e.ErrMsg)
}
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/albaohlson/Documents/workspace/projects/wechat-bot-go && go build ./...`
Expected: success (no output)

- [ ] **Step 3: Commit**

```bash
git add errors.go
git commit -m "feat: add sentinel errors and APIError type"
```

---

### Task 2: Crypto

**Files:**
- Create: `crypto.go`
- Create: `crypto_test.go`

- [ ] **Step 1: Write crypto_test.go**

```go
package wechat

import (
	"bytes"
	"testing"
)

func TestAesECBPaddedSize(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, 16},   // empty → full padding block
		{1, 16},   // 1 byte → padded to 16
		{15, 16},  // 15 bytes → padded to 16
		{16, 32},  // exact block → adds full padding block
		{17, 32},  // 17 bytes → padded to 32
		{31, 32},
		{32, 48},
	}
	for _, tt := range tests {
		got := aesECBPaddedSize(tt.input)
		if got != tt.want {
			t.Errorf("aesECBPaddedSize(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestAesECBRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef") // 16 bytes

	tests := [][]byte{
		[]byte("hello"),                          // short
		[]byte("exactly16bytes!!"),               // exact block
		bytes.Repeat([]byte("x"), 100),           // multi-block
		{},                                        // empty
	}
	for _, plaintext := range tests {
		ciphertext, err := aesECBEncrypt(plaintext, key)
		if err != nil {
			t.Fatalf("encrypt(%q): %v", plaintext, err)
		}
		if len(ciphertext) != aesECBPaddedSize(len(plaintext)) {
			t.Errorf("ciphertext len = %d, want %d", len(ciphertext), aesECBPaddedSize(len(plaintext)))
		}
		got, err := aesECBDecrypt(ciphertext, key)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Errorf("round-trip failed: got %q, want %q", got, plaintext)
		}
	}
}

func TestAesECBInvalidKeyLength(t *testing.T) {
	_, err := aesECBEncrypt([]byte("data"), []byte("short"))
	if err == nil {
		t.Error("expected error for short key")
	}
	_, err = aesECBDecrypt([]byte("0123456789abcdef"), []byte("short"))
	if err == nil {
		t.Error("expected error for short key on decrypt")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./... -run TestAesECB -v`
Expected: FAIL (functions not defined)

- [ ] **Step 3: Write crypto.go**

```go
package wechat

import (
	"crypto/aes"
	"fmt"
)

// aesECBEncrypt encrypts data using AES-128-ECB with PKCS7 padding.
func aesECBEncrypt(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	bs := block.BlockSize()
	padded := pkcs7Pad(data, bs)
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += bs {
		block.Encrypt(out[i:i+bs], padded[i:i+bs])
	}
	return out, nil
}

// aesECBDecrypt decrypts AES-128-ECB data and removes PKCS7 padding.
func aesECBDecrypt(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	bs := block.BlockSize()
	if len(data) == 0 || len(data)%bs != 0 {
		return nil, fmt.Errorf("ciphertext length %d is not a multiple of block size %d", len(data), bs)
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += bs {
		block.Decrypt(out[i:i+bs], data[i:i+bs])
	}
	return pkcs7Unpad(out, bs)
}

// aesECBPaddedSize returns the ciphertext size after PKCS7 padding.
func aesECBPaddedSize(rawSize int) int {
	const bs = aes.BlockSize
	// PKCS7 always adds at least 1 byte of padding
	return ((rawSize + 1 + bs - 1) / bs) * bs
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padded := make([]byte, len(data)+padding)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	return padded
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize {
		return nil, fmt.Errorf("invalid padding value: %d", padding)
	}
	for i := len(data) - padding; i < len(data); i++ {
		if data[i] != byte(padding) {
			return nil, fmt.Errorf("invalid padding byte at position %d", i)
		}
	}
	return data[:len(data)-padding], nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./... -run TestAesECB -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add crypto.go crypto_test.go
git commit -m "feat: add AES-128-ECB crypto with PKCS7 padding"
```

---

### Task 3: HTTP Client — Wire Types, Constants, CDN URL Builders

**Files:**
- Create: `client.go`
- Create: `client_test.go`

- [ ] **Step 1: Write client_test.go with CDN URL builder tests**

```go
package wechat

import "testing"

func TestBuildCDNDownloadURL(t *testing.T) {
	got := buildCDNDownloadURL("enc-param-123", "https://cdn.example.com/c2c")
	want := "https://cdn.example.com/c2c/download?encrypted_query_param=enc-param-123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildCDNDownloadURLEncoding(t *testing.T) {
	got := buildCDNDownloadURL("a=b&c=d", "https://cdn.example.com/c2c")
	want := "https://cdn.example.com/c2c/download?encrypted_query_param=a%3Db%26c%3Dd"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildCDNUploadURL(t *testing.T) {
	got := buildCDNUploadURL("https://cdn.example.com/c2c", "upload-param", "filekey123")
	want := "https://cdn.example.com/c2c/upload?encrypted_query_param=upload-param&filekey=filekey123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./... -run TestBuildCDN -v`
Expected: FAIL

- [ ] **Step 3: Write client.go with constants, wire types, and CDN URL builders**

```go
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
// These are the values for the media_type field in getUploadURL requests.
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

// --- Wire format structs (unexported, match iLink Bot API JSON) ---

type baseInfo struct {
	ChannelVersion string `json:"channel_version"`
}

func newBaseInfo() baseInfo {
	return baseInfo{ChannelVersion: channelVersion}
}

// getUpdates

type getUpdatesRequest struct {
	GetUpdatesBuf string   `json:"get_updates_buf"`
	BaseInfo      baseInfo `json:"base_info"`
}

type getUpdatesResponse struct {
	Ret           int            `json:"ret"`
	ErrCode       int            `json:"errcode"`
	ErrMsg        string         `json:"errmsg"`
	Msgs          []wireMessage  `json:"msgs"`
	GetUpdatesBuf string         `json:"get_updates_buf"`
}

// wireMessage is the raw message from the API.
type wireMessage struct {
	Seq          int            `json:"seq"`
	MessageID    int            `json:"message_id"`
	FromUserID   string         `json:"from_user_id"`
	ToUserID     string         `json:"to_user_id"`
	ClientID     string         `json:"client_id"`
	CreateTimeMs int64          `json:"create_time_ms"`
	GroupID      string         `json:"group_id"`
	MessageType  int            `json:"message_type"`
	MessageState int            `json:"message_state"`
	ItemList     []wireItem     `json:"item_list"`
	ContextToken string         `json:"context_token"`
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

// sendMessage

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

// getUploadURL

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

// QR code

type wireQRCodeResponse struct {
	QRCode         string `json:"qrcode"`
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
}

func newIlinkClient(baseURL, botToken string) *ilinkClient {
	return &ilinkClient{
		baseURL:    baseURL,
		botToken:   botToken,
		httpClient: &http.Client{},
	}
}

func (c *ilinkClient) setAuth(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("AuthorizationType", "ilink_bot_token")

	// X-WECHAT-UIN: base64(string(random uint32))
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

// do performs an HTTP request to the iLink API.
// For non-nil body, it JSON-encodes and sends as POST body.
// For non-nil result, it JSON-decodes the response into result.
// It checks both ret and errcode fields for API errors.
func (c *ilinkClient) do(ctx context.Context, method, path string, body, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	reqURL := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{
			HTTPStatus: resp.StatusCode,
			ErrMsg:     string(respBody),
		}
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
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
		return &APIError{
			HTTPStatus: resp.StatusCode,
			Ret:        envelope.Ret,
			ErrCode:    envelope.ErrCode,
			ErrMsg:     envelope.ErrMsg,
		}
	}

	return nil
}

// --- API methods ---

func (c *ilinkClient) getBotQRCode(ctx context.Context) (*wireQRCodeResponse, error) {
	// No auth for QR code request — use a temporary client without botToken
	noAuth := &ilinkClient{baseURL: defaultBaseURL, httpClient: c.httpClient}
	var resp wireQRCodeResponse
	err := noAuth.do(ctx, http.MethodGet, "/ilink/bot/get_bot_qrcode?bot_type=3", nil, &resp)
	return &resp, err
}

func (c *ilinkClient) getQRCodeStatus(ctx context.Context, qrCode string) (*wireQRCodeStatusResponse, error) {
	path := "/ilink/bot/get_qrcode_status?qrcode=" + url.QueryEscape(qrCode)

	var bodyReader io.Reader
	reqURL := defaultBaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	// No botToken auth, but set standard + extra header
	noAuth := &ilinkClient{baseURL: defaultBaseURL, httpClient: c.httpClient}
	noAuth.setAuth(req)
	req.Header.Set("iLink-App-ClientVersion", "1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, &APIError{HTTPStatus: resp.StatusCode, ErrMsg: string(body)}
	}

	var result wireQRCodeStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
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

// uploadToCDN uploads encrypted data to the CDN. Retries up to 3 times on 5xx.
// Returns the x-encrypted-param header value (download param).
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
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			errMsg := resp.Header.Get("x-error-message")
			return "", fmt.Errorf("CDN upload client error %d: %s", resp.StatusCode, errMsg)
		}
		if resp.StatusCode != 200 {
			lastErr = fmt.Errorf("CDN upload server error: status %d", resp.StatusCode)
			continue
		}

		downloadParam := resp.Header.Get("x-encrypted-param")
		if downloadParam == "" {
			return "", fmt.Errorf("CDN upload response missing x-encrypted-param header")
		}
		return downloadParam, nil
	}
	return "", fmt.Errorf("CDN upload failed after 3 attempts: %w", lastErr)
}

// downloadFromCDN downloads and decrypts media from the CDN.
func (c *ilinkClient) downloadFromCDN(ctx context.Context, encryptQueryParam, aesKeyBase64 string) ([]byte, error) {
	cdnURL := buildCDNDownloadURL(encryptQueryParam, cdnBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cdnURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create CDN download request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("CDN download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("CDN download failed: status %d", resp.StatusCode)
	}

	encrypted, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read CDN response: %w", err)
	}

	// Parse AES key: base64 decode, then handle two formats
	decoded, err := base64.StdEncoding.DecodeString(aesKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("decode aes_key: %w", err)
	}

	var aesKey []byte
	switch len(decoded) {
	case 16:
		aesKey = decoded
	case 32:
		// Hex string → decode to 16 bytes
		aesKey, err = hexDecode(decoded)
		if err != nil {
			return nil, fmt.Errorf("hex decode aes_key: %w", err)
		}
	default:
		return nil, fmt.Errorf("unexpected aes_key length after base64: %d", len(decoded))
	}

	return aesECBDecrypt(encrypted, aesKey)
}

// hexDecode decodes a hex-encoded byte slice to raw bytes.
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

// generateClientID creates a unique message ID for deduplication.
func generateClientID() string {
	var buf [4]byte
	rand.Read(buf[:])
	return fmt.Sprintf("bot:%d-%x", time.Now().UnixMilli(), buf[:])
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./... -run TestBuildCDN -v`
Expected: PASS

- [ ] **Step 5: Add client_test.go tests for do() and auth headers**

Append to `client_test.go`:

```go
package wechat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ... existing CDN URL tests above ...

func TestClientDoAuthHeaders(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		w.Write([]byte(`{"ret":0}`))
	}))
	defer srv.Close()

	c := newIlinkClient(srv.URL, "test-token")
	err := c.do(context.Background(), http.MethodPost, "/test", map[string]string{"key": "val"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := gotHeaders.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := gotHeaders.Get("AuthorizationType"); got != "ilink_bot_token" {
		t.Errorf("AuthorizationType = %q", got)
	}
	if got := gotHeaders.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := gotHeaders.Get("X-WECHAT-UIN"); got == "" {
		t.Error("X-WECHAT-UIN is empty")
	}
}

func TestClientDoNoAuthWhenEmpty(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		w.Write([]byte(`{"ret":0}`))
	}))
	defer srv.Close()

	c := newIlinkClient(srv.URL, "")
	err := c.do(context.Background(), http.MethodGet, "/test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := gotHeaders.Get("Authorization"); got != "" {
		t.Errorf("Authorization should be empty, got %q", got)
	}
}

func TestClientDoAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ret":-14,"errcode":-14,"errmsg":"session expired"}`))
	}))
	defer srv.Close()

	c := newIlinkClient(srv.URL, "token")
	err := c.do(context.Background(), http.MethodPost, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.ErrCode != -14 {
		t.Errorf("ErrCode = %d, want -14", apiErr.ErrCode)
	}
}

func TestClientDoHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c := newIlinkClient(srv.URL, "token")
	err := c.do(context.Background(), http.MethodPost, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.HTTPStatus != 500 {
		t.Errorf("HTTPStatus = %d, want 500", apiErr.HTTPStatus)
	}
}

func TestClientDoResultParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ret":0,"data":"hello"}`))
	}))
	defer srv.Close()

	c := newIlinkClient(srv.URL, "token")
	var result struct {
		Data string `json:"data"`
	}
	err := c.do(context.Background(), http.MethodGet, "/test", nil, &result)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data != "hello" {
		t.Errorf("Data = %q, want %q", result.Data, "hello")
	}
}
```

- [ ] **Step 6: Run all tests — verify they pass**

Run: `go test ./... -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add client.go client_test.go
git commit -m "feat: add iLink HTTP client with wire types, CDN URL builders, and auth"
```

---

### Task 4: Storage Interface + MemoryStorage

**Files:**
- Create: `storage.go`
- Create: `storage_test.go`

- [ ] **Step 1: Write storage_test.go**

```go
package wechat

import (
	"testing"
	"time"
)

func testStorage(t *testing.T, s Storage) {
	t.Helper()

	// Load missing key
	_, found, err := s.Load("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("expected found=false for missing key")
	}

	// Save and load
	now := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	session := Session{
		ID:             "test-session",
		BotToken:       "token-123",
		BaseURL:        "https://example.com",
		Cursor:         "cursor-abc",
		ContextToken:   "ctx-tok-456",
		TokenUpdatedAt: now,
		PeerUserID:     "user-789",
	}
	if err := s.Save(session); err != nil {
		t.Fatal(err)
	}

	got, found, err := s.Load("test-session")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if got.ID != session.ID ||
		got.BotToken != session.BotToken ||
		got.BaseURL != session.BaseURL ||
		got.Cursor != session.Cursor ||
		got.ContextToken != session.ContextToken ||
		!got.TokenUpdatedAt.Equal(session.TokenUpdatedAt) ||
		got.PeerUserID != session.PeerUserID {
		t.Errorf("got %+v, want %+v", got, session)
	}

	// Update and re-load
	session.Cursor = "cursor-def"
	if err := s.Save(session); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Load("test-session")
	if got.Cursor != "cursor-def" {
		t.Errorf("Cursor = %q after update, want %q", got.Cursor, "cursor-def")
	}
}

func TestMemoryStorage(t *testing.T) {
	testStorage(t, NewMemoryStorage())
}

func TestMemoryStorageIsolation(t *testing.T) {
	s := NewMemoryStorage()
	session := Session{ID: "iso", BotToken: "tok"}
	s.Save(session)

	got, _, _ := s.Load("iso")
	got.BotToken = "mutated"

	got2, _, _ := s.Load("iso")
	if got2.BotToken != "tok" {
		t.Error("mutation leaked through — storage should copy on load")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `go test ./... -run TestMemory -v`
Expected: FAIL

- [ ] **Step 3: Write storage.go**

```go
package wechat

import (
	"sync"
	"time"
)

// Storage persists bot session state across restarts.
type Storage interface {
	Save(session Session) error
	Load(sessionID string) (Session, bool, error)
}

// Session holds all state for a single bot instance.
type Session struct {
	ID             string
	BotToken       string
	BaseURL        string
	Cursor         string
	ContextToken   string
	TokenUpdatedAt time.Time
	PeerUserID     string
}

// MemoryStorage is an in-memory Storage suitable for testing.
type memoryStorage struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

// NewMemoryStorage returns a new in-memory Storage.
func NewMemoryStorage() Storage {
	return &memoryStorage{sessions: make(map[string]Session)}
}

func (m *memoryStorage) Save(session Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
	return nil
}

func (m *memoryStorage) Load(sessionID string) (Session, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[sessionID]
	return s, ok, nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `go test ./... -run TestMemory -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add storage.go storage_test.go
git commit -m "feat: add Storage interface and MemoryStorage"
```

---

### Task 5: SQLiteStorage

**Files:**
- Create: `sqlite.go`
- Create: `sqlite_test.go`

- [ ] **Step 1: Add modernc.org/sqlite dependency**

Run:
```bash
cd /Users/albaohlson/Documents/workspace/projects/wechat-bot-go && go get modernc.org/sqlite
go get github.com/mattn/go-sqlite3 || true  # fallback not needed, just get modernc
go get modernc.org/sqlite@latest
```

Actually, for pure Go SQLite we use the `database/sql` driver from `modernc.org/sqlite`:

```bash
go get modernc.org/sqlite@latest
```

- [ ] **Step 2: Write sqlite_test.go**

```go
package wechat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteStorage(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "test.db")
	s, err := NewSQLiteStorage(dsn)
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, s)
}

func TestSQLiteStoragePersistence(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "persist.db")

	// First instance: save
	s1, err := NewSQLiteStorage(dsn)
	if err != nil {
		t.Fatal(err)
	}
	s1.Save(Session{ID: "s1", BotToken: "tok"})

	// Second instance: load from same file
	s2, err := NewSQLiteStorage(dsn)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := s2.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected found=true after reopening DB")
	}
	if got.BotToken != "tok" {
		t.Errorf("BotToken = %q, want %q", got.BotToken, "tok")
	}
}

func TestSQLiteStorageFileCreated(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "new.db")
	_, err := NewSQLiteStorage(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dsn); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}
```

- [ ] **Step 3: Run tests — verify they fail**

Run: `go test ./... -run TestSQLite -v`
Expected: FAIL (NewSQLiteStorage not defined)

- [ ] **Step 4: Write sqlite.go**

```go
package wechat

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type sqliteStorage struct {
	db *sql.DB
}

// NewSQLiteStorage creates a Storage backed by SQLite at the given file path.
func NewSQLiteStorage(dsn string) (Storage, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id               TEXT PRIMARY KEY,
		bot_token        TEXT NOT NULL DEFAULT '',
		base_url         TEXT NOT NULL DEFAULT '',
		cursor           TEXT NOT NULL DEFAULT '',
		context_token    TEXT NOT NULL DEFAULT '',
		token_updated_at DATETIME,
		peer_user_id     TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create table: %w", err)
	}
	return &sqliteStorage{db: db}, nil
}

func (s *sqliteStorage) Save(session Session) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO sessions
		(id, bot_token, base_url, cursor, context_token, token_updated_at, peer_user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.BotToken,
		session.BaseURL,
		session.Cursor,
		session.ContextToken,
		session.TokenUpdatedAt.UTC().Format(time.RFC3339),
		session.PeerUserID,
	)
	return err
}

func (s *sqliteStorage) Load(sessionID string) (Session, bool, error) {
	var session Session
	var tokenUpdatedAt string
	err := s.db.QueryRow(`SELECT id, bot_token, base_url, cursor, context_token, token_updated_at, peer_user_id
		FROM sessions WHERE id = ?`, sessionID).Scan(
		&session.ID,
		&session.BotToken,
		&session.BaseURL,
		&session.Cursor,
		&session.ContextToken,
		&tokenUpdatedAt,
		&session.PeerUserID,
	)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	if tokenUpdatedAt != "" {
		session.TokenUpdatedAt, _ = time.Parse(time.RFC3339, tokenUpdatedAt)
	}
	return session, true, nil
}
```

- [ ] **Step 5: Run tests — verify they pass**

Run: `go test ./... -run TestSQLite -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add sqlite.go sqlite_test.go go.mod go.sum
git commit -m "feat: add SQLiteStorage implementation"
```

---

### Task 6: Message Types

**Files:**
- Create: `message.go`

- [ ] **Step 1: Write message.go**

```go
package wechat

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"time"
)

// ItemType identifies the kind of content in a message item.
type ItemType int

const (
	ItemText  ItemType = 1
	ItemImage ItemType = 2
	ItemVoice ItemType = 3
	ItemFile  ItemType = 4
	ItemVideo ItemType = 5
)

// Message represents an inbound WeChat message with one or more items.
type Message struct {
	ID         string
	FromUserID string
	Timestamp  time.Time
	Items      []Item
}

// Text concatenates all TextItem content. Returns "" if no text items.
func (m Message) Text() string {
	var sb strings.Builder
	for _, item := range m.Items {
		if item.Type == ItemText && item.Text != nil {
			sb.WriteString(item.Text.Content)
		}
	}
	return sb.String()
}

// Item is a single content element in a message.
type Item struct {
	Type  ItemType
	Text  *TextItem
	Image *ImageItem
	Voice *VoiceItem
	File  *FileItem
	Video *VideoItem
}

// TextItem contains text content.
type TextItem struct {
	Content string
}

// ImageItem contains image metadata and download capability.
type ImageItem struct {
	Width    int
	Height   int
	download func() (io.ReadCloser, error)
}

// Download fetches and decrypts the image from CDN.
func (i *ImageItem) Download() (io.ReadCloser, error) {
	return i.download()
}

// VoiceItem contains voice message metadata and download capability.
type VoiceItem struct {
	Duration   int    // playtime in milliseconds
	EncodeType int    // codec: 1=pcm, 2=adpcm, 6=silk, 7=mp3, etc.
	Text       string // voice-to-text transcription (may be empty)
	download   func() (io.ReadCloser, error)
}

// Download fetches and decrypts the voice message from CDN.
func (i *VoiceItem) Download() (io.ReadCloser, error) {
	return i.download()
}

// FileItem contains file metadata and download capability.
type FileItem struct {
	FileName string
	FileSize int64
	download func() (io.ReadCloser, error)
}

// Download fetches and decrypts the file from CDN.
func (i *FileItem) Download() (io.ReadCloser, error) {
	return i.download()
}

// VideoItem contains video metadata and download capability.
type VideoItem struct {
	Duration int // play_length in seconds
	Width    int
	Height   int
	download func() (io.ReadCloser, error)
}

// Download fetches and decrypts the video from CDN.
func (i *VideoItem) Download() (io.ReadCloser, error) {
	return i.download()
}

// newDownloadFunc creates a closure that downloads and decrypts media from CDN.
func (c *ilinkClient) newDownloadFunc(encryptQueryParam, aesKeyBase64 string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		data, err := c.downloadFromCDN(context.Background(), encryptQueryParam, aesKeyBase64)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

// parseMessage converts a wire message to a public Message, injecting download closures.
func (c *ilinkClient) parseMessage(raw wireMessage) Message {
	msg := Message{
		ID:         strconv.Itoa(raw.MessageID),
		FromUserID: raw.FromUserID,
		Timestamp:  time.UnixMilli(raw.CreateTimeMs),
	}

	for _, wi := range raw.ItemList {
		item := Item{Type: ItemType(wi.Type)}
		switch ItemType(wi.Type) {
		case ItemText:
			if wi.TextItem != nil {
				item.Text = &TextItem{Content: wi.TextItem.Text}
			}
		case ItemImage:
			if wi.ImageItem != nil {
				img := &ImageItem{
					Width:  wi.ImageItem.ThumbWidth,
					Height: wi.ImageItem.ThumbHeight,
				}
				if wi.ImageItem.Media != nil && wi.ImageItem.Media.EncryptQueryParam != "" {
					img.download = c.newDownloadFunc(
						wi.ImageItem.Media.EncryptQueryParam,
						wi.ImageItem.Media.AESKey,
					)
				}
				item.Image = img
			}
		case ItemVoice:
			if wi.VoiceItem != nil {
				v := &VoiceItem{
					Duration:   wi.VoiceItem.Playtime,
					EncodeType: wi.VoiceItem.EncodeType,
					Text:       wi.VoiceItem.Text,
				}
				if wi.VoiceItem.Media != nil && wi.VoiceItem.Media.EncryptQueryParam != "" {
					v.download = c.newDownloadFunc(
						wi.VoiceItem.Media.EncryptQueryParam,
						wi.VoiceItem.Media.AESKey,
					)
				}
				item.Voice = v
			}
		case ItemFile:
			if wi.FileItem != nil {
				size, _ := strconv.ParseInt(wi.FileItem.Len, 10, 64)
				f := &FileItem{
					FileName: wi.FileItem.FileName,
					FileSize: size,
				}
				if wi.FileItem.Media != nil && wi.FileItem.Media.EncryptQueryParam != "" {
					f.download = c.newDownloadFunc(
						wi.FileItem.Media.EncryptQueryParam,
						wi.FileItem.Media.AESKey,
					)
				}
				item.File = f
			}
		case ItemVideo:
			if wi.VideoItem != nil {
				v := &VideoItem{
					Duration: wi.VideoItem.PlayLength,
					Width:    wi.VideoItem.ThumbWidth,
					Height:   wi.VideoItem.ThumbHeight,
				}
				if wi.VideoItem.Media != nil && wi.VideoItem.Media.EncryptQueryParam != "" {
					v.download = c.newDownloadFunc(
						wi.VideoItem.Media.EncryptQueryParam,
						wi.VideoItem.Media.AESKey,
					)
				}
				item.Video = v
			}
		default:
			continue // skip unknown types
		}
		msg.Items = append(msg.Items, item)
	}
	return msg
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add message.go
git commit -m "feat: add Message types with download closures and wire parsing"
```

---

### Task 7: Bot — Types, Options, NewBot

**Files:**
- Create: `bot.go`

- [ ] **Step 1: Write bot.go with types, options, and NewBot**

```go
package wechat

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// QRCode contains the data returned when requesting a login QR code.
type QRCode struct {
	URL         string // scannable QR code content/URL
	ImageBase64 string // pre-rendered PNG as base64
}

// QRHandler is called when a QR code is available for scanning.
type QRHandler func(qr QRCode)

// Option configures a Bot.
type Option func(*options)

type options struct {
	storage   Storage
	sessionID string
	qrHandler QRHandler
}

// WithStorage sets the storage backend for session persistence.
func WithStorage(s Storage) Option {
	return func(o *options) { o.storage = s }
}

// WithSessionID sets the session ID for this bot instance. Default is "default".
func WithSessionID(id string) Option {
	return func(o *options) { o.sessionID = id }
}

// WithQRHandler sets the callback for QR code display during login.
func WithQRHandler(fn QRHandler) Option {
	return func(o *options) { o.qrHandler = fn }
}

// Bot is a WeChat bot that manages login, message polling, and sending.
// One Bot instance handles a single conversation with one user.
type Bot struct {
	mu       sync.Mutex
	opts     options
	session  Session
	client   *ilinkClient
	handler  func(msg Message)
	cancel   context.CancelFunc
}

// NewBot creates a new Bot with the given options.
func NewBot(opts ...Option) *Bot {
	o := options{
		sessionID: "default",
		storage:   NewMemoryStorage(),
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &Bot{opts: o}
}

// OnMessage registers the message handler. Called once per inbound message.
func (b *Bot) OnMessage(h func(msg Message)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handler = h
}

// Start begins the bot lifecycle: load session -> login (if needed) -> poll messages.
// Blocks until ctx is cancelled, Stop() is called, or a fatal error occurs.
func (b *Bot) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.cancel = cancel
	b.mu.Unlock()
	defer cancel()

	// Load session
	session, found, err := b.opts.storage.Load(b.opts.sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	if found {
		b.session = session
	} else {
		b.session = Session{ID: b.opts.sessionID}
	}

	// Login if needed
	if b.session.BotToken == "" {
		if err := b.login(ctx); err != nil {
			return err
		}
	}

	// Create client
	baseURL := b.session.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	b.client = newIlinkClient(baseURL, b.session.BotToken)

	// Poll messages
	return b.poll(ctx)
}

// Stop gracefully stops the bot. Start() will return nil.
func (b *Bot) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cancel != nil {
		b.cancel()
	}
}

func (b *Bot) login(ctx context.Context) error {
	if b.opts.qrHandler == nil {
		return fmt.Errorf("wechat: QR handler is required for login (use WithQRHandler)")
	}

	tmpClient := newIlinkClient(defaultBaseURL, "")

	// Get QR code
	qrResp, err := tmpClient.getBotQRCode(ctx)
	if err != nil {
		return fmt.Errorf("get QR code: %w", err)
	}

	// Notify caller
	b.opts.qrHandler(QRCode{
		URL:         qrResp.QRCode,
		ImageBase64: qrResp.QRCodeImgContent,
	})

	// Poll status
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(qrPollInterval):
		}

		status, err := tmpClient.getQRCodeStatus(ctx, qrResp.QRCode)
		if err != nil {
			return fmt.Errorf("get QR status: %w", err)
		}

		switch status.Status {
		case "confirmed":
			b.session.BotToken = status.BotToken
			b.session.BaseURL = status.BaseURL
			if b.session.BaseURL == "" {
				b.session.BaseURL = defaultBaseURL
			}
			if err := b.opts.storage.Save(b.session); err != nil {
				return fmt.Errorf("save session after login: %w", err)
			}
			return nil
		case "expired":
			return fmt.Errorf("wechat: QR code expired")
		case "wait", "scaned":
			continue
		}
	}
}

func (b *Bot) poll(ctx context.Context) error {
	consecutiveErrors := 0

	for {
		select {
		case <-ctx.Done():
			b.opts.storage.Save(b.session)
			return nil
		default:
		}

		resp, err := b.client.getUpdates(ctx, b.session.Cursor)
		if err != nil {
			if ctx.Err() != nil {
				b.opts.storage.Save(b.session)
				return nil
			}

			// Check for session expired
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.ErrCode == -14 {
				return ErrSessionExpired
			}

			consecutiveErrors++
			if consecutiveErrors >= 5 {
				return fmt.Errorf("wechat: %d consecutive poll errors, last: %w", consecutiveErrors, err)
			}

			select {
			case <-ctx.Done():
				b.opts.storage.Save(b.session)
				return nil
			case <-time.After(5 * time.Second):
			}
			continue
		}

		consecutiveErrors = 0

		// Update cursor
		if resp.GetUpdatesBuf != "" {
			b.session.Cursor = resp.GetUpdatesBuf
		}

		// Process messages
		for _, raw := range resp.Msgs {
			// Skip group messages
			if raw.GroupID != "" {
				continue
			}
			// Skip bot's own messages
			if raw.MessageType == 2 {
				continue
			}

			// Update context token and peer user ID
			if raw.ContextToken != "" {
				b.session.ContextToken = raw.ContextToken
				b.session.TokenUpdatedAt = time.Now()
			}
			if raw.FromUserID != "" {
				b.session.PeerUserID = raw.FromUserID
			}

			// Dispatch to handler
			b.mu.Lock()
			h := b.handler
			b.mu.Unlock()
			if h != nil {
				msg := b.client.parseMessage(raw)
				h(msg)
			}
		}

		// Save session after processing
		if len(resp.Msgs) > 0 {
			b.opts.storage.Save(b.session)
		}
	}
}

// --- Send methods ---

func (b *Bot) checkContextToken() error {
	if b.session.ContextToken == "" {
		return ErrNoContextToken
	}
	if time.Since(b.session.TokenUpdatedAt) > 24*time.Hour {
		return ErrContextTokenExpired
	}
	return nil
}

// Send sends a text message to the conversation user.
func (b *Bot) Send(text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.client == nil {
		return fmt.Errorf("wechat: bot not started")
	}
	if err := b.checkContextToken(); err != nil {
		return err
	}

	req := wireSendRequest{
		BaseInfo: newBaseInfo(),
		Msg: wireSendMsg{
			FromUserID:   "",
			ToUserID:     b.session.PeerUserID,
			ClientID:     generateClientID(),
			MessageType:  2,
			MessageState: 2,
			ContextToken: b.session.ContextToken,
			ItemList: []wireItem{
				{
					Type:     int(ItemText),
					TextItem: &wireTextItem{Text: text},
				},
			},
		},
	}
	return b.client.sendMessage(context.Background(), req)
}

// SendImage sends an image to the conversation user.
func (b *Bot) SendImage(r io.Reader, filename string) error {
	return b.sendMedia(r, filename, ItemImage)
}

// SendVoice sends a voice message to the conversation user.
func (b *Bot) SendVoice(r io.Reader, filename string) error {
	return b.sendMedia(r, filename, ItemVoice)
}

// SendFile sends a file to the conversation user.
func (b *Bot) SendFile(r io.Reader, filename string) error {
	return b.sendMedia(r, filename, ItemFile)
}

// SendVideo sends a video to the conversation user.
func (b *Bot) SendVideo(r io.Reader, filename string) error {
	return b.sendMedia(r, filename, ItemVideo)
}

func (b *Bot) sendMedia(r io.Reader, filename string, itemType ItemType) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.client == nil {
		return fmt.Errorf("wechat: bot not started")
	}
	if err := b.checkContextToken(); err != nil {
		return err
	}

	// Read file
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	// Compute metadata
	rawSize := len(data)
	hash := md5.Sum(data)
	rawFileMD5 := hex.EncodeToString(hash[:])

	// Generate crypto params
	aesKey := make([]byte, 16)
	rand.Read(aesKey)
	filekeyBytes := make([]byte, 16)
	rand.Read(filekeyBytes)
	filekey := hex.EncodeToString(filekeyBytes)
	paddedSize := aesECBPaddedSize(rawSize)

	// aeskey for getUploadURL: just hex string
	aesKeyHex := hex.EncodeToString(aesKey)

	// Get upload URL
	ctx := context.Background()
	uploadResp, err := b.client.getUploadURL(ctx, wireUploadURLRequest{
		FileKey:     filekey,
		MediaType:   uploadMediaType(itemType),
		ToUserID:    b.session.PeerUserID,
		RawSize:     rawSize,
		RawFileMD5:  rawFileMD5,
		FileSize:    paddedSize,
		NoNeedThumb: true,
		AESKey:      aesKeyHex,
		BaseInfo:    newBaseInfo(),
	})
	if err != nil {
		return fmt.Errorf("get upload URL: %w", err)
	}

	// Encrypt
	encrypted, err := aesECBEncrypt(data, aesKey)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	// Upload to CDN
	cdnURL := buildCDNUploadURL(cdnBaseURL, uploadResp.UploadParam, filekey)
	downloadParam, err := b.client.uploadToCDN(ctx, cdnURL, encrypted)
	if err != nil {
		return fmt.Errorf("upload to CDN: %w", err)
	}

	// aes_key for cdnMedia: base64(hex(key))
	aesKeyB64 := base64.StdEncoding.EncodeToString([]byte(aesKeyHex))
	cdnMedia := &wireCDNMedia{
		EncryptQueryParam: downloadParam,
		AESKey:            aesKeyB64,
		EncryptType:       1,
	}

	// Build item
	item := wireItem{Type: int(itemType)}
	switch itemType {
	case ItemImage:
		item.ImageItem = &wireImageItem{Media: cdnMedia, MidSize: paddedSize}
	case ItemVoice:
		item.VoiceItem = &wireVoiceItem{Media: cdnMedia}
	case ItemFile:
		item.FileItem = &wireFileItem{Media: cdnMedia, FileName: filename, Len: fmt.Sprintf("%d", rawSize)}
	case ItemVideo:
		item.VideoItem = &wireVideoItem{Media: cdnMedia, VideoSize: paddedSize}
	}

	// Send message
	req := wireSendRequest{
		BaseInfo: newBaseInfo(),
		Msg: wireSendMsg{
			FromUserID:   "",
			ToUserID:     b.session.PeerUserID,
			ClientID:     generateClientID(),
			MessageType:  2,
			MessageState: 2,
			ContextToken: b.session.ContextToken,
			ItemList:     []wireItem{item},
		},
	}
	return b.client.sendMessage(ctx, req)
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add bot.go
git commit -m "feat: add Bot with login, polling, and send methods"
```

---

### Task 8: Bot Tests

**Files:**
- Create: `bot_test.go`

- [ ] **Step 1: Write bot_test.go**

```go
package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockServer returns an httptest.Server that simulates the iLink Bot API.
func mockServer(t *testing.T, opts mockOpts) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	pollCount := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch {
		case r.URL.Path == "/ilink/bot/get_bot_qrcode":
			json.NewEncoder(w).Encode(wireQRCodeResponse{
				QRCode:           "test-qr-url",
				QRCodeImgContent: "dGVzdC1pbWFnZQ==",
			})

		case r.URL.Path == "/ilink/bot/get_qrcode_status":
			json.NewEncoder(w).Encode(wireQRCodeStatusResponse{
				Status:   "confirmed",
				BotToken: "test-bot-token",
				BaseURL:  "",
			})

		case r.URL.Path == "/ilink/bot/getupdates":
			pollCount++
			if pollCount == 1 && opts.firstPollMsgs != nil {
				json.NewEncoder(w).Encode(getUpdatesResponse{
					Ret:           0,
					Msgs:          opts.firstPollMsgs,
					GetUpdatesBuf: "cursor-1",
				})
			} else if opts.sessionExpired && pollCount == 2 {
				json.NewEncoder(w).Encode(getUpdatesResponse{
					Ret:     -14,
					ErrCode: -14,
					ErrMsg:  "session expired",
				})
			} else {
				// Block until context cancelled (simulates long-poll)
				<-r.Context().Done()
			}

		case r.URL.Path == "/ilink/bot/sendmessage":
			var req wireSendRequest
			json.NewDecoder(r.Body).Decode(&req)
			if opts.onSend != nil {
				opts.onSend(req)
			}
			json.NewEncoder(w).Encode(wireSendResponse{Ret: 0})

		default:
			w.WriteHeader(404)
		}
	}))
}

type mockOpts struct {
	firstPollMsgs  []wireMessage
	sessionExpired bool
	onSend         func(wireSendRequest)
}

func TestBotLoginAndPoll(t *testing.T) {
	var gotQR QRCode
	var gotMsg Message

	srv := mockServer(t, mockOpts{
		firstPollMsgs: []wireMessage{
			{
				MessageID:    1,
				FromUserID:   "user-1",
				MessageType:  1,
				CreateTimeMs: time.Now().UnixMilli(),
				ContextToken: "ctx-tok-1",
				ItemList: []wireItem{
					{Type: 1, TextItem: &wireTextItem{Text: "hello"}},
				},
			},
		},
	})
	defer srv.Close()

	// Override defaultBaseURL for tests (via client's baseURL)
	store := NewMemoryStorage()
	bot := NewBot(
		WithStorage(store),
		WithSessionID("test"),
		WithQRHandler(func(qr QRCode) { gotQR = qr }),
	)
	bot.OnMessage(func(msg Message) {
		gotMsg = msg
		bot.Stop()
	})

	// Patch the defaultBaseURL — we need the login flow to hit our mock server.
	// We'll pre-save a session with the mock server URL to skip login.
	store.Save(Session{
		ID:       "test",
		BotToken: "test-token",
		BaseURL:  srv.URL,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := bot.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if gotMsg.Text() != "hello" {
		t.Errorf("msg.Text() = %q, want %q", gotMsg.Text(), "hello")
	}
	if gotMsg.FromUserID != "user-1" {
		t.Errorf("FromUserID = %q, want %q", gotMsg.FromUserID, "user-1")
	}

	// Verify session was updated
	s, _, _ := store.Load("test")
	if s.ContextToken != "ctx-tok-1" {
		t.Errorf("ContextToken = %q, want %q", s.ContextToken, "ctx-tok-1")
	}
	if s.PeerUserID != "user-1" {
		t.Errorf("PeerUserID = %q, want %q", s.PeerUserID, "user-1")
	}
	if s.Cursor != "cursor-1" {
		t.Errorf("Cursor = %q, want %q", s.Cursor, "cursor-1")
	}
}

func TestBotSessionExpired(t *testing.T) {
	srv := mockServer(t, mockOpts{
		firstPollMsgs: []wireMessage{
			{
				MessageID:    1,
				FromUserID:   "user-1",
				MessageType:  1,
				ContextToken: "tok",
				ItemList:     []wireItem{{Type: 1, TextItem: &wireTextItem{Text: "hi"}}},
			},
		},
		sessionExpired: true,
	})
	defer srv.Close()

	store := NewMemoryStorage()
	store.Save(Session{ID: "test", BotToken: "tok", BaseURL: srv.URL})

	bot := NewBot(WithStorage(store), WithSessionID("test"))
	bot.OnMessage(func(msg Message) {})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := bot.Start(ctx)
	if err != ErrSessionExpired {
		t.Errorf("Start = %v, want ErrSessionExpired", err)
	}
}

func TestBotSendText(t *testing.T) {
	var sentReq wireSendRequest
	srv := mockServer(t, mockOpts{
		firstPollMsgs: []wireMessage{
			{
				MessageID:    1,
				FromUserID:   "user-1",
				MessageType:  1,
				ContextToken: "ctx-tok",
				ItemList:     []wireItem{{Type: 1, TextItem: &wireTextItem{Text: "hi"}}},
			},
		},
		onSend: func(req wireSendRequest) { sentReq = req },
	})
	defer srv.Close()

	store := NewMemoryStorage()
	store.Save(Session{ID: "test", BotToken: "tok", BaseURL: srv.URL})

	bot := NewBot(WithStorage(store), WithSessionID("test"))
	bot.OnMessage(func(msg Message) {
		bot.Send("echo: " + msg.Text())
		bot.Stop()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bot.Start(ctx)

	if sentReq.Msg.ToUserID != "user-1" {
		t.Errorf("ToUserID = %q, want %q", sentReq.Msg.ToUserID, "user-1")
	}
	if sentReq.Msg.ContextToken != "ctx-tok" {
		t.Errorf("ContextToken = %q, want %q", sentReq.Msg.ContextToken, "ctx-tok")
	}
	if len(sentReq.Msg.ItemList) != 1 || sentReq.Msg.ItemList[0].TextItem == nil {
		t.Fatal("expected 1 text item")
	}
	if sentReq.Msg.ItemList[0].TextItem.Text != "echo: hi" {
		t.Errorf("text = %q, want %q", sentReq.Msg.ItemList[0].TextItem.Text, "echo: hi")
	}
	if sentReq.Msg.MessageType != 2 {
		t.Errorf("MessageType = %d, want 2", sentReq.Msg.MessageType)
	}
	if sentReq.Msg.MessageState != 2 {
		t.Errorf("MessageState = %d, want 2", sentReq.Msg.MessageState)
	}
	if sentReq.Msg.FromUserID != "" {
		t.Errorf("FromUserID = %q, want empty", sentReq.Msg.FromUserID)
	}
	if !strings.HasPrefix(sentReq.Msg.ClientID, "bot:") {
		t.Errorf("ClientID = %q, want prefix 'bot:'", sentReq.Msg.ClientID)
	}
}

func TestBotSendNoContextToken(t *testing.T) {
	srv := mockServer(t, mockOpts{})
	defer srv.Close()

	store := NewMemoryStorage()
	store.Save(Session{ID: "test", BotToken: "tok", BaseURL: srv.URL})

	bot := NewBot(WithStorage(store), WithSessionID("test"))

	// Start in background
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go bot.Start(ctx)

	// Wait for bot to start
	time.Sleep(100 * time.Millisecond)

	err := bot.Send("should fail")
	if err != ErrNoContextToken {
		t.Errorf("Send = %v, want ErrNoContextToken", err)
	}
	bot.Stop()
}

func TestBotSkipsGroupMessages(t *testing.T) {
	received := 0
	srv := mockServer(t, mockOpts{
		firstPollMsgs: []wireMessage{
			{
				MessageID:    1,
				FromUserID:   "user-1",
				GroupID:      "group-1",
				MessageType:  1,
				ContextToken: "tok",
				ItemList:     []wireItem{{Type: 1, TextItem: &wireTextItem{Text: "group msg"}}},
			},
			{
				MessageID:    2,
				FromUserID:   "user-1",
				MessageType:  1,
				ContextToken: "tok",
				ItemList:     []wireItem{{Type: 1, TextItem: &wireTextItem{Text: "dm"}}},
			},
		},
	})
	defer srv.Close()

	store := NewMemoryStorage()
	store.Save(Session{ID: "test", BotToken: "tok", BaseURL: srv.URL})

	bot := NewBot(WithStorage(store), WithSessionID("test"))
	bot.OnMessage(func(msg Message) {
		received++
		if msg.Text() != "dm" {
			t.Errorf("got %q, expected only the DM", msg.Text())
		}
		bot.Stop()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bot.Start(ctx)

	if received != 1 {
		t.Errorf("received %d messages, want 1 (group message should be skipped)", received)
	}
}
```

- [ ] **Step 2: Run tests — verify they pass**

Run: `go test ./... -run TestBot -v -timeout 30s`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add bot_test.go
git commit -m "test: add bot tests for login, polling, send, and error handling"
```

---

### Task 9: Example Test

**Files:**
- Create: `example_test.go`

- [ ] **Step 1: Write example_test.go**

This should compile and pass `go vet` but is not runnable against real servers.

```go
package wechat_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	wechat "github.com/fuzhong-jiye/wechat-bot-go"
)

func Example() {
	store, err := wechat.NewSQLiteStorage("bot.db")
	if err != nil {
		log.Fatal(err)
	}

	bot := wechat.NewBot(
		wechat.WithStorage(store),
		wechat.WithSessionID("my-bot"),
		wechat.WithQRHandler(func(qr wechat.QRCode) {
			fmt.Println("Scan QR code to login:", qr.URL)
		}),
	)

	bot.OnMessage(func(msg wechat.Message) {
		for _, item := range msg.Items {
			switch item.Type {
			case wechat.ItemText:
				bot.Send("You said: " + item.Text.Content)
			case wechat.ItemImage:
				reader, err := item.Image.Download()
				if err != nil {
					log.Println("download failed:", err)
					continue
				}
				reader.Close()
				bot.Send("Image received")
			}
		}
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				bot.Send("Periodic message")
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		err := bot.Start(ctx)
		if ctx.Err() != nil {
			break
		}
		log.Println("bot exited:", err)
		time.Sleep(5 * time.Second)
	}
}
```

- [ ] **Step 2: Verify compilation**

Run: `go vet ./...`
Expected: success

- [ ] **Step 3: Commit**

```bash
git add example_test.go
git commit -m "docs: add compilable example test"
```

---

### Task 10: Final Integration Check

- [ ] **Step 1: Run full test suite**

Run: `go test ./... -v -timeout 60s`
Expected: all tests PASS

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: clean

- [ ] **Step 3: Verify clean git state**

Run: `git status`
Expected: clean working tree

- [ ] **Step 4: Review test coverage**

Run: `go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out`
Review: ensure crypto, storage, and bot polling/send paths are covered.

- [ ] **Step 5: Clean up coverage file**

Run: `rm coverage.out`
