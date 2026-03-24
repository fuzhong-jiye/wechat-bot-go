# WeChat Bot Go SDK 设计文档

> Module: `github.com/fuzhong-jiye/wechat-bot-go`
> Date: 2026-03-24

## 1. 概述

基于现有 TypeScript 微信 bot 项目中对 iLink Bot API 的实现，构建一个 Go SDK。定位为**高层 Bot 框架**，自动管理登录、轮询、会话状态和 contextToken，开发者只需注册消息处理回调。

### 核心模型

**一个 Bot 实例 = 一个用户的单聊对话。** Bot 登录后与唯一一个用户建立对话关系。如需与多个用户对话，创建多个 Bot 实例（使用不同 SessionID，共享同一个 Storage）。

### 约束

- 仅支持私聊场景，不考虑群聊（收到群消息时静默忽略）
- 一个 Bot 实例只处理与一个用户的对话
- 单包结构（`package wechat`）
- 底层通过 iLink Bot API（`ilinkai.weixin.qq.com`）与微信交互
- 所有 API 请求体必须包含 `base_info: { channel_version: "1.0.2" }`

## 2. Bot 生命周期

### 2.1 公开 API

```go
func NewBot(opts ...Option) *Bot

type Option func(*options)

func WithStorage(s Storage) Option
func WithSessionID(id string) Option        // 默认 "default"
func WithQRHandler(fn QRHandler) Option     // 必须提供

// Start 启动 bot：加载 session → 登录（如需）→ 开始轮询
// 阻塞直到 ctx 取消或调用 Stop
// 可重试错误（网络抖动）内部自动重试，致命错误返回 error
func (b *Bot) Start(ctx context.Context) error

// Stop 优雅停止，触发 Start 返回
func (b *Bot) Stop()

// 发送消息（handler 内外均可调用）
func (b *Bot) Send(text string) error
func (b *Bot) SendImage(r io.Reader, filename string) error
func (b *Bot) SendVoice(r io.Reader, filename string) error
func (b *Bot) SendFile(r io.Reader, filename string) error
func (b *Bot) SendVideo(r io.Reader, filename string) error

// 注册消息回调
func (b *Bot) OnMessage(h func(msg Message))
```

### 2.2 启动流程

```
Start(ctx)
  ├─ storage.Load(sessionID)
  │   ├─ 有有效 botToken → 直接进入轮询
  │   └─ 无 token → 触发登录流程
  │       ├─ 请求 QR 码
  │       ├─ 调用 QRHandler 回调
  │       ├─ 轮询扫码状态
  │       └─ 获取 credentials → storage.Save()
  ├─ 进入消息轮询循环
  │   ├─ pollMessages(cursor)
  │   ├─ 更新 cursor + contextToken → storage.Save()
  │   ├─ 分发消息到 OnMessage handler
  │   └─ 出错 → 内部重试（见 2.3）
  └─ ctx.Done() 或 Stop() → storage.Save() → return nil
```

用户侧重连模式：

```go
for {
    if err := bot.Start(ctx); err != nil {
        log.Println("bot 退出:", err)
        time.Sleep(5 * time.Second)
        continue
    }
    break // ctx 取消，正常退出
}
```

### 2.3 轮询错误恢复策略

```
轮询出错
  ├─ API 返回 errcode=-14（session 过期）
  │   → return ErrSessionExpired（致命，需重新登录）
  ├─ 其他错误
  │   → consecutiveErrors++
  │   → 等待 5 秒后重试
  │   → 连续 5 次失败 → return error（致命）
  └─ 成功 → consecutiveErrors = 0
```

```go
var ErrSessionExpired = errors.New("wechat: session expired, re-login required")
```

## 3. 消息类型与 Handler

### 3.1 Message

一条微信消息可能包含多个 item（如文本 + 图片），因此 `Message` 包含 `[]Item` 而不是拆分成独立的类型消息。

```go
type Message struct {
    ID         string
    FromUserID string
    Timestamp  time.Time
    Items      []Item
}

// 便捷方法：拼接所有 TextItem 的文本，无文本返回 ""
func (m Message) Text() string
```

### 3.2 Item 与具体类型

