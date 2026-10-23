package rearm

import (
	"context"
	"errors"
	"time"
)

// TokenStore is where every process using one API key keeps the key's access token (the CLI's
// token cache file). The client exchanges only while holding its lock: it re-reads the store first
// and adopts a fresh token another process saved instead of exchanging, and it saves the token it
// obtained before giving the lock back. A store failure (lock, load, save) never fails a request:
// the client exchanges in place as it does without a store.
type TokenStore interface {
	// Lock takes the cross-process lock, waiting until ctx ends; release gives it back.
	Lock(ctx context.Context) (release func(), err error)
	// Load reads the stored token: AccessToken and AccessTokenExpiry (the server's expiry, not the
	// client's planned renewal). A store with no usable token for this key returns ErrNoToken.
	Load() (SessionTokens, error)
	// Save writes the token (AccessToken and AccessTokenExpiry); an empty AccessToken removes the
	// entry. Called only between Lock and release.
	Save(SessionTokens) error
}

// ErrNoToken is what TokenStore.Load returns when the store holds no token for the key.
var ErrNoToken = errors.New("rearm: no access token in the store")

// WithTokenStore makes a key client (New) keep its access token in a store shared with the other
// processes of one key. Ignored by NewWithSession and NewWithAssertion.
func WithTokenStore(s TokenStore) Option { return func(o *options) { o.tokenStore = s } }

// renewKey gets a key client a token it can send. rejected is the bearer the server just refused
// with a 401 ("" when the token is merely due or there is none yet): that token is dead whatever
// its expiry says. One renewal runs at a time per client; a goroutine that waited for another
// one's renewal uses its result, so a rejected token is exchanged at most once per client.
func (t *authTransport) renewKey(ctx context.Context, now time.Time, rejected string) error {
	if t.renewing != nil {
		select {
		case t.renewing <- struct{}{}:
			defer func() { <-t.renewing }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.mu.Lock()
	cur, fresh, exchange := t.bearer, t.bearer != "" && now.Before(t.bearerExp), t.exchange
	t.mu.Unlock()
	if !exchange {
		return nil // another goroutine found no token endpoint here: Basic from now on
	}
	if rejected != "" && cur != rejected {
		return nil // renewed by another goroutine since the refused request went out
	}
	if rejected == "" && fresh {
		return nil // renewed by another goroutine while this one waited
	}
	if t.tokenStore == nil {
		_, err := t.exchangeKey(ctx)
		return err
	}
	lctx, cancel := context.WithTimeout(ctx, LockWait)
	release, err := t.tokenStore.Lock(lctx)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The lock is an economy, not a correctness device: key tokens are independent of each other,
		// so a process that exchanges on its own harms nobody.
		_, err := t.exchangeKey(ctx)
		return err
	}
	defer release()
	if on, err := t.tokenStore.Load(); err == nil && on.AccessToken != "" && on.AccessToken != rejected &&
		now.Before(on.AccessTokenExpiry.Add(-time.Minute)) {
		// another process (or an earlier one) exchanged: adopt its token, no server call
		t.mu.Lock()
		t.bearer = on.AccessToken
		t.bearerExp = on.AccessTokenExpiry.Add(-time.Minute)
		t.mu.Unlock()
		return nil
	}
	// a hung network must not hold the lock for the client's whole timeout
	rctx, rcancel := context.WithTimeout(ctx, refreshWait)
	outcome, err := t.exchangeKey(rctx)
	rcancel()
	switch outcome {
	case keyIssued:
		t.mu.Lock()
		got := SessionTokens{AccessToken: t.bearer, AccessTokenExpiry: t.bearerExp.Add(time.Minute)}
		t.mu.Unlock()
		_ = t.tokenStore.Save(got) // a token that cannot be stored is still good for this process
	case keyRefused:
		if rejected != "" {
			// the key or its secret is gone: no later process should present the dead token
			_ = t.tokenStore.Save(SessionTokens{})
		}
	}
	return err
}
