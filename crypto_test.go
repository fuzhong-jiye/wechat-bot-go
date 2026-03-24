package wechat

import (
	"bytes"
	"testing"
)

func TestAesECBPaddedSize(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, 16},
		{1, 16},
		{15, 16},
		{16, 32},
		{17, 32},
		{31, 32},
		{32, 48},
	}
	for _, tt := range tests {
		got := aesECBPaddedSize(tt.input)
		if got != tt.want {
			t.Errorf("aesECBPaddedSize(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestAesECBRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef")

	tests := [][]byte{
		[]byte("hello"),
		[]byte("exactly16bytes!!"),
		bytes.Repeat([]byte("x"), 100),
		{},
	}
	for _, plaintext := range tests {
		ciphertext, err := aesECBEncrypt(plaintext, key)
		if err != nil {
			t.Fatalf("encrypt(%q): %v", plaintext, err)
		}
		if len(ciphertext) != aesECBPaddedSize(len(plaintext)) {
			t.Errorf("ciphertext len = %d, want %d", len(ciphertext), aesECBPaddedSize(len(plaintext)))
		}
		got, err := aesECBDecrypt(ciphertext, key)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Errorf("round-trip failed: got %q, want %q", got, plaintext)
		}
	}
}

func TestAesECBInvalidKeyLength(t *testing.T) {
	_, err := aesECBEncrypt([]byte("data"), []byte("short"))
	if err == nil {
		t.Error("expected error for short key")
	}
	_, err = aesECBDecrypt([]byte("0123456789abcdef"), []byte("short"))
	if err == nil {
		t.Error("expected error for short key on decrypt")
	}
}
