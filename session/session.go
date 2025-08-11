package session

import (
	"sync"
)

type Session struct {
	State string
	Data  map[string]string
}

type Manager struct {
	sessions map[int64]*Session
	mu       sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[int64]*Session),
	}
}

func (m *Manager) Get(chatID int64) *Session {
	m.mu.RLock()
	s, ok := m.sessions[chatID]
	m.mu.RUnlock()

	if ok {
		return s
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	s = &Session{
		Data: make(map[string]string),
	}
	m.sessions[chatID] = s
	return s
}

func (m *Manager) Reset(chatID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, chatID)
}
