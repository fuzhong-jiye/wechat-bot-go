package wechat

import (
	"path/filepath"
	"testing"
)

func TestSQLiteStorage(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "test.db")
	s, err := NewSQLiteStorage(dsn)
	if err != nil {
		t.Fatal(err)
	}
	testStorage(t, s)
}

func TestSQLiteStoragePersistence(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "persist.db")

	s1, err := NewSQLiteStorage(dsn)
	if err != nil {
		t.Fatal(err)
	}
	s1.Save(Session{ID: "s1", BotToken: "tok"})

	s2, err := NewSQLiteStorage(dsn)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := s2.Load("s1")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected found=true after reopening DB")
	}
	if got.BotToken != "tok" {
		t.Errorf("BotToken = %q, want %q", got.BotToken, "tok")
	}
}
