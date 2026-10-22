package sessiontest

import (
	"context"
	"sync"

	rearm "github.com/relizaio/rearm-client-go"
)

// MemoryTokenStore is an in-process rearm.TokenStore with a real lock, for key clients
// (rearm.WithTokenStore). It records every Load and Save, and its Lock, Load and Save can be made
// to fail.
type MemoryTokenStore struct {
	lock chan struct{}

	mu      sync.Mutex
	token   rearm.SessionTokens
	saved   []rearm.SessionTokens
	lockErr error
	loadErr error
	saveErr error
	locks   int
	loads   int
}

var _ rearm.TokenStore = (*MemoryTokenStore)(nil)

// NewMemoryTokenStore holds initial (AccessToken and AccessTokenExpiry; an empty AccessToken is an
// empty store).
func NewMemoryTokenStore(initial rearm.SessionTokens) *MemoryTokenStore {
	return &MemoryTokenStore{lock: make(chan struct{}, 1), token: initial}
}

// Lock waits for the lock until ctx ends.
func (m *MemoryTokenStore) Lock(ctx context.Context) (func(), error) {
	m.mu.Lock()
	m.locks++
	err := m.lockErr
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case m.lock <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() { once.Do(func() { <-m.lock }) }, nil
}

// Load returns the stored token, or rearm.ErrNoToken when the store holds none.
func (m *MemoryTokenStore) Load() (rearm.SessionTokens, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loads++
	if m.loadErr != nil {
		return rearm.SessionTokens{}, m.loadErr
	}
	if m.token.AccessToken == "" {
		return rearm.SessionTokens{}, rearm.ErrNoToken
	}
	return m.token, nil
}

// Save replaces the stored token; an empty AccessToken empties the store.
func (m *MemoryTokenStore) Save(t rearm.SessionTokens) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saved = append(m.saved, t)
	m.token = rearm.SessionTokens{AccessToken: t.AccessToken, AccessTokenExpiry: t.AccessTokenExpiry}
	return nil
}

// Get is the stored token.
func (m *MemoryTokenStore) Get() rearm.SessionTokens {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.token
}

// Saved returns every token a successful Save stored, in order (an empty one is a removal).
func (m *MemoryTokenStore) Saved() []rearm.SessionTokens {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]rearm.SessionTokens(nil), m.saved...)
}

// Locks counts the Lock calls (failed ones included).
func (m *MemoryTokenStore) Locks() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.locks
}

// Loads counts the Load calls.
func (m *MemoryTokenStore) Loads() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loads
}

// Hold takes the lock outside any client (another process holding it) until release is called.
func (m *MemoryTokenStore) Hold() (release func()) {
	m.lock <- struct{}{}
	var once sync.Once
	return func() { once.Do(func() { <-m.lock }) }
}

// FailLocks makes every Lock return err (nil to stop).
func (m *MemoryTokenStore) FailLocks(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lockErr = err
}

// FailLoads makes every Load return err (nil to stop).
func (m *MemoryTokenStore) FailLoads(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadErr = err
}

// FailSaves makes every Save return err (nil to stop).
func (m *MemoryTokenStore) FailSaves(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveErr = err
}
