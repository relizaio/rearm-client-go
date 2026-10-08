package rearm_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/relizaio/rearm-client-go/sessiontest"
)

// S401-1 round 2: the guards of the renewal that no round-1 test pinned (test report run 1, T-13,
// T-16 and T-18).

// T-13: goroutines of one client that all get a 401 for the same refused bearer renew once. The
// first renews; the others find the bearer already replaced and resend as is. Without a store
// nothing else stops each of them refreshing again with the token just retired.
func TestConcurrent401sOnOneRefusedBearerRenewOnce(t *testing.T) {
	for _, withStore := range []bool{false, true} {
		// the refresh answer is slow, so every goroutine is refused before the first renewal ends
		srv := sessiontest.NewServer(t, sessiontest.Options{RefreshDelay: 300 * time.Millisecond})
		_, set := srv.Login()
		var store rearm.SessionStore
		if withStore {
			store = sessiontest.NewMemoryStore(set)
		}
		c := sessionClient(t, srv, store, set, nil)
		mustCall(t, c)
		srv.RotateDirectly() // the client's bearer is dead long before its exp
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- call(c)
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("store=%v: a request failed: %v", withStore, err)
			}
		}
		n := srv.Counts()
		if n.Rejected401 < 2 {
			t.Fatalf("store=%v: the case needs several goroutines refused on one bearer, got %+v", withStore, n)
		}
		if n.Refreshes != 1 || n.GraceRerotations != 1 || n.Reused != 0 || n.Revocations != 0 || n.InvalidGrants != 0 {
			t.Fatalf("store=%v: one renewal for every 401 on the same bearer, got %+v", withStore, n)
		}
		if !srv.IsCurrent(c.Tokens().RefreshToken) {
			t.Fatalf("store=%v: the client ends on the server's current refresh token", withStore)
		}
	}
}

// T-16: a refresh made while holding the store's lock carries its own deadline, so a hung token
// endpoint gives the lock back long before the client's own timeout.
func TestARefreshUnderTheLockHasItsOwnDeadline(t *testing.T) {
	t.Cleanup(rearm.SetRefreshWait(100 * time.Millisecond))
	srv := sessiontest.NewServer(t, sessiontest.Options{RefreshDelay: 5 * time.Second})
	_, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	c := sessionClient(t, srv, store, set, nil)
	rearm.ExpireAccessToken(c)
	began := time.Now()
	err := call(c)
	took := time.Since(began)
	if err == nil || !strings.Contains(err.Error(), "session refresh") || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected the refresh to give up at its deadline, got %v", err)
	}
	if took > 2*time.Second {
		t.Fatalf("the refresh must stop at its own deadline, took %v", took)
	}
	holds := store.Holds()
	if len(holds) != 1 || holds[0].End.IsZero() || len(holds[0].Saves) != 0 {
		t.Fatalf("one hold, given back, nothing saved, got %+v", holds)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	release, err := store.Lock(ctx)
	if err != nil {
		t.Fatalf("the lock is free again: %v", err)
	}
	release()
	if n := srv.Counts(); n.Refreshes != 1 {
		t.Fatalf("one refresh attempt, got %+v", n)
	}
}

// T-18: a set adopted from the store is clamped to the session's hard end, as a refreshed one is,
// even when the stored access token says it lives longer.
func TestAnAdoptedSetIsClampedToTheHardEnd(t *testing.T) {
	srv := sessiontest.NewServer(t, sessiontest.Options{})
	_, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	a := sessionClient(t, srv, store, set, nil)
	c := sessionClient(t, srv, store, set, nil)
	rearm.ExpireAccessToken(a)
	mustCall(t, a) // A refreshes and saves a set whose token runs about an hour
	hard := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	stored := store.Get()
	if !stored.AccessTokenExpiry.After(hard) {
		t.Fatalf("the stored token must outlive the hard end for this case, got %v", stored.AccessTokenExpiry)
	}
	stored.SessionHardExpiry = hard // another process learnt the hard end; the token on file was not clamped
	store.Set(stored)
	rearm.ExpireAccessToken(c)
	mustCall(t, c) // C adopts the stored set: usable, no refresh
	got := c.Tokens()
	if got.AccessToken != stored.AccessToken || !got.SessionHardExpiry.Equal(hard) {
		t.Fatalf("C adopts the stored set, got %+v", got)
	}
	if !got.AccessTokenExpiry.Equal(hard) {
		t.Fatalf("the adopted token is planned to end at the hard end %v, got %v", hard, got.AccessTokenExpiry)
	}
	if n := srv.Counts(); n.Refreshes != 1 {
		t.Fatalf("A's refresh only, got %+v", n)
	}
}
