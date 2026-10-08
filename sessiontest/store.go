package sessiontest

import (
	"context"
	"sync"
	"time"

	rearm "github.com/relizaio/rearm-client-go"
)

// Hold is one interval a MemoryStore's lock was held, with the Saves made inside it (wall clock).
type Hold struct {
	// Requested is when Lock was called; Start when it was taken.
	Requested, Start, End time.Time
	Saves                 []time.Time
}

// MemoryStore is an in-process rearm.SessionStore with a real lock. It records every hold and Save,
// and can be made to fail.
type MemoryStore struct {
	lock chan struct{}

	mu      sync.Mutex
	tokens  rearm.SessionTokens
	empty   bool
	saveErr error
	loadErr error
	holds   []Hold
	open    *Hold
	loads   int
	saves   int
}

var _ rearm.SessionStore = (*MemoryStore)(nil)

// NewMemoryStore holds initial (RefreshToken included).
func NewMemoryStore(initial rearm.SessionTokens) *MemoryStore {
	return &MemoryStore{lock: make(chan struct{}, 1), tokens: initial}
}

// Lock waits for the lock until ctx ends.
func (m *MemoryStore) Lock(ctx context.Context) (func(), error) {
	requested := time.Now()
	select {
	case m.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	m.mu.Lock()
	m.open = &Hold{Requested: requested, Start: time.Now()}
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			m.open.End = time.Now()
			m.holds = append(m.holds, *m.open)
			m.open = nil
			m.mu.Unlock()
			<-m.lock
		})
	}, nil
}

// Load returns the stored set, or rearm.ErrNoSession once Clear was called.
func (m *MemoryStore) Load() (rearm.SessionTokens, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loads++
	if m.loadErr != nil {
		return rearm.SessionTokens{}, m.loadErr
	}
	if m.empty {
		return rearm.SessionTokens{}, rearm.ErrNoSession
	}
	return m.tokens, nil
}

// Save replaces the stored set.
func (m *MemoryStore) Save(t rearm.SessionTokens) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saves++
	m.tokens, m.empty = t, false
	if m.open != nil {
		m.open.Saves = append(m.open.Saves, time.Now())
	}
	return nil
}

// Get is the stored set.
func (m *MemoryStore) Get() rearm.SessionTokens {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokens
}

// Set replaces the stored set without the lock (a test's hand on the file).
func (m *MemoryStore) Set(t rearm.SessionTokens) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens, m.empty = t, false
}

// Clear empties the store: Load answers rearm.ErrNoSession (a logout).
func (m *MemoryStore) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens, m.empty = rearm.SessionTokens{}, true
}

// FailSaves makes every Save return err (nil to stop).
func (m *MemoryStore) FailSaves(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveErr = err
}

// FailLoads makes every Load return err (nil to stop).
func (m *MemoryStore) FailLoads(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadErr = err
}

// Holds returns every completed lock hold, in order.
func (m *MemoryStore) Holds() []Hold {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Hold(nil), m.holds...)
}

// Loads and Saves count the calls (failed ones excluded for Saves).
func (m *MemoryStore) Loads() int { m.mu.Lock(); defer m.mu.Unlock(); return m.loads }
func (m *MemoryStore) Saves() int { m.mu.Lock(); defer m.mu.Unlock(); return m.saves }
