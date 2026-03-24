# WeChat Bot Go SDK — Implementation Design

> Module: `github.com/fuzhong-jiye/wechat-bot-go`
> Date: 2026-03-24
> Reference: `docs/design.md` (API spec), TypeScript reference at `~/Documents/workspace/projects/wechat-bot`

## 1. Approach

Bottom-up: crypto → HTTP client → storage → message types → bot orchestrator. Each layer is independently testable with no upward dependencies.

## 2. Package & File Layout

Single package `wechat`. No sub-packages.

```
wechat-bot-go/
├── go.mod
├── docs/
├── crypto.go            # AES-128-ECB encrypt/decrypt (unexported)
├── crypto_test.go
├── client.go            # ilinkClient, auth headers, API methods (unexported)
├── client_test.go
├── storage.go           # Storage interface, Session, MemoryStorage
├── storage_test.go
├── sqlite.go            # SQLiteStorage
├── sqlite_test.go
├── message.go           # Message, Item, ItemType, *Item types
├── bot.go               # Bot, Option, Start/Stop/Send/OnMessage
├── bot_test.go
├── errors.go            # Sentinel errors, APIError
└── example_test.go      # Testable example from design doc §8
```

## 3. Crypto Layer (`crypto.go`)

Unexported. Pure stdlib (`crypto/aes`).

```go
func aesECBEncrypt(data, key []byte) ([]byte, error)
func aesECBDecrypt(data, key []byte) ([]byte, error)
func aesECBPaddedSize(rawSize int) int
```

- AES-128, ECB mode (no IV), PKCS7 padding
- Go stdlib has no ECB mode — encrypt/decrypt block-by-block (16 bytes)
- PKCS7 always adds padding; if input is block-aligned, adds a full 16-byte block

### Key encoding (critical)

**Sending (upload):**
- Generate 16 random bytes as AES key
- For the `aeskey` API field: `base64(hex(key))` — hex-encode the 16 bytes to a 32-char string, then base64-encode that string

**Receiving (download):**
- Base64-decode the `aes_key` field from the message
- If result is 16 bytes → use directly as AES key
- If result is 32 bytes → it's a hex string, hex-decode to 16 bytes

### Tests

- Round-trip encrypt/decrypt
- Known test vectors
- Padding edge cases (exact block multiple, empty input)
- Invalid key length → error
- Key encoding: verify `base64(hex(key))` round-trip
- Both key formats in decryption path

## 4. HTTP Client (`client.go`)

Unexported struct, all API communication.

```go
type ilinkClient struct {
    baseURL    string
    botToken   string
    httpClient *http.Client
}
```

### Constants

```go
const (
    defaultBaseURL  = "https://ilinkai.weixin.qq.com"
    cdnBaseURL      = "https://novac2c.cdn.weixin.qq.com/c2c"
    channelVersion  = "1.0.2"
    apiTimeout      = 15 * time.Second
    pollTimeout     = 40 * time.Second
    qrPollInterval  = 2 * time.Second
)
```

### Auth headers (every request)

```go
"Content-Type":      "application/json"
"AuthorizationType": "ilink_bot_token"
"X-WECHAT-UIN":      base64(strconv.FormatUint(rand.Uint32(), 10))  // fresh per request
"Authorization":     "Bearer " + botToken  // omitted if botToken is empty
```

### Unified request method

```go
func (c *ilinkClient) do(ctx context.Context, method, path string, body, result any) error
```

- Serializes body to JSON
- Builds request with auth headers
- Sends via `httpClient.Do()`
- Checks HTTP status
- Parses JSON response
- Checks both `ret` and `errcode` fields — if either non-zero, returns `*APIError`

Note: each request struct carries its own `BaseInfo` field. Call-site helpers populate it before passing to `do()`.

### API methods

