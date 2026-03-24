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

## 2. 核心设计决策

| 决策 | 结论 |
|------|------|
| 定位 | 高层 Bot 框架，自动管理登录/轮询/状态 |
| Handler 模型 | 精确类型优先（OnText/OnImage/...）+ OnMessage 兜底 |
| 场景 | 仅私聊 |
| 登录 | 回调暴露 QR 数据，用户自行决定展示方式 |
| 持久化 | Storage 接口 + SQLite/Memory 内置实现，支持多 session |
| 媒体发送 | 完全封装（传 `io.Reader`） |
| 媒体接收 | 按需下载（手动调用 `Download()`） |
| 错误处理 | Error channel，用户控制重连 |
| 对话模型 | 一个 Bot 实例 = 一个用户对话，多用户用多 Bot |
| Context | 回复当前会话 + 通过 Bot 实例主动发送 |

## 3. Bot 生命周期

### 3.1 Bot 结构

```go
type Bot struct {
    opts         options
    handlers     handlerRegistry
    client       *ilinkClient
    session      *Session
    contextToken *TokenEntry      // 单个 token，对应唯一对话用户
    errors       chan *BotError
    stopCh       chan struct{}
}

func NewBot(opts ...Option) *Bot
```

### 3.2 Option 模式

```go
type Option func(*options)

func WithStorage(s Storage) Option
func WithSessionID(id string) Option        // 默认 "default"
func WithQRHandler(fn QRHandler) Option     // 必须提供
func WithPollTimeout(d time.Duration) Option // 默认 40s
func WithHTTPClient(c *http.Client) Option
```

### 3.3 生命周期方法

```go
// Start 启动 bot：加载 session → 登录（如需）→ 开始轮询
// 阻塞直到 ctx 取消或调用 Stop
func (b *Bot) Start(ctx context.Context) error

// Stop 优雅停止：停止轮询 → SaveSession → 关闭 errors channel
// 返回 error 以暴露 SaveSession 的潜在失败
func (b *Bot) Stop() error

// Errors 返回带缓冲（cap=32）的错误 channel
// 缓冲满时新错误会被丢弃（不阻塞轮询循环）
func (b *Bot) Errors() <-chan *BotError

// Reconnect 停止当前轮询循环，验证现有凭证，重新启动轮询
// 如果 session 过期（API 返回 -14），触发重新 QR 登录流程
func (b *Bot) Reconnect() error
```

### 3.4 启动流程

```
Start(ctx)
  ├─ storage.LoadSession(sessionID)
  │   ├─ 有有效 token → 直接进入轮询
  │   └─ 无 token → 触发登录流程
  │       ├─ 请求 QR 码
  │       ├─ 调用 QRHandler 回调
  │       ├─ 轮询扫码状态
  │       └─ 获取 credentials → storage.SaveSession()
  ├─ storage.LoadToken(sessionID) → 恢复 contextToken 到内存
  ├─ 进入消息轮询循环
  │   ├─ pollMessages(cursor)
  │   ├─ 更新 cursor + contextToken
  │   ├─ storage.SaveSession() + storage.SaveToken()
  │   ├─ 分发消息到 handler
  │   └─ 出错 → 写入 errors channel
  └─ ctx.Done() 或 Stop() → SaveSession() → 退出
```

### 3.5 轮询错误恢复策略

```
轮询出错
  ├─ API 返回 errcode=-14（session 过期）
  │   → 写入 errors channel（ErrPoll）
  │   → 停止轮询，等待用户调用 Reconnect()
  ├─ 其他错误
  │   → consecutiveErrors++
  │   → 等待 5 秒后重试
  │   → 连续 5 次失败 → 写入 errors channel → 停止轮询
  └─ 成功 → consecutiveErrors = 0
```

### 3.6 BotError

```go
type BotError struct {
    Type    ErrorType
    Err     error
    Context string
}

type ErrorType int

const (
    ErrPoll ErrorType = iota
    ErrSend
    ErrLogin
    ErrStorage
)

func (t ErrorType) String() string  // "poll", "send", "login", "storage"
```

