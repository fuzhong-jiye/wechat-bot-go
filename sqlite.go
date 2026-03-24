package wechat

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type sqliteStorage struct {
	db *sql.DB
}

// NewSQLiteStorage creates a Storage backed by SQLite at the given file path.
func NewSQLiteStorage(dsn string) (Storage, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id               TEXT PRIMARY KEY,
		bot_token        TEXT NOT NULL DEFAULT '',
		base_url         TEXT NOT NULL DEFAULT '',
		cursor           TEXT NOT NULL DEFAULT '',
		context_token    TEXT NOT NULL DEFAULT '',
		token_updated_at TEXT NOT NULL DEFAULT '',
		peer_user_id     TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create table: %w", err)
	}
	return &sqliteStorage{db: db}, nil
}

func (s *sqliteStorage) Save(session Session) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO sessions
		(id, bot_token, base_url, cursor, context_token, token_updated_at, peer_user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.BotToken,
		session.BaseURL,
		session.Cursor,
		session.ContextToken,
		session.TokenUpdatedAt.UTC().Format(time.RFC3339),
		session.PeerUserID,
	)
	return err
}

func (s *sqliteStorage) Load(sessionID string) (Session, bool, error) {
	var session Session
	var tokenUpdatedAt string
	err := s.db.QueryRow(`SELECT id, bot_token, base_url, cursor, context_token, token_updated_at, peer_user_id
		FROM sessions WHERE id = ?`, sessionID).Scan(
		&session.ID,
		&session.BotToken,
		&session.BaseURL,
		&session.Cursor,
		&session.ContextToken,
		&tokenUpdatedAt,
		&session.PeerUserID,
	)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	if tokenUpdatedAt != "" {
		session.TokenUpdatedAt, _ = time.Parse(time.RFC3339, tokenUpdatedAt)
	}
	return session, true, nil
}
