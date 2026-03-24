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
	URL string // scannable QR code content/URL
}

// QRHandler is called when a QR code is available for scanning.
type QRHandler func(qr QRCode)

// Option configures a Bot.
type Option func(*options)

type options struct {
	storage        Storage
	sessionID      string
	qrHandler      QRHandler
	logger         Logger
	logLevel       LogLevel
	clientFactory  func(baseURL, botToken string) *ilinkClient
	qrPollInterval time.Duration
	pollRetryDelay time.Duration
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

// WithLogger sets the structured logger used by the SDK.
func WithLogger(l Logger) Option {
	return func(o *options) {
		if l == nil {
			o.logger = NopLogger{}
			return
		}
		o.logger = l
	}
}

// WithLogLevel sets the minimum level emitted by the SDK logger.
func WithLogLevel(level LogLevel) Option {
	return func(o *options) { o.logLevel = level }
}

// Bot is a WeChat bot that manages login, message polling, and sending.
type Bot struct {
	mu      sync.Mutex
	opts    options
	session Session
	client  *ilinkClient
	handler func(msg Message)
	cancel  context.CancelFunc
	runCtx  context.Context
}

// NewBot creates a new Bot with the given options.
func NewBot(opts ...Option) *Bot {
	o := options{
		sessionID:      "default",
		storage:        NewMemoryStorage(),
		logger:         NopLogger{},
		logLevel:       LogInfo,
		clientFactory:  newIlinkClient,
		qrPollInterval: qrPollInterval,
		pollRetryDelay: 5 * time.Second,
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

func (b *Bot) log(ctx context.Context, level LogLevel, msg string, fields ...Field) {
	if b.opts.logger == nil || !shouldLog(b.opts.logLevel, level) {
		return
	}

	sessionID := b.opts.sessionID
	if b.session.ID != "" {
		sessionID = b.session.ID
	}
	fields = appendFields(fields, Field{Key: "session_id", Value: sessionID})
	if b.session.BaseURL != "" {
		fields = append(fields, Field{Key: "base_url", Value: b.session.BaseURL})
	}
	if b.session.PeerUserID != "" {
		fields = append(fields, Field{Key: "peer_user_id", Value: maskID(b.session.PeerUserID)})
	}

	b.opts.logger.Log(ctx, level, msg, fields...)
}

// Start begins the bot lifecycle: load session -> login (if needed) -> poll messages.
// Blocks until ctx is cancelled, Stop() is called, or a fatal error occurs.
func (b *Bot) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.cancel = cancel
	b.runCtx = ctx
	b.mu.Unlock()
	defer func() {
		cancel()
		b.mu.Lock()
		b.runCtx = nil
		b.cancel = nil
		b.mu.Unlock()
	}()
	b.log(ctx, LogInfo, "bot.start")

	// Load session
	session, found, err := b.opts.storage.Load(b.opts.sessionID)
	if err != nil {
		b.log(ctx, LogError, "storage.load.failed", Field{Key: "error", Value: sanitizeError(err)})
		return fmt.Errorf("load session: %w", err)
	}
	if found {
		b.session = session
		b.log(ctx, LogInfo, "session.loaded", Field{Key: "found", Value: true})
	} else {
		b.session = Session{ID: b.opts.sessionID}
		b.log(ctx, LogInfo, "session.loaded", Field{Key: "found", Value: false})
	}

	// Login if needed
	if b.session.BotToken == "" {
		if err := b.login(ctx); err != nil {
			b.log(ctx, LogError, "login.failed", Field{Key: "error", Value: sanitizeError(err)})
			return err
		}
	}

	// Create client
	baseURL := b.session.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	b.client = b.opts.clientFactory(baseURL, b.session.BotToken)
	b.client.logger = b.opts.logger
	b.client.logLevel = b.opts.logLevel
	b.client.sessionID = b.session.ID
	b.client.peerUserID = func() string {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.session.PeerUserID
	}
	b.log(ctx, LogInfo, "poll.started")

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

func (b *Bot) operationContext() context.Context {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.runCtx != nil {
		return b.runCtx
	}
	return context.Background()
}

func (b *Bot) login(ctx context.Context) error {
	if b.opts.qrHandler == nil {
		return fmt.Errorf("wechat: QR handler is required for login (use WithQRHandler)")
	}

	tmpClient := b.opts.clientFactory(defaultBaseURL, "")
	tmpClient.logger = b.opts.logger
	tmpClient.logLevel = b.opts.logLevel
	tmpClient.sessionID = b.opts.sessionID

	qrResp, err := tmpClient.getBotQRCode(ctx)
	if err != nil {
		return fmt.Errorf("get QR code: %w", err)
	}
	b.log(ctx, LogInfo, "login.qrcode.requested")

	b.opts.qrHandler(QRCode{
		URL: qrResp.QRCodeImgContent,
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(b.opts.qrPollInterval):
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
				b.log(ctx, LogError, "storage.save.failed.final", Field{Key: "phase", Value: "login"}, Field{Key: "error", Value: sanitizeError(err)})
				return fmt.Errorf("save session after login: %w", err)
			}
			b.log(ctx, LogInfo, "login.confirmed")
			return nil
		case "expired":
			b.log(ctx, LogWarn, "login.qrcode.expired")
			return fmt.Errorf("wechat: QR code expired")
		case "wait", "scaned":
			b.log(ctx, LogDebug, "login.qrcode.status", Field{Key: "status", Value: status.Status})
			continue
		}
	}
}

func (b *Bot) poll(ctx context.Context) error {
	consecutiveErrors := 0

	for {
		select {
		case <-ctx.Done():
			if err := b.opts.storage.Save(b.session); err != nil {
				b.log(ctx, LogWarn, "storage.save.failed", Field{Key: "phase", Value: "shutdown"}, Field{Key: "error", Value: sanitizeError(err)})
			}
			b.log(ctx, LogInfo, "bot.stopped")
			return nil
		default:
		}

		resp, err := b.client.getUpdates(ctx, b.session.Cursor)
		if err != nil {
			if ctx.Err() != nil {
				if err := b.opts.storage.Save(b.session); err != nil {
					b.log(ctx, LogWarn, "storage.save.failed", Field{Key: "phase", Value: "shutdown"}, Field{Key: "error", Value: sanitizeError(err)})
				}
				b.log(ctx, LogInfo, "bot.stopped")
				return nil
			}

			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.ErrCode == -14 {
				b.log(ctx, LogError, "poll.failed",
					Field{Key: "reason", Value: "session_expired"},
					Field{Key: "http_status", Value: apiErr.HTTPStatus},
					Field{Key: "ret", Value: apiErr.Ret},
					Field{Key: "errcode", Value: apiErr.ErrCode},
				)
				return ErrSessionExpired
			}

			consecutiveErrors++
			b.log(ctx, LogWarn, "poll.retry",
				Field{Key: "attempt", Value: consecutiveErrors},
				Field{Key: "error", Value: sanitizeError(err)},
			)
			if consecutiveErrors >= 5 {
				b.log(ctx, LogError, "poll.failed",
					Field{Key: "attempt", Value: consecutiveErrors},
					Field{Key: "error", Value: sanitizeError(err)},
				)
				return fmt.Errorf("wechat: %d consecutive poll errors, last: %w", consecutiveErrors, err)
			}

			select {
			case <-ctx.Done():
				b.opts.storage.Save(b.session)
				return nil
			case <-time.After(b.opts.pollRetryDelay):
			}
			continue
		}

		if consecutiveErrors > 0 {
			b.log(ctx, LogInfo, "poll.recovered", Field{Key: "attempts", Value: consecutiveErrors})
		}
		consecutiveErrors = 0

		if resp.GetUpdatesBuf != "" {
			b.session.Cursor = resp.GetUpdatesBuf
		}
		if len(resp.Msgs) > 0 {
			b.log(ctx, LogDebug, "poll.batch.received", Field{Key: "msg_count", Value: len(resp.Msgs)})
		}

		for _, raw := range resp.Msgs {
			if raw.GroupID != "" {
				b.log(ctx, LogDebug, "message.ignored", Field{Key: "reason", Value: "group"})
				continue
			}
			if raw.MessageType == 2 {
				b.log(ctx, LogDebug, "message.ignored", Field{Key: "reason", Value: "outbound"})
				continue
			}

			if raw.ContextToken != "" {
				b.session.ContextToken = raw.ContextToken
				b.session.TokenUpdatedAt = time.Now()
			}
			if raw.FromUserID != "" {
				b.session.PeerUserID = raw.FromUserID
			}

			b.mu.Lock()
			h := b.handler
			b.mu.Unlock()
			if h != nil {
				msg := b.client.parseMessage(raw)
				b.log(ctx, LogInfo, "message.received",
					Field{Key: "msg_id", Value: msg.ID},
					Field{Key: "item_count", Value: len(msg.Items)},
				)
				h(msg)
			}
		}

		if len(resp.Msgs) > 0 {
			if err := b.opts.storage.Save(b.session); err != nil {
				b.log(ctx, LogWarn, "storage.save.failed", Field{Key: "phase", Value: "poll"}, Field{Key: "error", Value: sanitizeError(err)})
			}
		}
	}
}

// --- Send methods ---

func (b *Bot) checkContextToken() error {
	return checkContextTokenValue(b.session.ContextToken, b.session.TokenUpdatedAt)
}

func checkContextTokenValue(contextToken string, tokenUpdatedAt time.Time) error {
	if contextToken == "" {
		return ErrNoContextToken
	}
	if time.Since(tokenUpdatedAt) > 24*time.Hour {
		return ErrContextTokenExpired
	}
	return nil
}

func contextTokenLogMessage(err error) string {
	switch err {
	case ErrNoContextToken:
		return "context_token.missing"
	case ErrContextTokenExpired:
		return "context_token.expired"
	default:
		return "context_token.invalid"
	}
}

// Send sends a text message to the conversation user.
func (b *Bot) Send(text string) error {
	ctx := b.operationContext()

	b.mu.Lock()
	client := b.client
	peerUserID := b.session.PeerUserID
	contextToken := b.session.ContextToken
	tokenUpdatedAt := b.session.TokenUpdatedAt
	b.mu.Unlock()

	if client == nil {
		return fmt.Errorf("wechat: bot not started")
	}
	if err := checkContextTokenValue(contextToken, tokenUpdatedAt); err != nil {
		b.log(ctx, LogWarn, contextTokenLogMessage(err))
		return err
	}

	req := wireSendRequest{
		BaseInfo: newBaseInfo(),
		Msg: wireSendMsg{
			FromUserID:   "",
			ToUserID:     peerUserID,
			ClientID:     generateClientID(),
			MessageType:  2,
			MessageState: 2,
			ContextToken: contextToken,
			ItemList: []wireItem{
				{
					Type:     int(ItemText),
					TextItem: &wireTextItem{Text: text},
				},
			},
		},
	}
	err := client.sendMessage(ctx, req)
	if err != nil {
		b.log(ctx, LogError, "message.send.failed",
			Field{Key: "kind", Value: "text"},
			Field{Key: "error", Value: sanitizeError(err)},
		)
		return err
	}
	b.log(ctx, LogInfo, "message.sent", Field{Key: "kind", Value: "text"})
	return nil
}

func withClientFactory(fn func(baseURL, botToken string) *ilinkClient) Option {
	return func(o *options) { o.clientFactory = fn }
}

func withQRPollInterval(d time.Duration) Option {
	return func(o *options) { o.qrPollInterval = d }
}

func withPollRetryDelay(d time.Duration) Option {
	return func(o *options) { o.pollRetryDelay = d }
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
	ctx := b.operationContext()

	b.mu.Lock()
	client := b.client
	peerUserID := b.session.PeerUserID
	contextToken := b.session.ContextToken
	tokenUpdatedAt := b.session.TokenUpdatedAt
	b.mu.Unlock()

	if client == nil {
		return fmt.Errorf("wechat: bot not started")
	}
	if err := checkContextTokenValue(contextToken, tokenUpdatedAt); err != nil {
		b.log(ctx, LogWarn, contextTokenLogMessage(err))
		return err
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	rawSize := len(data)
	hash := md5.Sum(data)
	rawFileMD5 := hex.EncodeToString(hash[:])

	aesKey := make([]byte, 16)
	rand.Read(aesKey)
	filekeyBytes := make([]byte, 16)
	rand.Read(filekeyBytes)
	filekey := hex.EncodeToString(filekeyBytes)
	paddedSize := aesECBPaddedSize(rawSize)

	aesKeyHex := hex.EncodeToString(aesKey)

	uploadResp, err := client.getUploadURL(ctx, wireUploadURLRequest{
		FileKey:     filekey,
		MediaType:   uploadMediaType(itemType),
		ToUserID:    peerUserID,
		RawSize:     rawSize,
		RawFileMD5:  rawFileMD5,
		FileSize:    paddedSize,
		NoNeedThumb: true,
		AESKey:      aesKeyHex,
		BaseInfo:    newBaseInfo(),
	})
	if err != nil {
		b.log(ctx, LogError, "media.upload.failed",
			Field{Key: "kind", Value: itemType.String()},
			Field{Key: "error", Value: sanitizeError(err)},
		)
		return fmt.Errorf("get upload URL: %w", err)
	}

	encrypted, err := aesECBEncrypt(data, aesKey)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	cdnURL := buildCDNUploadURL(cdnBaseURL, uploadResp.UploadParam, filekey)
	downloadParam, err := client.uploadToCDN(ctx, cdnURL, encrypted)
	if err != nil {
		b.log(ctx, LogError, "media.upload.failed",
			Field{Key: "kind", Value: itemType.String()},
			Field{Key: "error", Value: sanitizeError(err)},
		)
		return fmt.Errorf("upload to CDN: %w", err)
	}
	b.log(ctx, LogInfo, "media.uploaded",
		Field{Key: "kind", Value: itemType.String()},
		Field{Key: "filename", Value: filename},
		Field{Key: "raw_size", Value: rawSize},
	)

	aesKeyB64 := base64.StdEncoding.EncodeToString([]byte(aesKeyHex))
	cdnMedia := &wireCDNMedia{
		EncryptQueryParam: downloadParam,
		AESKey:            aesKeyB64,
		EncryptType:       1,
	}

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

	req := wireSendRequest{
		BaseInfo: newBaseInfo(),
		Msg: wireSendMsg{
			FromUserID:   "",
			ToUserID:     peerUserID,
			ClientID:     generateClientID(),
			MessageType:  2,
			MessageState: 2,
			ContextToken: contextToken,
			ItemList:     []wireItem{item},
		},
	}
	err = client.sendMessage(ctx, req)
	if err != nil {
		b.log(ctx, LogError, "message.send.failed",
			Field{Key: "kind", Value: itemType.String()},
			Field{Key: "error", Value: sanitizeError(err)},
		)
		return err
	}
	b.log(ctx, LogInfo, "message.sent",
		Field{Key: "kind", Value: itemType.String()},
		Field{Key: "filename", Value: filename},
	)
	return nil
}