## 4. 消息类型与 Handler

### 4.1 消息类型

```go
type Message struct {
    ID           string
    FromUserID   string
    ToUserID     string
    ContextToken string
    Timestamp    time.Time
    RawItems     []MessageItem
}

type TextMessage struct {
    Message
    Text string
}

type ImageMessage struct {
    Message
    media mediaRef
}

type VoiceMessage struct {
    Message
    media mediaRef
}

type FileMessage struct {
    Message
    FileName string
    FileSize int64
    media    mediaRef
}

type VideoMessage struct {
    Message
    media mediaRef
}
```

`mediaRef` 是不导出的内部结构，封装 CDN 下载参数：

```go
type mediaRef struct {
    encryptQueryParam string
    aesKey            string
    encryptType       int
    download          func() (io.ReadCloser, error)  // 闭包，持有 ilinkClient 引用
}
```

### 4.2 媒体按需下载

```go
func (m *ImageMessage) Download() (io.ReadCloser, error)
func (m *VoiceMessage) Download() (io.ReadCloser, error)
func (m *FileMessage) Download() (io.ReadCloser, error)
func (m *VideoMessage) Download() (io.ReadCloser, error)
```

内部通过 `mediaRef.download` 闭包实现（构造消息时注入 `ilinkClient` 引用）：CDN 下载 → AES-128-ECB 解密 → 返回 `io.ReadCloser`。

### 4.3 Handler 注册

```go
type TextHandler    func(ctx Context, msg TextMessage)
type ImageHandler   func(ctx Context, msg ImageMessage)
type VoiceHandler   func(ctx Context, msg VoiceMessage)
type FileHandler    func(ctx Context, msg FileMessage)
type VideoHandler   func(ctx Context, msg VideoMessage)
type MessageHandler func(ctx Context, msg Message)
type QRHandler      func(qr QRCode)

func (b *Bot) OnText(h TextHandler)
func (b *Bot) OnImage(h ImageHandler)
func (b *Bot) OnVoice(h VoiceHandler)
func (b *Bot) OnFile(h FileHandler)
func (b *Bot) OnVideo(h VideoHandler)
func (b *Bot) OnMessage(h MessageHandler)
```

### 4.4 分发逻辑

Handler 在轮询 goroutine 中**同步调用**。如需长时间处理（如下载大文件），handler 应自行启动 goroutine。

一条微信消息的 `item_list` 可能包含多个 item。每个 item 独立分发，共享同一个 Context：

```
收到消息 item_list
  ├─ type=1 (text)  → OnText handler? → 调用 : OnMessage
  ├─ type=2 (image) → OnImage handler? → 调用 : OnMessage
  ├─ type=3 (voice) → OnVoice handler? → 调用 : OnMessage
  ├─ type=4 (file)  → OnFile handler? → 调用 : OnMessage
  ├─ type=5 (video) → OnVideo handler? → 调用 : OnMessage
  └─ 未知类型 → OnMessage
```

## 5. Context 与发送

### 5.1 Context

```go
type Context struct {
    bot          *Bot
    fromUserID   string
    contextToken string
}

func (c Context) Reply(text string) error
func (c Context) ReplyImage(r io.Reader, filename string) error
func (c Context) ReplyVoice(r io.Reader, filename string) error
func (c Context) ReplyFile(r io.Reader, filename string) error
func (c Context) ReplyVideo(r io.Reader, filename string) error

func (c Context) Sender() string
func (c Context) Bot() *Bot
```

### 5.2 主动发送

通过 Bot 实例主动向对话用户发消息（不需要在 handler 中），前提是用户曾给 bot 发过消息（即存在有效 contextToken）：

```go
func (b *Bot) Send(text string) error
func (b *Bot) SendImage(r io.Reader, filename string) error
func (b *Bot) SendVoice(r io.Reader, filename string) error
func (b *Bot) SendFile(r io.Reader, filename string) error
func (b *Bot) SendVideo(r io.Reader, filename string) error
```

