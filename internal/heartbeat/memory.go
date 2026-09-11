package heartbeat

import (
	"context"
	"sync"
	"time"
)

type entry struct {
	lastSeen time.Time
	state    State
}

// MemoryBackend implementa Backend en memoria del proceso. Es el backend por
// defecto: para una unica instancia del servidor (el caso self-hosted mas
// comun) no hace falta Redis para saber que agentes siguen vivos.
type MemoryBackend struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// NewMemoryBackend crea un backend de heartbeat en memoria.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{entries: make(map[string]*entry)}
}

func (b *MemoryBackend) Touch(_ context.Context, agentID string, at time.Time) (State, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[agentID]
	if !ok {
		e = &entry{state: StateUnknown}
		b.entries[agentID] = e
	}
	previous := e.state
	e.lastSeen = at
	e.state = StateOnline
	return previous, nil
}

func (b *MemoryBackend) Sweep(_ context.Context, timeout time.Duration, now time.Time) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var newlyUnreachable []string
	for agentID, e := range b.entries {
		if e.state == StateOnline && now.Sub(e.lastSeen) > timeout {
			e.state = StateUnreachable
			newlyUnreachable = append(newlyUnreachable, agentID)
		}
	}
	return newlyUnreachable, nil
}

func (b *MemoryBackend) State(_ context.Context, agentID string) (State, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[agentID]
	if !ok {
		return StateUnknown, nil
	}
	return e.state, nil
}
