package wechat

import (
	"errors"
	"fmt"
)

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

func (e *APIError) Error() string {
	return fmt.Sprintf("wechat: API error (http=%d ret=%d errcode=%d): %s",
		e.HTTPStatus, e.Ret, e.ErrCode, e.ErrMsg)
}