| Method | HTTP | Path | Notes |
|--------|------|------|-------|
| `getBotQRCode(ctx)` | GET | `/ilink/bot/get_bot_qrcode?bot_type=3` | No auth |
| `getQRCodeStatus(ctx, qrCode)` | GET | `/ilink/bot/get_qrcode_status?qrcode=...` | No auth, extra `iLink-App-ClientVersion: 1` header |
| `getUpdates(ctx, cursor)` | POST | `/ilink/bot/getupdates` | 40s timeout child context |
| `sendMessage(ctx, msg)` | POST | `/ilink/bot/sendmessage` | 15s timeout |
| `getUploadURL(ctx, req)` | POST | `/ilink/bot/getuploadurl` | |
| `uploadToCDN(ctx, url, data)` | POST | CDN URL | `Content-Type: application/octet-stream`, retry 3x on 5xx, fail immediately on 4xx |

### CDN URL builders (unexported)

```go
func buildCDNUploadURL(cdnBaseURL, uploadParam, filekey string) string
func buildCDNDownloadURL(encryptQueryParam, cdnBaseURL string) string
```

Download URL format: `{cdnBaseURL}/download?encrypted_query_param={urlEncoded(param)}`

### Wire format structs

All unexported, matching the iLink Bot API. Each POST request struct includes a `BaseInfo` field set by the caller.

Key request structs:

```go
type sendRequest struct {
    BaseInfo     baseInfo `json:"base_info"`
    Msg          sendMsg  `json:"msg"`
}

type sendMsg struct {
    FromUserID   string     `json:"from_user_id"`    // always ""
    ToUserID     string     `json:"to_user_id"`
    ClientID     string     `json:"client_id"`       // "bot:{unix_ms}-{hex4}"
    MessageType  int        `json:"message_type"`    // 2 = outbound bot
    MessageState int        `json:"message_state"`   // 2 = FINISH
    ContextToken string     `json:"context_token"`
    ItemList     []wireItem `json:"item_list"`
}

type getUpdatesRequest struct {
    GetUpdatesBuf string   `json:"get_updates_buf"`
    BaseInfo      baseInfo `json:"base_info"`
}

type uploadURLRequest struct {
    FileKey      string   `json:"filekey"`
    MediaType    int      `json:"media_type"`       // 2=image, 3=voice, 4=file, 5=video
    ToUserID     string   `json:"to_user_id"`
    RawSize      int      `json:"rawsize"`
    RawFileMD5   string   `json:"rawfilemd5"`
    FileSize     int      `json:"filesize"`          // aesECBPaddedSize(rawSize)
    NoNeedThumb  bool     `json:"no_need_thumb"`     // always true
    AESKey       string   `json:"aeskey"`            // base64(hex(key))
    BaseInfo     baseInfo `json:"base_info"`
}
```

### Tests

- `do()` method against `httptest.Server`: verify headers, body shape, error code handling
- Auth header generation (random UIN, bearer token presence/absence)
- CDN URL builders

## 5. Storage (`storage.go`, `sqlite.go`)

### Interface

```go
type Storage interface {
    Save(session Session) error
    Load(sessionID string) (Session, bool, error)
}

type Session struct {
    ID             string
    BotToken       string
    BaseURL        string
    Cursor         string
    ContextToken   string
    TokenUpdatedAt time.Time
    PeerUserID     string
}
```

### MemoryStorage (`storage.go`)

`sync.RWMutex` + `map[string]Session`. Copies on Save/Load to prevent aliasing.

```go
func NewMemoryStorage() Storage
```

### SQLiteStorage (`sqlite.go`)

Uses `modernc.org/sqlite` (pure Go, no CGO). Creates table on construction.

```go
func NewSQLiteStorage(dsn string) (Storage, error)
```

Schema:

```sql
CREATE TABLE IF NOT EXISTS sessions (
    id               TEXT PRIMARY KEY,
    bot_token        TEXT NOT NULL,
    base_url         TEXT NOT NULL,
    cursor           TEXT NOT NULL DEFAULT '',
    context_token    TEXT NOT NULL DEFAULT '',
    token_updated_at DATETIME,
    peer_user_id     TEXT NOT NULL DEFAULT ''
);
```

