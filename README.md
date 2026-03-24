# wechat-bot-go

`wechat-bot-go` 是一个基于 Go 的微信 Bot SDK，封装了登录、二维码扫码、消息轮询、上下文 token 管理以及文本/媒体发送能力。

当前项目定位为高层 Bot 框架：

- 一个 `Bot` 实例对应一个用户会话
- 支持内存存储和本地文件持久化
- 提供文本、图片、语音（测试不可用）、文件、视频（测试不可用）发送接口

## 安装

要求：

- Go 1.26+

安装依赖：

```bash
go get github.com/fuzhong-jiye/wechat-bot-go@v1.0.2
```

如果你想运行仓库内示例：

```bash
git clone https://github.com/fuzhong-jiye/wechat-bot-go.git
cd wechat-bot-go
go run ./cmd/simple
```

完整回声示例：

```bash
go run ./cmd/example
```

## 快速开始

最简单启动一个 Bot：

```go
package main

import (
	"context"
	"fmt"

	wechat "github.com/fuzhong-jiye/wechat-bot-go"
)

func main() {
	bot := wechat.NewBot(
		wechat.WithQRHandler(func(qr wechat.QRCode) {
			fmt.Println("请扫码登录:", qr.URL)
		}),
	)

	bot.OnMessage(func(msg wechat.Message) {
		if text := msg.Text(); text != "" {
			if err := bot.Send("你刚才说的是: " + text); err != nil {
				fmt.Printf("发送失败: %v\n", err)
			}
		}
	})

	if err := bot.Start(context.Background()); err != nil {
		panic(err)
	}
}
```