不需要传 `userID` — 一个 Bot 实例只对应一个用户。

### 5.3 contextToken 管理

SDK 自动管理，用户不需要感知。一个 Bot 实例只维护一个 contextToken。

- 轮询收到消息 → `bot.contextToken = TokenEntry{token, time.Now()}` → 持久化
- 发送时检查 → 不存在返回 `ErrNoContextToken`，超过 24h 返回 `ErrContextTokenExpired`
- 过期 token 保留，用户重新发消息自然刷新

```go
var ErrNoContextToken = errors.New(
    "wechat: no context token, " +
    "the user must message the bot first",
)

var ErrContextTokenExpired = errors.New(
    "wechat: context token expired, " +
    "the user has not messaged the bot in the last 24 hours",
)
```

### 5.4 媒体发送内部流程（用户不感知）

`ReplyImage(r, "photo.jpg")` 内部：

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

### 5.5 发送消息内部协议细节

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

## 6. Storage

### 6.1 接口

```go
type Storage interface {
    SaveSession(session Session) error
    LoadSession(sessionID string) (Session, bool, error)
    DeleteSession(sessionID string) error
    ListSessions() ([]Session, error)

    SaveToken(sessionID string, entry TokenEntry) error
    LoadToken(sessionID string) (TokenEntry, bool, error)  // bool=是否存在
}

type Session struct {
    ID       string
    BotID    string  // ilink_bot_id，登录时获取
    UserID   string  // ilink_user_id，登录时获取
    BotToken string
    BaseURL  string
    Cursor   string
}

type TokenEntry struct {
    Token     string
    UpdatedAt time.Time
}
```

### 6.2 MemoryStorage

```go
func NewMemoryStorage() Storage
```

- `sync.RWMutex` + `map`
- 进程退出数据丢失
- 适合测试和一次性使用

### 6.3 SQLiteStorage

```go
func NewSQLiteStorage(dsn string) (Storage, error)
```

- 依赖 `modernc.org/sqlite`（纯 Go，无 CGO）
- 表结构：

```sql
CREATE TABLE sessions (
    id        TEXT PRIMARY KEY,
    bot_id    TEXT NOT NULL DEFAULT '',
    user_id   TEXT NOT NULL DEFAULT '',
    bot_token TEXT NOT NULL,
    base_url  TEXT NOT NULL,
    cursor    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE context_tokens (
    session_id TEXT PRIMARY KEY,
    token      TEXT NOT NULL,
    updated_at DATETIME NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);
```

### 6.4 Storage 调用时机

```
Start()        → LoadSession + LoadToken
登录成功        → SaveSession
每次轮询新消息  → SaveSession (cursor) + SaveToken (contextToken)
Stop()         → SaveSession (最后保存 cursor)
```

## 7. HTTP 客户端与认证

### 7.1 ilinkClient（不导出）

```go
type ilinkClient struct {
    baseURL    string
    botToken   string
    httpClient *http.Client
}
```

### 7.2 认证头

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

### 7.3 API 方法

```go
func (c *ilinkClient) getBotQRCode() (*QRCode, error)
func (c *ilinkClient) getQRCodeStatus(qrID string) (*LoginResult, error)
func (c *ilinkClient) getUpdates(ctx context.Context, cursor string) (*UpdatesResult, error)
func (c *ilinkClient) sendMessage(msg sendRequest) error
func (c *ilinkClient) getUploadURL(req uploadURLRequest) (*UploadURLResult, error)
func (c *ilinkClient) uploadToCDN(url string, data []byte) (downloadParam string, err error) // 重试 3 次，返回 x-encrypted-param

// 可选能力（v1 包含）
func (c *ilinkClient) getConfig(userID string) (*ConfigResult, error)   // 获取 typing_ticket
func (c *ilinkClient) sendTyping(userID string, ticket string) error    // 发送"正在输入"状态
```

### 7.4 统一响应处理

```go
func (c *ilinkClient) do(ctx context.Context, method, path string, body, result any) error
```