```go
type ItemType int

const (
    ItemText  ItemType = 1
    ItemImage ItemType = 2
    ItemVoice ItemType = 3
    ItemFile  ItemType = 4
    ItemVideo ItemType = 5
)

type Item struct {
    Type  ItemType
    Text  *TextItem
    Image *ImageItem
    Voice *VoiceItem
    File  *FileItem
    Video *VideoItem
}

type TextItem struct {
    Content string
}

type ImageItem struct {
    download func() (io.ReadCloser, error)  // 闭包，持有 ilinkClient 引用
}

func (i *ImageItem) Download() (io.ReadCloser, error)

type VoiceItem struct {
    download func() (io.ReadCloser, error)
}

func (i *VoiceItem) Download() (io.ReadCloser, error)

type FileItem struct {
    FileName string
    FileSize int64
    download func() (io.ReadCloser, error)
}

func (i *FileItem) Download() (io.ReadCloser, error)

type VideoItem struct {
    download func() (io.ReadCloser, error)
}

func (i *VideoItem) Download() (io.ReadCloser, error)
```

`Download()` 内部通过闭包实现（构造 Item 时注入 `ilinkClient` 引用）：CDN 下载 → AES-128-ECB 解密 → 返回 `io.ReadCloser`。

### 3.3 分发逻辑

Handler 在轮询 goroutine 中**同步调用**。如需长时间处理（如下载大文件），handler 应自行启动 goroutine。

```
收到消息
  → 解析 item_list → 构造 Message{Items: [...]}
  → 调用 OnMessage handler（整条消息一次回调）
```

## 4. 发送与 contextToken

### 4.1 contextToken 管理

SDK 自动管理，用户不需要感知。一个 Bot 实例只维护一个 contextToken。

- 轮询收到消息 → 更新 `session.ContextToken` + `session.TokenUpdatedAt` → 持久化
- 发送时检查 → 不存在返回 `ErrNoContextToken`，超过 24h 返回 `ErrContextTokenExpired`
- 过期 token 保留，用户重新发消息自然刷新

```go
var ErrNoContextToken = errors.New(
    "wechat: no context token, the user must message the bot first",
)

var ErrContextTokenExpired = errors.New(
    "wechat: context token expired, the user has not messaged the bot in the last 24 hours",
)
```

### 4.2 媒体发送内部流程

`SendImage(r, "photo.jpg")` 内部：

```
1. io.ReadAll(r) → []byte
2. 计算 rawfilemd5 (MD5)
3. 生成随机 aesKey (16 bytes) + filekey (hex)
4. getUploadURL API → CDN 上传地址
5. AES-128-ECB 加密
6. POST 上传到 CDN（失败重试 3 次）
7. 从 CDN 响应头读取 x-encrypted-param（下载参数）
8. sendMessage API，附带 CDN 下载参数
```

### 4.3 发送消息内部协议细节

每条发送请求必须包含：

```go
type sendRequest struct {
    BaseInfo     baseInfo `json:"base_info"`      // channel_version: "1.0.2"
    FromUserID   string   `json:"from_user_id"`   // 始终为空字符串 ""
    ToUserID     string   `json:"to_user_id"`
    MessageType  int      `json:"message_type"`   // 2 = outbound bot
    MessageState int      `json:"message_state"`  // 2 = FINISH
    ContextToken string   `json:"context_token"`
    ClientID     string   `json:"client_id"`      // 每条消息唯一，防去重
    ItemList     []item   `json:"item_list"`
}

// ClientID 格式: "bot:{timestamp}-{random_hex_4}"
// 相同 client_id 的消息只会被投递一次
```

## 5. Storage