`Save` uses `INSERT OR REPLACE`. `Load` uses `SELECT WHERE id = ?`.

### Call sites

| Event | Action |
|-------|--------|
| `Start()` | `Load(sessionID)` |
| Login success | `Save(session)` |
| Each poll with new messages | `Save(session)` — cursor + contextToken + peerUserID |
| `Stop()` | `Save(session)` |

### Tests

- Save/Load round-trip, load missing key returns `(zero, false, nil)`
- SQLiteStorage: same tests against temp file, plus concurrent access

## 6. Message Types (`message.go`)

### Public types

```go
type ItemType int

const (
    ItemText  ItemType = 1
    ItemImage ItemType = 2
    ItemVoice ItemType = 3
    ItemFile  ItemType = 4
    ItemVideo ItemType = 5
)

type Message struct {
    ID         string
    FromUserID string
    Timestamp  time.Time
    Items      []Item
}

func (m Message) Text() string  // concatenates all TextItem.Content

type Item struct {
    Type  ItemType
    Text  *TextItem
    Image *ImageItem
    Voice *VoiceItem
    File  *FileItem
    Video *VideoItem
}
```

### Item types

```go
type TextItem struct {
    Content string
}

type ImageItem struct {
    Width    int  // thumb_width from API
    Height   int  // thumb_height from API
    download func() (io.ReadCloser, error)
}
func (i *ImageItem) Download() (io.ReadCloser, error)

type VoiceItem struct {
    Duration   int    // playtime in milliseconds
    EncodeType int    // codec: 1=pcm, 2=adpcm, 6=silk, 7=mp3, etc.
    Text       string // voice-to-text transcription (may be empty)
    download   func() (io.ReadCloser, error)
}
func (i *VoiceItem) Download() (io.ReadCloser, error)

type FileItem struct {
    FileName string
    FileSize int64
    download func() (io.ReadCloser, error)
}
func (i *FileItem) Download() (io.ReadCloser, error)

type VideoItem struct {
    Duration int  // play_length in seconds
    Width    int  // thumb_width
    Height   int  // thumb_height
    download func() (io.ReadCloser, error)
}
func (i *VideoItem) Download() (io.ReadCloser, error)
```

### Download closure

Injected at message parse time, captures `ilinkClient` reference:

```go
func (c *ilinkClient) newDownloadFunc(encryptQueryParam, aesKeyBase64 string) func() (io.ReadCloser, error)
```

Flow: build CDN download URL → HTTP GET → AES-ECB decrypt (handling both 16-byte and 32-byte-hex key formats) → wrap in `io.NopCloser`.

### Content type

Not provided by the API for any media type. Matching TS behavior:
- Images: no format info (treat as opaque binary)
- Voice: `EncodeType` field indicates codec
- Files: `FileName` extension is the only hint
- Video: no format info

Callers needing MIME detection can use `http.DetectContentType()` on downloaded bytes.

### Wire format parsing

```go
func (c *ilinkClient) parseMessage(raw rawMessage) Message
```

Maps `item_list` entries by type, constructs appropriate `*XxxItem` with download closures for media types. Skips items with unknown types.

## 7. Bot (`bot.go`)

### Construction

```go
func NewBot(opts ...Option) *Bot

type Option func(*options)

func WithStorage(s Storage) Option
func WithSessionID(id string) Option       // default "default"
func WithQRHandler(fn QRHandler) Option    // required

type QRHandler func(qr QRCode)

type QRCode struct {
    URL         string // qrcode field — the scannable content/URL
    ImageBase64 string // qrcode_img_content — pre-rendered PNG as base64
}
```

### Internal state

```go
type Bot struct {
    mu       sync.Mutex
    opts     options
    session  Session
    client   *ilinkClient
    handler  func(msg Message)
    stopFunc context.CancelFunc
}
```

### Start(ctx) flow

