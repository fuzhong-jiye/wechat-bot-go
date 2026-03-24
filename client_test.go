package wechat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		w.Write([]byte(`{"ret":0}`))
	}))
	defer srv.Close()

	c := newIlinkClient(srv.URL, "test-token")
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header
		w.Write([]byte(`{"ret":0}`))
	}))
	defer srv.Close()

	c := newIlinkClient(srv.URL, "")
	err := c.do(context.Background(), http.MethodGet, "/test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := gotHeaders.Get("Authorization"); got != "" {
		t.Errorf("Authorization should be empty, got %q", got)
	}
}

func TestClientDoAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ret":-14,"errcode":-14,"errmsg":"session expired"}`))
	}))
	defer srv.Close()

	c := newIlinkClient(srv.URL, "token")
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	c := newIlinkClient(srv.URL, "token")
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ret":0,"data":"hello"}`))
	}))
	defer srv.Close()

	c := newIlinkClient(srv.URL, "token")
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
