package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func newBotTestFactory(rt roundTripFunc) func(baseURL, botToken string) *ilinkClient {
	httpClient := newTestClient(rt)
	return func(baseURL, botToken string) *ilinkClient {
		return &ilinkClient{
			baseURL:    baseURL,
			botToken:   botToken,
			httpClient: httpClient,
		}
	}
}

func TestBotLoginAndPoll(t *testing.T) {
	var gotQR QRCode
	var updateCalls int32

	store := NewMemoryStorage()
	bot := NewBot(
		WithStorage(store),
		WithSessionID("test"),
		WithQRHandler(func(qr QRCode) { gotQR = qr }),
		withClientFactory(newBotTestFactory(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/ilink/bot/get_bot_qrcode":
				return newTestResponse(200, `{"qrcode":"qr://login","qrcode_img_content":"img-b64"}`), nil
			case "/ilink/bot/get_qrcode_status":
				return newTestResponse(200, `{"status":"confirmed","bot_token":"bot-token","baseurl":"https://mock.ilink"}`), nil
			case "/ilink/bot/getupdates":
				if atomic.AddInt32(&updateCalls, 1) == 1 {
					return newTestResponse(200, `{"ret":0,"errcode":0,"errmsg":"","msgs":[{"message_id":1,"from_user_id":"user-1","create_time_ms":1710000000000,"message_type":1,"item_list":[{"type":1,"text_item":{"text":"hello"}}],"context_token":"ctx-1"}],"get_updates_buf":"cursor-1"}`), nil
				}
				return newTestResponse(200, `{"ret":0,"errcode":0,"errmsg":"","msgs":[],"get_updates_buf":"cursor-1"}`), nil
			default:
				return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		})),
		withQRPollInterval(time.Millisecond),
		withPollRetryDelay(time.Millisecond),
	)

	msgCh := make(chan Message, 1)
	bot.OnMessage(func(msg Message) {
		msgCh <- msg
		bot.Stop()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := bot.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if gotQR.URL != "img-b64" {
		t.Errorf("QRCode.URL = %q, want %q", gotQR.URL, "img-b64")
	}

	select {
	case msg := <-msgCh:
		if msg.Text() != "hello" {
			t.Errorf("msg.Text() = %q, want %q", msg.Text(), "hello")
		}
		if msg.FromUserID != "user-1" {
			t.Errorf("msg.FromUserID = %q, want %q", msg.FromUserID, "user-1")
		}
	default:
		t.Fatal("expected a message callback")
	}

	s, found, err := store.Load("test")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected saved session")
	}
	if s.BotToken != "bot-token" {
		t.Errorf("BotToken = %q, want %q", s.BotToken, "bot-token")
	}
	if s.BaseURL != "https://mock.ilink" {
		t.Errorf("BaseURL = %q, want %q", s.BaseURL, "https://mock.ilink")
	}
	if s.Cursor != "cursor-1" {
		t.Errorf("Cursor = %q, want %q", s.Cursor, "cursor-1")
	}
	if s.ContextToken != "ctx-1" {
		t.Errorf("ContextToken = %q, want %q", s.ContextToken, "ctx-1")
	}
	if s.PeerUserID != "user-1" {
		t.Errorf("PeerUserID = %q, want %q", s.PeerUserID, "user-1")
	}
}

func TestBotSessionExpired(t *testing.T) {
	store := NewMemoryStorage()
	if err := store.Save(Session{ID: "test", BotToken: "bot-token", BaseURL: "https://mock.ilink"}); err != nil {
		t.Fatal(err)
	}

	bot := NewBot(
		WithStorage(store),
		WithSessionID("test"),
		withClientFactory(newBotTestFactory(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/ilink/bot/getupdates" {
				return nil, fmt.Errorf("unexpected request: %s", r.URL.Path)
			}
			return newTestResponse(200, `{"ret":-14,"errcode":-14,"errmsg":"session expired"}`), nil
		})),
		withPollRetryDelay(time.Millisecond),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := bot.Start(ctx)
	if err != ErrSessionExpired {
		t.Fatalf("Start() error = %v, want %v", err, ErrSessionExpired)
	}
}

