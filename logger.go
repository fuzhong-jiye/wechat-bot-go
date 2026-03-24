package wechat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// LogLevel controls logger verbosity.
type LogLevel int

const (
	LogDebug LogLevel = iota
	LogInfo
	LogWarn
	LogError
)

// Field is a structured log attribute.
type Field struct {
	Key   string
	Value any
}

// Logger is the SDK logging interface.
type Logger interface {
	Log(ctx context.Context, level LogLevel, msg string, fields ...Field)
}

// NopLogger discards all log events.
type NopLogger struct{}

// Log implements Logger.
func (NopLogger) Log(context.Context, LogLevel, string, ...Field) {}

type slogLogger struct {
	logger *slog.Logger
}

// NewSlogLogger adapts slog.Logger to the SDK Logger interface.
func NewSlogLogger(logger *slog.Logger) Logger {
	if logger == nil {
		return NopLogger{}
	}
	return slogLogger{logger: logger}
}

func (l slogLogger) Log(ctx context.Context, level LogLevel, msg string, fields ...Field) {
	attrs := make([]any, 0, len(fields)*2)
	for _, field := range fields {
		attrs = append(attrs, field.Key, field.Value)
	}
	l.logger.Log(ctx, toSlogLevel(level), msg, attrs...)
}

func toSlogLevel(level LogLevel) slog.Level {
	switch level {
	case LogDebug:
		return slog.LevelDebug
	case LogInfo:
		return slog.LevelInfo
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func shouldLog(threshold, level LogLevel) bool {
	return level >= threshold
}

func appendFields(fields []Field, extra ...Field) []Field {
	out := make([]Field, 0, len(fields)+len(extra))
	out = append(out, fields...)
	out = append(out, extra...)
	return out
}

func maskID(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 10 {
		return value
	}
	return value[:6] + "..." + value[len(value)-4:]
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.ErrMsg == "" {
			return fmt.Sprintf("http=%d ret=%d errcode=%d", apiErr.HTTPStatus, apiErr.Ret, apiErr.ErrCode)
		}
		return fmt.Sprintf("http=%d ret=%d errcode=%d msg=%s",
			apiErr.HTTPStatus, apiErr.Ret, apiErr.ErrCode, sanitizeString(apiErr.ErrMsg))
	}

	return sanitizeString(err.Error())
}

func sanitizeString(s string) string {
	for _, pattern := range sensitivePatterns {
		s = pattern.ReplaceAllString(s, "$1[redacted]$3")
	}
	s = bearerPattern.ReplaceAllString(s, "$1[redacted]")
	replacer := strings.NewReplacer(
		"bot_token", "[redacted]",
		"context_token", "[redacted]",
		"qrcode", "[redacted]",
		"qrcode_img_content", "[redacted]",
		"upload_param", "[redacted]",
		"encrypted_query_param", "[redacted]",
		"aes_key", "[redacted]",
		"Authorization", "[redacted]",
		"Bearer ", "[redacted] ",
	)
	return replacer.Replace(s)
}

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(bot_token["=: ]+)([^"&,\s}]+)("?)`),
	regexp.MustCompile(`(?i)(context_token["=: ]+)([^"&,\s}]+)("?)`),
	regexp.MustCompile(`(?i)(qrcode_img_content["=: ]+)([^"&,\s}]+)("?)`),
	regexp.MustCompile(`(?i)(qrcode["=: ]+)([^"&,\s}]+)("?)`),
	regexp.MustCompile(`(?i)(upload_param["=: ]+)([^"&,\s}]+)("?)`),
	regexp.MustCompile(`(?i)(encrypted_query_param["=: ]+)([^"&,\s}]+)("?)`),
	regexp.MustCompile(`(?i)(aes_key["=: ]+)([^"&,\s}]+)("?)`),
	regexp.MustCompile(`(?i)(Authorization["=: ]+)([^"&,\s}]+)("?)`),
}

var bearerPattern = regexp.MustCompile(`(?i)(Bearer\s+)([^\s]+)`)
