package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"

	wechat "github.com/fuzhong-jiye/wechat-bot-go"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	store, err := wechat.NewJSONFileStorage("bot.json")
	if err != nil {
		logger.Error("init storage failed", "error", err)
		os.Exit(1)
	}

	bot := wechat.NewBot(
		wechat.WithStorage(store),
		wechat.WithSessionID("my-bot"),
		wechat.WithLogger(wechat.NewSlogLogger(logger)),
		wechat.WithLogLevel(wechat.LogInfo),
		wechat.WithQRHandler(func(qr wechat.QRCode) {
			logger.Info("scan QR code", "qr_url", qr.URL)
		}),
	)

	bot.OnMessage(func(msg wechat.Message) {
		if text := msg.Text(); text != "" {
			if err := bot.Send("You said: " + text); err != nil {
				logger.Error("send failed", "error", err)
			}
		}
	})

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := bot.Start(ctx); err != nil && ctx.Err() == nil {
		logger.Error("bot stopped with error", "error", err)
		os.Exit(1)
	}
}
