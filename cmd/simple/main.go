package main

import (
	"context"
	"fmt"

	wechat "github.com/fuzhong-jiye/wechat-bot-go"
)

func main() {
	bot := wechat.NewBot(
		wechat.WithQRHandler(func(qr wechat.QRCode) {
			fmt.Println("请扫码登录: ", qr.URL)
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
