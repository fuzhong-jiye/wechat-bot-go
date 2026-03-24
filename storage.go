package wechat

import (
	"sync"
	"time"
)

// Storage persists bot session state across restarts.
type Storage interface {
	Save(session Session) error
	Load(sessionID string) (Session, bool, error)
}

// Session holds all state for a single bot instance.
type Session struct {
	ID             string
	BotToken       string
	BaseURL        string
	Cursor         string
	ContextToken   string
	TokenUpdatedAt time.Time
	PeerUserID     string
}

type memoryStorage struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

// NewMemoryStorage returns a new in-memory Storage.
func NewMemoryStorage() Storage {
	return &memoryStorage{sessions: make(map[string]Session)}
}

func (m *memoryStorage) Save(session Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
	return nil
}

func (m *memoryStorage) Load(sessionID string) (Session, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[sessionID]
	return s, ok, nil
}
