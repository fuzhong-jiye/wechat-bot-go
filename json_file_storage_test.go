package wechat

import (
	"path/filepath"
	"testing"
)

func TestJSONFileStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.json")
	s, err := NewJSONFileStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, s)
}

func TestJSONFileStoragePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.json")

	s1, err := NewJSONFileStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Save(Session{ID: "s1", BotToken: "tok"}); err != nil {
		t.Fatal(err)
	}

	s2, err := NewJSONFileStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := s2.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected found=true after reopening file")
	}
	if got.BotToken != "tok" {
		t.Errorf("BotToken = %q, want %q", got.BotToken, "tok")
	}
}
