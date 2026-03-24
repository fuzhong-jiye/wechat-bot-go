package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type capturedLog struct {
	level  LogLevel
	msg    string
	fields map[string]any
}

type captureLogger struct {
	mu   sync.Mutex
	logs []capturedLog
}

func (l *captureLogger) Log(_ context.Context, level LogLevel, msg string, fields ...Field) {
	entry := capturedLog{
		level:  level,
		msg:    msg,
		fields: make(map[string]any, len(fields)),
	}
	for _, field := range fields {
		entry.fields[field.Key] = field.Value
	}
	l.mu.Lock()
	l.logs = append(l.logs, entry)
	l.mu.Unlock()
}

func (l *captureLogger) containsMsg(msg string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, entry := range l.logs {
		if entry.msg == msg {
			return true
		}
	}
	return false
}

func (l *captureLogger) containsValue(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, entry := range l.logs {
		if strings.Contains(entry.msg, substr) {
			return true
		}
		for _, value := range entry.fields {
			if strings.Contains(fmt.Sprint(value), substr) {
				return true
			}
		}
	}
	return false
}

func (l *captureLogger) fieldFor(msg, key string) any {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, entry := range l.logs {
		if entry.msg == msg {
			return entry.fields[key]
		}
	}
	return nil
}

func TestBotLoggerCapturesEventsAndRedactsSecrets(t *testing.T) {
	var sentReq wireSendRequest
	logger := &captureLogger{}

	store := NewMemoryStorage()
	if err := store.Save(Session{ID: "test", BotToken: "bot-token-secret", BaseURL: "https://mock.ilink"}); err != nil {
		t.Fatal(err)
	}

	bot := NewBot(
		WithStorage(store),
		WithSessionID("test"),
		WithLogger(logger),
		WithLogLevel(LogInfo),
		withClientFactory(newBotTestFactory(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/ilink/bot/getupdates":
				return newTestResponse(200, `{"ret":0,"errcode":0,"errmsg":"","msgs":[{"message_id":1,"from_user_id":"user-1234567890","create_time_ms":1710000000000,"message_type":1,"item_list":[{"type":1,"text_item":{"text":"hi"}}],"context_token":"ctx-secret-123"}],"get_updates_buf":"cursor-1"}`), nil
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

	if !logger.containsMsg("bot.start") {
		t.Fatal("expected bot.start log")
	}
	if !logger.containsMsg("message.received") {
		t.Fatal("expected message.received log")
	}
	if !logger.containsMsg("message.sent") {
		t.Fatal("expected message.sent log")
	}
	if got := logger.fieldFor("message.received", "peer_user_id"); got != "user-1...7890" {
		t.Fatalf("peer_user_id = %v, want %q", got, "user-1...7890")
	}
	if logger.containsValue("ctx-secret-123") {
		t.Fatal("context token leaked into logs")
	}
	if logger.containsValue("bot-token-secret") {
		t.Fatal("bot token leaked into logs")
	}
	if sentReq.Msg.ContextToken != "ctx-secret-123" {
		t.Fatalf("send context token = %q, want original secret", sentReq.Msg.ContextToken)
	}
}

func TestLoggerLevelFiltering(t *testing.T) {
	logger := &captureLogger{}
	bot := NewBot(
		WithLogger(logger),
		WithLogLevel(LogError),
	)
	bot.client = newIlinkClient("https://mock.ilink", "bot-token")

	if err := bot.Send("hello"); err != ErrNoContextToken {
		t.Fatalf("Send() error = %v, want %v", err, ErrNoContextToken)
	}
	if logger.containsMsg("context_token.missing") {
		t.Fatal("warn log should be filtered at error level")
	}
}

func TestClientLoggerSanitizesRequestErrors(t *testing.T) {
	logger := &captureLogger{}
	c := newIlinkClient("https://example.invalid", "bot-token-secret")
	c.logger = logger
	c.logLevel = LogError
	c.sessionID = "test"
	c.httpClient = newTestClient(func(r *http.Request) (*http.Response, error) {
		return newTestResponse(500, `{"errmsg":"bot_token=bot-token-secret&context_token=ctx-secret-1"}`), nil
	})

	err := c.do(context.Background(), http.MethodPost, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !logger.containsMsg("request.failed") {
		t.Fatal("expected request.failed log")
	}
	if logger.containsValue("bot-token-secret") {
		t.Fatal("bot token leaked into request logs")
	}
	if logger.containsValue("ctx-secret-1") {
		t.Fatal("context token leaked into request logs")
	}
}
