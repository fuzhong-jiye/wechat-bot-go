package wechat

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newTestClient(rt roundTripFunc) *http.Client {
	return &http.Client{Transport: rt}
}

func TestBuildCDNDownloadURL(t *testing.T) {
	got := buildCDNDownloadURL("enc-param-123", "https://cdn.example.com/c2c")
	want := "https://cdn.example.com/c2c/download?encrypted_query_param=enc-param-123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildCDNDownloadURLEncoding(t *testing.T) {
	got := buildCDNDownloadURL("a=b&c=d", "https://cdn.example.com/c2c")
	want := "https://cdn.example.com/c2c/download?encrypted_query_param=a%3Db%26c%3Dd"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildCDNUploadURL(t *testing.T) {
	got := buildCDNUploadURL("https://cdn.example.com/c2c", "upload-param", "filekey123")
	want := "https://cdn.example.com/c2c/upload?encrypted_query_param=upload-param&filekey=filekey123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClientDoAuthHeaders(t *testing.T) {
	var gotHeaders http.Header
	c := newIlinkClient("https://example.invalid", "test-token")
	c.httpClient = newTestClient(func(r *http.Request) (*http.Response, error) {
		gotHeaders = r.Header.Clone()
		return newTestResponse(200, `{"ret":0}`), nil
	})

	err := c.do(context.Background(), http.MethodPost, "/test", map[string]string{"key": "val"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := gotHeaders.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := gotHeaders.Get("AuthorizationType"); got != "ilink_bot_token" {
		t.Errorf("AuthorizationType = %q", got)
	}
	if got := gotHeaders.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := gotHeaders.Get("X-WECHAT-UIN"); got == "" {
		t.Error("X-WECHAT-UIN is empty")
	}
}

func TestClientDoNoAuthWhenEmpty(t *testing.T) {
	var gotHeaders http.Header
	c := newIlinkClient("https://example.invalid", "")
	c.httpClient = newTestClient(func(r *http.Request) (*http.Response, error) {
		gotHeaders = r.Header.Clone()
		return newTestResponse(200, `{"ret":0}`), nil
	})

	err := c.do(context.Background(), http.MethodGet, "/test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := gotHeaders.Get("Authorization"); got != "" {
		t.Errorf("Authorization should be empty, got %q", got)
	}
}

func TestClientDoAPIError(t *testing.T) {
	c := newIlinkClient("https://example.invalid", "token")
	c.httpClient = newTestClient(func(r *http.Request) (*http.Response, error) {
		return newTestResponse(200, `{"ret":-14,"errcode":-14,"errmsg":"session expired"}`), nil
	})

	err := c.do(context.Background(), http.MethodPost, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.ErrCode != -14 {
		t.Errorf("ErrCode = %d, want -14", apiErr.ErrCode)
	}
}

func TestClientDoHTTPError(t *testing.T) {
	c := newIlinkClient("https://example.invalid", "token")
	c.httpClient = newTestClient(func(r *http.Request) (*http.Response, error) {
		return newTestResponse(500, "internal error"), nil
	})

	err := c.do(context.Background(), http.MethodPost, "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.HTTPStatus != 500 {
		t.Errorf("HTTPStatus = %d, want 500", apiErr.HTTPStatus)
	}
}

func TestClientDoResultParsing(t *testing.T) {
	c := newIlinkClient("https://example.invalid", "token")
	c.httpClient = newTestClient(func(r *http.Request) (*http.Response, error) {
		return newTestResponse(200, `{"ret":0,"data":"hello"}`), nil
	})

	var result struct {
		Data string `json:"data"`
	}
	err := c.do(context.Background(), http.MethodGet, "/test", nil, &result)
	if err != nil {
		t.Fatal(err)
	}
	if result.Data != "hello" {
		t.Errorf("Data = %q, want %q", result.Data, "hello")
	}
}

func TestGetQRCodeStatusSetsClientVersionHeader(t *testing.T) {
	var gotHeaders http.Header
	c := newIlinkClient("https://example.invalid", "")
	c.httpClient = newTestClient(func(r *http.Request) (*http.Response, error) {
		gotHeaders = r.Header.Clone()
		return newTestResponse(200, `{"status":"wait"}`), nil
	})

	_, err := c.getQRCodeStatus(context.Background(), "qr-123")
	if err != nil {
		t.Fatal(err)
	}
	if got := gotHeaders.Get("iLink-App-ClientVersion"); got != "1" {
		t.Errorf("iLink-App-ClientVersion = %q, want 1", got)
	}
}