如果你想在重启后保留 session、接入日志并优雅退出，可以参考完整示例：

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	wechat "github.com/fuzhong-jiye/wechat-bot-go"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	store, err := wechat.NewJSONFileStorage("bot.json")
	if err != nil {
		panic(err)
	}

	bot := wechat.NewBot(
		wechat.WithStorage(store),
		wechat.WithSessionID("demo-bot"),
		wechat.WithLogger(wechat.NewSlogLogger(logger)),
		wechat.WithLogLevel(wechat.LogInfo),
		wechat.WithQRHandler(func(qr wechat.QRCode) {
			logger.Info("请扫码登录", "qr_url", qr.URL)
		}),
	)

	bot.OnMessage(func(msg wechat.Message) {
		if text := msg.Text(); text != "" {
			_ = bot.Send("你刚才说的是: " + text)
		}
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := bot.Start(ctx); err != nil && ctx.Err() == nil {
		panic(err)
	}
}
```

## 用法说明

### 1. 创建 Bot

使用 `wechat.NewBot(...)` 创建实例，常用选项包括：

- `WithStorage(storage)`：设置会话存储，默认是内存存储
- `WithSessionID(id)`：设置会话 ID，默认是 `default`
- `WithQRHandler(fn)`：首次登录时必须提供，用于展示二维码
- `WithLogger(logger)`：接入结构化日志
- `WithLogLevel(level)`：设置日志级别

### 2. Storage 接口

`Storage` 用来持久化 Bot 的会话状态，便于进程重启后复用登录态与消息上下文。

接口定义如下：

```go
type Storage interface {
	Save(session Session) error
	Load(sessionID string) (Session, bool, error)
}
```

其中 `Session` 包含以下字段：

```go
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

字段说明：

- `ID`：会话唯一标识，对应 `WithSessionID`
- `BotToken`：登录成功后的机器人令牌
- `BaseURL`：当前会话对应的服务地址
- `Cursor`：消息轮询游标
- `ContextToken`：发送消息时需要的上下文 token
- `TokenUpdatedAt`：上下文 token 更新时间
- `PeerUserID`：最近一次和 Bot 建立上下文的用户 ID

实现自定义存储时建议：

- `Save` 按 `Session.ID` 做幂等更新
- `Load` 在数据不存在时返回 `found=false, err=nil`
- 对 `time.Time` 字段使用数据库原生时间类型保存
- 如果你的存储会被多个 goroutine 访问，保证并发安全

下面是一个 `gorm + mysql` 的示例实现：

```go
package main

import (
	"errors"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	wechat "github.com/fuzhong-jiye/wechat-bot-go"
)

type SessionModel struct {
	ID             string    `gorm:"primaryKey;size:128"`
	BotToken       string    `gorm:"type:text"`
	BaseURL        string    `gorm:"type:text"`
	Cursor         string    `gorm:"type:text"`
	ContextToken   string    `gorm:"type:text"`
	TokenUpdatedAt time.Time
	PeerUserID     string    `gorm:"type:varchar(128)"`
	UpdatedAt      time.Time
	CreatedAt      time.Time
}

type GormStorage struct {
	db *gorm.DB
}

func NewGormStorage(dsn string) (*GormStorage, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&SessionModel{}); err != nil {
		return nil, err
	}
	return &GormStorage{db: db}, nil
}

func (s *GormStorage) Save(session wechat.Session) error {
	model := SessionModel{
		ID:             session.ID,
		BotToken:       session.BotToken,
		BaseURL:        session.BaseURL,
		Cursor:         session.Cursor,
		ContextToken:   session.ContextToken,
		TokenUpdatedAt: session.TokenUpdatedAt,
		PeerUserID:     session.PeerUserID,
	}

	return s.db.Save(&model).Error
}

func (s *GormStorage) Load(sessionID string) (wechat.Session, bool, error) {
	var model SessionModel
	err := s.db.First(&model, "id = ?", sessionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return wechat.Session{}, false, nil
	}
	if err != nil {
		return wechat.Session{}, false, err
	}

	return wechat.Session{
		ID:             model.ID,
		BotToken:       model.BotToken,
		BaseURL:        model.BaseURL,
		Cursor:         model.Cursor,
		ContextToken:   model.ContextToken,
		TokenUpdatedAt: model.TokenUpdatedAt,
		PeerUserID:     model.PeerUserID,
	}, true, nil
}
```

使用方式：

```go
store, err := NewGormStorage("user:password@tcp(127.0.0.1:3306)/wechat_bot?charset=utf8mb4&parseTime=True&loc=Local")
if err != nil {
	panic(err)
}

bot := wechat.NewBot(
	wechat.WithStorage(store),
	wechat.WithSessionID("demo-bot"),
)
```

### 3. 启动与停止

调用 `bot.Start(ctx)` 后，SDK 会自动执行：

1. 加载 session
2. 如果没有登录态，则拉取二维码并等待扫码确认
3. 开始轮询消息
4. 自动维护 `cursor`、`contextToken` 和会话状态

调用 `bot.Stop()` 或取消 `ctx` 可优雅退出。

### 4. 接收消息

通过 `bot.OnMessage(func(msg wechat.Message))` 注册消息处理函数。

- `msg.Text()`：拼接所有文本内容
- `msg.Items`：访问原始消息项
- 图片/语音/文件/视频消息支持 `Download()` 下载内容

示例：

```go
bot.OnMessage(func(msg wechat.Message) {
	for _, item := range msg.Items {
		switch item.Type {
		case wechat.ItemImage:
			if item.Image != nil {
				rc, err := item.Image.Download()
				if err == nil {
					defer rc.Close()
				}
			}
		}
	}
})
```

### 5. 发送消息

支持以下接口：

```go
bot.Send("hello")
bot.SendImage(reader, "photo.jpg")
bot.SendVoice(reader, "voice.mp3")
bot.SendFile(reader, "report.pdf")
bot.SendVideo(reader, "clip.mp4")
```

注意：

- 发送消息前，用户必须先给 Bot 发过消息，否则会返回 `ErrNoContextToken`
- 如果用户超过 24 小时未发消息，可能返回 `ErrContextTokenExpired`
- 会话失效时，`Start` 可能返回 `ErrSessionExpired`