内部：序列化 → 构建请求 + 认证头 → 发送 → 检查 HTTP status → 解析 JSON → 检查 `ret/errcode` → 返回 `*APIError` 或 nil。

### 7.5 QRCode

```go
type QRCode struct {
    ID   string
    URL  string
    Data []byte
}
```

### 7.6 APIError

```go
type APIError struct {
    HTTPStatus int
    Ret        int
    ErrCode    int
    ErrMsg     string
}

func (e *APIError) Error() string
```

## 8. 加解密

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

## 9. 错误类型汇总

| 错误 | 说明 |
|------|------|
| `*APIError` | iLink API 返回的业务错误 |
| `*BotError` | 带分类的运行时错误（通过 error channel） |
| `ErrNoContextToken` | 用户尚未给 bot 发过消息 |
| `ErrContextTokenExpired` | contextToken 超过 24 小时过期 |

## 10. 文件结构

```
github.com/fuzhong-jiye/wechat-bot-go/
├── bot.go              // Bot, NewBot, Start, Stop, Reconnect, Errors
├── option.go           // Option, With* 函数
├── handler.go          // handler 注册与分发逻辑
├── context.go          // Context, Reply*, Sender, Bot
├── message.go          // Message, TextMessage, ImageMessage, etc.
├── client.go           // ilinkClient, do(), 认证头, API 方法
├── crypto.go           // AES-128-ECB 加解密
├── storage.go          // Storage 接口, Session, TokenEntry
├── storage_memory.go   // MemoryStorage 实现
├── storage_sqlite.go   // SQLiteStorage 实现
├── errors.go           // BotError, APIError, sentinel errors
└── example_test.go     // 可运行的 Example 函数
```

## 11. 完整用户示例

### 11.1 基础用法 — 一个 Bot 对应一个用户

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

    // 在 handler 中回复
    bot.OnText(func(ctx wechat.Context, msg wechat.TextMessage) {
        log.Printf("收到文本: %s", msg.Text)
        ctx.Reply("你说了: " + msg.Text)
    })

    bot.OnImage(func(ctx wechat.Context, msg wechat.ImageMessage) {
        reader, err := msg.Download()
        if err != nil {
            log.Println("下载失败:", err)
            return
        }
        defer reader.Close()
        ctx.Reply("图片已收到")
    })

    bot.OnMessage(func(ctx wechat.Context, msg wechat.Message) {
        log.Println("收到未处理消息类型")
    })

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
    defer cancel()

    go func() {
        if err := bot.Start(ctx); err != nil {
            log.Fatal("启动失败:", err)
        }
    }()

    // 主动发送 — 不需要指定 userID，Bot 只对应一个用户
    go func() {
        ticker := time.NewTicker(60 * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                if err := bot.Send("这是一条定时主动消息"); err != nil {
                    log.Println("主动发送失败:", err)
                }
            case <-ctx.Done():
                return
            }
        }
    }()

    // 错误处理
    for {
        select {
        case err := <-bot.Errors():
            log.Printf("错误 [%s]: %v", err.Type, err.Err)
            if err.Type == wechat.ErrPoll {
                bot.Reconnect()
            }
        case <-ctx.Done():
            bot.Stop()
            return
        }
    }
}
```

### 11.2 多用户场景 — 多个 Bot 共享 Storage

```go
store, _ := wechat.NewSQLiteStorage("bots.db")

// 每个 Bot 实例对应一个用户对话
userIDs := []string{"user-alice", "user-bob"}
for _, id := range userIDs {
    bot := wechat.NewBot(
        wechat.WithStorage(store),
        wechat.WithSessionID(id),
        wechat.WithQRHandler(func(qr wechat.QRCode) {
            fmt.Printf("[%s] 请扫码: %s\n", id, qr.URL)
        }),
    )
    bot.OnText(func(ctx wechat.Context, msg wechat.TextMessage) {
        ctx.Reply("收到: " + msg.Text)
    })
    go bot.Start(ctx)
}
```