1. `storage.Load(sessionID)`
2. If valid `botToken` → create `ilinkClient` → skip to step 4
3. Login flow:
   - `getBotQRCode()` → call `QRHandler` with `QRCode{URL, ImageBase64}`
   - Poll `getQRCodeStatus(qrCode)` every 2 seconds
   - On `status == "confirmed"` → extract `botToken` + `baseURL` → `storage.Save()`
   - On `status == "expired"` → return error
4. Polling loop:
   - `getUpdates(ctx, cursor)` with 40s timeout
   - Success → reset consecutive errors, update cursor/contextToken/peerUserID, `storage.Save()`, filter out group messages (`group_id` present), dispatch to handler
   - `errcode == -14` → return `ErrSessionExpired`
   - Other error → `consecutiveErrors++`, sleep 5s, retry. 5 consecutive → return error
5. `ctx.Done()` or `Stop()` → `storage.Save()` → return nil

### Stop()

Cancels internal context, `Start` returns nil.

### Sending

```go
func (b *Bot) Send(text string) error
func (b *Bot) SendImage(r io.Reader, filename string) error
func (b *Bot) SendVoice(r io.Reader, filename string) error
func (b *Bot) SendFile(r io.Reader, filename string) error
func (b *Bot) SendVideo(r io.Reader, filename string) error
```

All send methods:
1. Lock mu, check contextToken exists and not expired (24h from `TokenUpdatedAt`)
2. For text: build `sendRequest` with `item_list: [{type: 1, text_item: {text: ...}}]`
3. For media: `io.ReadAll(r)` → MD5 → generate 16-byte AES key + 16-byte filekey → `getUploadURL` → AES-ECB encrypt → `uploadToCDN` (retry 3x on 5xx) → build media item with `cdn_media` → `sendMessage`
4. `client_id` format: `"bot:{unix_ms}-{random_hex_4}"`

### Media send item variants

```
Image: {type: 2, image_item: {media: cdnMedia, mid_size: paddedSize}}
Voice: {type: 3, voice_item: {media: cdnMedia}}
File:  {type: 4, file_item:  {media: cdnMedia, file_name: name, len: "rawSize"}}
Video: {type: 5, video_item: {media: cdnMedia, video_size: paddedSize}}
```

Where `cdnMedia = {encrypt_query_param, aes_key: base64(hex(key)), encrypt_type: 1}`.

### Handler dispatch

Synchronous in the polling goroutine. Callers needing async should `go func()` inside their handler.

### OnMessage

```go
func (b *Bot) OnMessage(h func(msg Message))
```

Single handler. Called once per message (not per item).

### Tests

- Login flow with mock HTTP server (QR generation, status polling, credential extraction)
- Polling loop: message dispatch, cursor update, error recovery, session expiry
- Send text/media: verify request body shape, contextToken validation
- Stop: graceful shutdown, session saved

## 8. Errors (`errors.go`)

```go
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

func (e *APIError) Error() string
```

`do()` returns `*APIError` when the API response indicates failure. Callers can type-assert:

```go
var apiErr *wechat.APIError
if errors.As(err, &apiErr) {
    // inspect apiErr.ErrCode, apiErr.ErrMsg
}
```

## 9. Dependencies

```
github.com/fuzhong-jiye/wechat-bot-go
├── stdlib only (crypto/aes, net/http, encoding/json, etc.)
└── modernc.org/sqlite  (pure Go SQLite, for SQLiteStorage only)
```

## 10. Build Order

| Step | Files | Depends on | Testable in isolation |
|------|-------|------------|----------------------|
| 1 | `errors.go` | nothing | yes |
| 2 | `crypto.go`, `crypto_test.go` | nothing | yes |
| 3 | `client.go`, `client_test.go` | errors, crypto | yes (httptest) |
| 4 | `storage.go`, `storage_test.go` | nothing | yes |
| 5 | `sqlite.go`, `sqlite_test.go` | storage | yes (temp file) |
| 6 | `message.go` | client (for download closures) | yes |
| 7 | `bot.go`, `bot_test.go` | all above | yes (httptest + MemoryStorage) |
| 8 | `example_test.go` | all above | compilable example |
