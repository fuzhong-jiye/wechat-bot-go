package wechat

import (
	"testing"
	"time"
)

func testStorage(t *testing.T, s Storage) {
	t.Helper()

	// Load missing key
	_, found, err := s.Load("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Error("expected found=false for missing key")
	}

	// Save and load
	now := time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	session := Session{
		ID:             "test-session",
		BotToken:       "token-123",
		BaseURL:        "https://example.com",
		Cursor:         "cursor-abc",
		ContextToken:   "ctx-tok-456",
		TokenUpdatedAt: now,
		PeerUserID:     "user-789",
	}
	if err := s.Save(session); err != nil {
		t.Fatal(err)
	}

	got, found, err := s.Load("test-session")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if got.ID != session.ID ||
		got.BotToken != session.BotToken ||
		got.BaseURL != session.BaseURL ||
		got.Cursor != session.Cursor ||
		got.ContextToken != session.ContextToken ||
		!got.TokenUpdatedAt.Equal(session.TokenUpdatedAt) ||
		got.PeerUserID != session.PeerUserID {
		t.Errorf("got %+v, want %+v", got, session)
	}

	// Update and re-load
	session.Cursor = "cursor-def"
	if err := s.Save(session); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Load("test-session")
	if got.Cursor != "cursor-def" {
		t.Errorf("Cursor = %q after update, want %q", got.Cursor, "cursor-def")
	}
}

func TestMemoryStorage(t *testing.T) {
	testStorage(t, NewMemoryStorage())
}

func TestMemoryStorageIsolation(t *testing.T) {
	s := NewMemoryStorage()
	session := Session{ID: "iso", BotToken: "tok"}
	s.Save(session)

	got, _, _ := s.Load("iso")
	got.BotToken = "mutated"

	got2, _, _ := s.Load("iso")
	if got2.BotToken != "tok" {
		t.Error("mutation leaked through — storage should copy on load")
	}
}