### 5.1 接口

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
    PeerUserID     string  // 对话用户 ID，从首条入站消息获取
}
```

### 5.2 内置实现

**MemoryStorage** — `sync.RWMutex` + `map`，适合测试。

```go
func NewMemoryStorage() Storage
```

**SQLiteStorage** — 依赖 `modernc.org/sqlite`（纯 Go，无 CGO）。

```go
func NewSQLiteStorage(dsn string) (Storage, error)
```

```sql
CREATE TABLE sessions (
    id               TEXT PRIMARY KEY,
    bot_token        TEXT NOT NULL,
    base_url         TEXT NOT NULL,
    cursor           TEXT NOT NULL DEFAULT '',
    context_token    TEXT NOT NULL DEFAULT '',
    token_updated_at DATETIME,
    peer_user_id     TEXT NOT NULL DEFAULT ''
);
```

### 5.3 调用时机

```
Start()        → Load(sessionID)
登录成功        → Save(session)
每次轮询新消息  → Save(session)  // cursor + contextToken + peerUserID
Stop()         → Save(session)
```

## 6. HTTP 客户端与认证

### 6.1 ilinkClient（不导出）

```go
type ilinkClient struct {
    baseURL    string
    botToken   string
    httpClient *http.Client
}
```

### 6.2 认证头

每个请求：

```go
headers := map[string]string{
    "Content-Type":      "application/json",
    "AuthorizationType": "ilink_bot_token",
    "X-WECHAT-UIN":      base64(randomUint32()),  // 每次请求随机生成
    "Authorization":      "Bearer " + botToken,    // 登录后才有
}
```

**特殊 header：** `getQRCodeStatus` 端点额外需要 `iLink-App-ClientVersion: 1`。

**请求体：** 所有 POST 请求体必须包含 `base_info`：

```go
type baseInfo struct {
    ChannelVersion string `json:"channel_version"` // 固定 "1.0.2"
}
```

### 6.3 API 方法

```go
func (c *ilinkClient) getBotQRCode() (*QRCode, error)
func (c *ilinkClient) getQRCodeStatus(qrID string) (*LoginResult, error)
func (c *ilinkClient) getUpdates(ctx context.Context, cursor string) (*UpdatesResult, error)
func (c *ilinkClient) sendMessage(msg sendRequest) error
func (c *ilinkClient) getUploadURL(req uploadURLRequest) (*UploadURLResult, error)
func (c *ilinkClient) uploadToCDN(url string, data []byte) (downloadParam string, err error) // 重试 3 次，返回 x-encrypted-param
```

### 6.4 统一响应处理

```go
func (c *ilinkClient) do(ctx context.Context, method, path string, body, result any) error
```

内部：序列化 → 构建请求 + 认证头 → 发送 → 检查 HTTP status → 解析 JSON → 检查 `ret/errcode` → 返回 `*APIError` 或 nil。

### 6.5 QRCode

```go
type QRCode struct {
    ID  string
    URL string
}
```

### 6.6 APIError

```go
type APIError struct {
    HTTPStatus int
    Ret        int
    ErrCode    int
    ErrMsg     string
}

func (e *APIError) Error() string
```

## 7. 加解密

### AES-128-ECB（不导出）

```go
func aesECBEncrypt(data []byte, key []byte) ([]byte, error)
func aesECBDecrypt(data []byte, key []byte) ([]byte, error)
```

关键细节：

**发送方向（加密上传）：**
- 生成 16 随机字节作为 AES key
- 传给 API 时编码为 `base64(hex(key))`，**不是** `base64(key)`
- ECB 模式没有 IV
- 加密前 PKCS7 padding

**接收方向（解密下载）：**
- 从消息的 `aes_key` 字段 base64 解码后，需要处理两种格式：
  - 16 字节 → 直接作为 AES key 使用
  - 32 字节 hex 字符串 → hex 解码为 16 字节后使用
- 判断逻辑：`len(decoded) == 16 ? 直接用 : hexDecode(decoded)`

## 8. 完整用户示例

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "os/signal"
    "time"

    wechat "github.com/fuzhong-jiye/wechat-bot-go"
)

func main() {
    store, err := wechat.NewSQLiteStorage("bot.db")
    if err != nil {
        log.Fatal(err)
    }

    bot := wechat.NewBot(
        wechat.WithStorage(store),
        wechat.WithSessionID("my-bot"),
        wechat.WithQRHandler(func(qr wechat.QRCode) {
            fmt.Println("请扫描二维码登录:", qr.URL)
        }),
    )

    bot.OnMessage(func(msg wechat.Message) {
        for _, item := range msg.Items {
            switch item.Type {
            case wechat.ItemText:
                bot.Send("你说了: " + item.Text.Content)
            case wechat.ItemImage:
                reader, err := item.Image.Download()
                if err != nil {
                    log.Println("下载失败:", err)
                    continue
                }
                reader.Close()
                bot.Send("图片已收到")
            }
        }
    })

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
    defer cancel()

    // 主动发送
    go func() {
        ticker := time.NewTicker(60 * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                bot.Send("定时消息")
            case <-ctx.Done():
                return
            }
        }
    }()

    // Start 阻塞，致命错误返回，可重试
    for {
        err := bot.Start(ctx)
        if ctx.Err() != nil {
            break // 正常退出
        }
        log.Println("bot 退出:", err)
        time.Sleep(5 * time.Second)
    }
}
```
