package ws

import (
	"context"
	"log/slog"
	"sync"
)

const TextMessage = 1 // WebSocket text frame opcode

// MemoryRegistry is a single-instance registry.
// Does NOT fan out across replicas — use RedisRegistry in production.
type MemoryRegistry struct {
	mu    sync.RWMutex
	conns map[string]map[string]Conn // userID → connID → conn
}

func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{conns: make(map[string]map[string]Conn)}
}

func (m *MemoryRegistry) Start(_ context.Context) error { return nil }
func (m *MemoryRegistry) Stop(_ context.Context) error  { return nil }

func (m *MemoryRegistry) Register(userID, connID string, conn Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conns[userID]; !ok {
		m.conns[userID] = make(map[string]Conn)
	}
	m.conns[userID][connID] = conn
	slog.Debug("ws registered", "user", userID, "conn", connID)
}

func (m *MemoryRegistry) Unregister(userID, connID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.conns[userID]; ok {
		delete(u, connID)
		if len(u) == 0 {
			delete(m.conns, userID)
		}
	}
	slog.Debug("ws unregistered", "user", userID, "conn", connID)
}

func (m *MemoryRegistry) Broadcast(_ context.Context, userID string, payload []byte) {
	m.mu.RLock()
	u, ok := m.conns[userID]
	if !ok {
		m.mu.RUnlock()
		return
	}
	snapshot := make(map[string]Conn, len(u))
	for id, c := range u {
		snapshot[id] = c
	}
	m.mu.RUnlock()

	var dead []string
	for id, c := range snapshot {
		if err := c.WriteMessage(TextMessage, payload); err != nil {
			slog.Warn("ws send failed", "user", userID, "conn", id, "err", err)
			dead = append(dead, id)
		}
	}
	for _, id := range dead {
		m.Unregister(userID, id)
	}
}