func TestBotSendText(t *testing.T) {
	var updateCalls int32
	var sentReq wireSendRequest

	store := NewMemoryStorage()
	if err := store.Save(Session{ID: "test", BotToken: "bot-token", BaseURL: "https://mock.ilink"}); err != nil {
		t.Fatal(err)
	}

	bot := NewBot(
		WithStorage(store),
		WithSessionID("test"),
		withClientFactory(newBotTestFactory(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/ilink/bot/getupdates":
				if atomic.AddInt32(&updateCalls, 1) == 1 {
					return newTestResponse(200, `{"ret":0,"errcode":0,"errmsg":"","msgs":[{"message_id":1,"from_user_id":"user-1","create_time_ms":1710000000000,"message_type":1,"item_list":[{"type":1,"text_item":{"text":"hi"}}],"context_token":"ctx-1"}],"get_updates_buf":"cursor-1"}`), nil
				}
				return newTestResponse(200, `{"ret":0,"errcode":0,"errmsg":"","msgs":[],"get_updates_buf":"cursor-1"}`), nil
			case "/ilink/bot/sendmessage":
				if err := json.NewDecoder(r.Body).Decode(&sentReq); err != nil {
					return nil, err
				}
				return newTestResponse(200, `{"ret":0,"errcode":0,"errmsg":""}`), nil
			default:
				return nil, fmt.Errorf("unexpected request: %s", r.URL.Path)
			}
		})),
	)

	bot.OnMessage(func(msg Message) {
		if err := bot.Send("echo: " + msg.Text()); err != nil {
			t.Errorf("Send: %v", err)
		}
		bot.Stop()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := bot.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if sentReq.Msg.ToUserID != "user-1" {
		t.Errorf("ToUserID = %q, want %q", sentReq.Msg.ToUserID, "user-1")
	}
	if sentReq.Msg.ContextToken != "ctx-1" {
		t.Errorf("ContextToken = %q, want %q", sentReq.Msg.ContextToken, "ctx-1")
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
	if len(sentReq.Msg.ItemList) != 1 || sentReq.Msg.ItemList[0].TextItem == nil {
		t.Fatal("expected a single text item")
	}
	if sentReq.Msg.ItemList[0].TextItem.Text != "echo: hi" {
		t.Errorf("Text = %q, want %q", sentReq.Msg.ItemList[0].TextItem.Text, "echo: hi")
	}
	if sentReq.Msg.ClientID == "" {
		t.Fatal("expected generated ClientID")
	}
}

func TestBotSendRequiresContextToken(t *testing.T) {
	bot := NewBot()
	bot.client = newIlinkClient("https://mock.ilink", "bot-token")

	if err := bot.Send("hello"); err != ErrNoContextToken {
		t.Fatalf("Send() error = %v, want %v", err, ErrNoContextToken)
	}
}

func TestBotSendRejectsExpiredContextToken(t *testing.T) {
	bot := NewBot()
	bot.client = newIlinkClient("https://mock.ilink", "bot-token")
	bot.session = Session{
		ContextToken:   "ctx-1",
		TokenUpdatedAt: time.Now().Add(-25 * time.Hour),
	}

	if err := bot.Send("hello"); err != ErrContextTokenExpired {
		t.Fatalf("Send() error = %v, want %v", err, ErrContextTokenExpired)
	}
}

func TestBotSkipsGroupAndOutboundMessages(t *testing.T) {
	var received []string

	store := NewMemoryStorage()
	if err := store.Save(Session{ID: "test", BotToken: "bot-token", BaseURL: "https://mock.ilink"}); err != nil {
		t.Fatal(err)
	}

	bot := NewBot(
		WithStorage(store),
		WithSessionID("test"),
		withClientFactory(newBotTestFactory(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/ilink/bot/getupdates":
				return newTestResponse(200, `{"ret":0,"errcode":0,"errmsg":"","msgs":[{"message_id":1,"from_user_id":"group-user","group_id":"group-1","message_type":1,"item_list":[{"type":1,"text_item":{"text":"group"}}]},{"message_id":2,"from_user_id":"user-1","message_type":2,"item_list":[{"type":1,"text_item":{"text":"outbound"}}]},{"message_id":3,"from_user_id":"user-1","create_time_ms":1710000000000,"message_type":1,"item_list":[{"type":1,"text_item":{"text":"direct"}}],"context_token":"ctx-1"}],"get_updates_buf":"cursor-1"}`), nil
			default:
				return nil, fmt.Errorf("unexpected request: %s", r.URL.Path)
			}
		})),
	)

	bot.OnMessage(func(msg Message) {
		received = append(received, msg.Text())
		bot.Stop()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := bot.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if len(received) != 1 {
		t.Fatalf("received %d messages, want 1", len(received))
	}
	if received[0] != "direct" {
		t.Fatalf("received %q, want %q", received[0], "direct")
	}
}
