package wechat

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type jsonFileStorage struct {
	mu   sync.Mutex
	path string
}

// NewJSONFileStorage creates a Storage backed by a JSON file at the given path.
func NewJSONFileStorage(path string) (Storage, error) {
	if path == "" {
		return nil, errors.New("json storage path is empty")
	}
	return &jsonFileStorage{path: path}, nil
}

func (s *jsonFileStorage) Save(session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessions, err := s.readAll()
	if err != nil {
		return err
	}
	sessions[session.ID] = session
	return s.writeAll(sessions)
}

func (s *jsonFileStorage) Load(sessionID string) (Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessions, err := s.readAll()
	if err != nil {
		return Session{}, false, err
	}
	session, ok := sessions[sessionID]
	return session, ok, nil
}

func (s *jsonFileStorage) readAll() (map[string]Session, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]Session), nil
		}
		return nil, fmt.Errorf("read json storage: %w", err)
	}
	if len(data) == 0 {
		return make(map[string]Session), nil
	}

	var sessions map[string]Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, fmt.Errorf("decode json storage: %w", err)
	}
	if sessions == nil {
		sessions = make(map[string]Session)
	}
	return sessions, nil
}

func (s *jsonFileStorage) writeAll(sessions map[string]Session) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create json storage directory: %w", err)
	}

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json storage: %w", err)
	}
	data = append(data, '\n')

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write json storage temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace json storage file: %w", err)
	}
	return nil
}
