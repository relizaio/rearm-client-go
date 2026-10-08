package rearm_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/relizaio/rearm-client-go/sessiontest"
)

// S401-1: processes sharing one browser login renew through a SessionStore (lock, re-read, adopt
// or refresh, save) and retry a 401 once, so a refresh by one of them never kills the others.

type recorder struct {
	mu  sync.Mutex
	got []rearm.SessionTokens
}

func (r *recorder) persist(s rearm.SessionTokens) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, s)
}

func (r *recorder) all() []rearm.SessionTokens {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]rearm.SessionTokens(nil), r.got...)
}

func sessionClient(t *testing.T, srv *sessiontest.Server, store rearm.SessionStore, set rearm.SessionTokens, rec *recorder) *rearm.Client {
	t.Helper()
	var opts []rearm.Option
	if store != nil {
		opts = append(opts, rearm.WithSessionStore(store))
	}
	var persist func(rearm.SessionTokens)
	if rec != nil {
		persist = rec.persist
	}
	c, err := rearm.NewWithSession(srv.URL, set.RefreshToken, set, persist, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func call(c *rearm.Client) error {
	_, err := rearm.Raw(context.Background(), c, "Ping", "query Ping { __typename }", nil)
	return err
}

func mustCall(t *testing.T, c *rearm.Client) {
	t.Helper()
	if err := call(c); err != nil {
		t.Fatalf("request failed: %v", err)
	}
}

func noHarm(t *testing.T, srv *sessiontest.Server) {
	t.Helper()
	n := srv.Counts()
	if n.GraceRerotations != 0 || n.Reused != 0 || n.Revocations != 0 || n.InvalidGrants != 0 || srv.Revoked() {
		t.Fatalf("expected no grace re-rotation, no reuse, no revocation, no invalid_grant, got %+v", n)
	}
}

func TestThreeClientsOnOneStoreRefreshOnce(t *testing.T) {
	srv := sessiontest.NewServer(t, sessiontest.Options{})
	_, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	clients := []*rearm.Client{sessionClient(t, srv, store, set, nil), sessionClient(t, srv, store, set, nil), sessionClient(t, srv, store, set, nil)}
	for _, c := range clients {
		mustCall(t, c)
	}
	for cycle := 1; cycle <= 3; cycle++ {
		// an hour passes for everyone: the server's tokens expire and every client finds its own due
		srv.Advance(time.Hour)
		for _, c := range clients {
			rearm.ExpireAccessToken(c)
		}
		before := srv.Counts().Refreshes
		var wg sync.WaitGroup
		errs := make(chan error, 3*len(clients))
		for _, c := range clients {
			wg.Add(1)
			go func(c *rearm.Client) {
				defer wg.Done()
				for i := 0; i < 3; i++ {
					errs <- call(c)
				}
			}(c)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("cycle %d: a request failed: %v", cycle, err)
			}
		}
		// and sequential, interleaved requests between the forcings
		for _, i := range []int{2, 0, 1, 1, 2, 0} {
			mustCall(t, clients[i])
		}
		if got := srv.Counts().Refreshes - before; got != 1 {
			t.Fatalf("cycle %d: expected exactly one refresh, got %d", cycle, got)
		}
	}
	if n := srv.Counts(); n.Rejected401 != 0 {
		t.Fatalf("no request should have been refused, got %+v", n)
	}
	noHarm(t, srv)
	stored := store.Get().RefreshToken
	if !srv.IsCurrent(stored) {
		t.Fatal("the store must hold the server's current refresh token")
	}
	for i, c := range clients {
		if c.Tokens().RefreshToken != stored {
			t.Fatalf("client %d holds another refresh token than the store", i)
		}
	}
}

func TestARejectedTokenIsAdoptedFromTheStoreAndRetriedOnce(t *testing.T) {
	srv := sessiontest.NewServer(t, sessiontest.Options{HTML401: true})
	_, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	var recB recorder
	a := sessionClient(t, srv, store, set, nil)
	b := sessionClient(t, srv, store, set, &recB)
	mustCall(t, a)
	mustCall(t, b)
	rearm.ExpireAccessToken(a)
	mustCall(t, a) // A refreshes: B's access token is dead from here on, long before its exp
	if n := srv.Counts(); n.Refreshes != 1 {
		t.Fatalf("A refreshes once, got %+v", n)
	}
	mustCall(t, b) // 401 (HTML), reload, adopt A's set, retry
	n := srv.Counts()
	if n.Refreshes != 1 || n.Rejected401 != 1 {
		t.Fatalf("B makes no refresh and sees one 401 it recovers from, got %+v", n)
	}
	got := recB.all()
	if len(got) != 1 || got[0].AccessToken != a.Tokens().AccessToken || got[0].RefreshToken != a.Tokens().RefreshToken {
		t.Fatalf("B's persist receives the adopted set, rotated refresh token included, got %+v", got)
	}
	noHarm(t, srv)
}

func TestARejectedTokenWithNothingFresherRefreshesOnce(t *testing.T) {
	srv := sessiontest.NewServer(t, sessiontest.Options{})
	_, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	c := sessionClient(t, srv, store, set, nil)
	mustCall(t, c)
	srv.RotateDirectly() // a process outside the store rotates: the client's token is dead
	mustCall(t, c)       // 401, the store holds nothing fresher, refresh (inside the grace), save, retry
	n := srv.Counts()
	if n.Rejected401 != 1 || n.Refreshes != 1 || n.GraceRerotations != 1 || n.Revocations != 0 {
		t.Fatalf("one 401, one refresh with the retired token inside the grace, got %+v", n)
	}
	if store.Get().RefreshToken != c.Tokens().RefreshToken || !srv.IsCurrent(c.Tokens().RefreshToken) {
		t.Fatal("the refreshed set is saved and current")
	}
	// a server that refuses everything: the request returns the 401 after exactly two sends
	srv.RefuseAll(true)
	before := srv.Counts()
	err := call(c)
	if err == nil || !strings.Contains(err.Error(), "request failed with status 401") {
		t.Fatalf("expected the 401 to be returned, got %v", err)
	}
	after := srv.Counts()
	if sends := after.Rejected401 - before.Rejected401; sends != 2 {
		t.Fatalf("expected exactly two sends, got %d", sends)
	}
	if after.Refreshes-before.Refreshes != 1 {
		t.Fatalf("one refresh between the two sends, got %d", after.Refreshes-before.Refreshes)
	}
}

func TestTheRetiredTokenIsNeverPresentedAfterTheGrace(t *testing.T) {
	srv := sessiontest.NewServer(t, sessiontest.Options{})
	old, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	a := sessionClient(t, srv, store, set, nil)
	b := sessionClient(t, srv, store, set, nil)
	rearm.ExpireAccessToken(a)
	mustCall(t, a) // t0: A rotates; B still holds the old set
	srv.Advance(3 * time.Minute)
	// the store's access token is due too, so B must refresh: with the store's refresh token
	s := store.Get()
	s.AccessTokenExpiry = time.Now().Add(-time.Second)
	store.Set(s)
	rearm.ExpireAccessToken(b)
	mustCall(t, b)
	for i, p := range srv.Presented() {
		if i > 0 && p == old {
			t.Fatal("the retired refresh token was presented again")
		}
	}
	if n := srv.Counts(); n.Refreshes != 2 {
		t.Fatalf("A once, B once with the current token, got %+v", n)
	}
	noHarm(t, srv)

	// variant: the store's set is usable, so B makes no refresh at all
	srv2 := sessiontest.NewServer(t, sessiontest.Options{})
	_, set2 := srv2.Login()
	store2 := sessiontest.NewMemoryStore(set2)
	a2 := sessionClient(t, srv2, store2, set2, nil)
	b2 := sessionClient(t, srv2, store2, set2, nil)
	rearm.ExpireAccessToken(a2)
	mustCall(t, a2)
	srv2.Advance(3 * time.Minute)
	rearm.ExpireAccessToken(b2)
	mustCall(t, b2)
	if n := srv2.Counts(); n.Refreshes != 1 || n.Rejected401 != 0 {
		t.Fatalf("B adopts the usable set, got %+v", n)
	}
	noHarm(t, srv2)
}

func TestTheLockSpansRefreshAndSave(t *testing.T) {
	srv := sessiontest.NewServer(t, sessiontest.Options{RefreshDelay: 300 * time.Millisecond})
	_, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	a := sessionClient(t, srv, store, set, nil)
	b := sessionClient(t, srv, store, set, nil)
	rearm.ExpireAccessToken(a)
	rearm.ExpireAccessToken(b)
	done := make(chan error, 1)
	go func() { done <- call(a) }()
	deadline := time.Now().Add(5 * time.Second)
	for srv.Counts().Refreshes == 0 {
		if time.Now().After(deadline) {
			t.Fatal("A never refreshed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A holds the lock, its refresh in flight: B wants a token now and blocks on the lock
	if err := call(b); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	holds := store.Holds()
	refreshes := srv.RefreshLog()
	if len(refreshes) != 1 || len(holds) != 2 {
		t.Fatalf("one refresh and two holds, got refreshes=%d holds=%d", len(refreshes), len(holds))
	}
	first, second := holds[0], holds[1]
	if refreshes[0].Before(first.Start) || refreshes[0].After(first.End) {
		t.Fatal("the refresh falls outside the first hold")
	}
	if len(first.Saves) != 1 || first.Saves[0].Before(refreshes[0]) || first.Saves[0].After(first.End) {
		t.Fatalf("the Save falls inside the hold, after the refresh, got %v", first.Saves)
	}
	if !second.Requested.Before(first.End) || len(second.Saves) != 0 {
		t.Fatal("B asked for the lock during A's hold, then saved nothing")
	}
	if b.Tokens().RefreshToken != a.Tokens().RefreshToken {
		t.Fatal("B read the saved set")
	}
	noHarm(t, srv)
}

func TestWithoutAStoreTheOldPathStands(t *testing.T) {
	srv := sessiontest.NewServer(t, sessiontest.Options{})
	_, set := srv.Login()
	var rec recorder
	c := sessionClient(t, srv, nil, set, &rec)
	rearm.ExpireAccessToken(c)
	mustCall(t, c)
	if n := srv.Counts(); n.Refreshes != 1 || len(rec.all()) != 1 || rec.all()[0].RefreshToken == "" {
		t.Fatalf("refresh in place, the rotated set through persist, got %+v %+v", n, rec.all())
	}
	srv.RotateDirectly()
	mustCall(t, c) // 401, one refresh with the in-memory token (inside the grace), retry
	if n := srv.Counts(); n.Refreshes != 2 || n.Rejected401 != 1 || n.GraceRerotations != 1 {
		t.Fatalf("one refresh with the in-memory token, got %+v", n)
	}
	srv.RefuseAll(true)
	err := call(c)
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("the second 401 is returned, got %v", err)
	}
	if n := srv.Counts(); n.Rejected401 != 3 {
		t.Fatalf("two sends for the refused request, got %+v", n)
	}
}

func TestAStoreWithNoSessionIsASessionError(t *testing.T) {
	srv := sessiontest.NewServer(t, sessiontest.Options{})
	_, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	c := sessionClient(t, srv, store, set, nil)
	store.Clear()
	rearm.ExpireAccessToken(c)
	err := call(c)
	var se *rearm.SessionError
	if !errors.As(err, &se) || se.Code != "invalid_grant" || se.Description != "no browser-login session on file; run rearm login" {
		t.Fatalf("expected the no-session SessionError, got %v", err)
	}
	if n := srv.Counts(); n.Refreshes != 0 || n.GraphQL != 0 || n.Rejected401 != 0 {
		t.Fatalf("no server call, got %+v", n)
	}
}

func TestASaveFailureIsReported(t *testing.T) {
	srv := sessiontest.NewServer(t, sessiontest.Options{})
	_, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	c := sessionClient(t, srv, store, set, nil)
	store.FailSaves(errors.New("read-only file system"))
	rearm.ExpireAccessToken(c)
	err := call(c)
	if err == nil || !strings.Contains(err.Error(), "session refreshed but could not be stored") || !strings.Contains(err.Error(), "read-only file system") {
		t.Fatalf("expected the save failure, got %v", err)
	}
	if got := c.Tokens(); got.RefreshToken == set.RefreshToken || !srv.IsCurrent(got.RefreshToken) || got.AccessToken == set.AccessToken {
		t.Fatal("the new set is kept in memory")
	}
}

func TestALockNotTakenInTimeIsAnError(t *testing.T) {
	saved := rearm.LockWait
	rearm.LockWait = 50 * time.Millisecond
	t.Cleanup(func() { rearm.LockWait = saved })
	srv := sessiontest.NewServer(t, sessiontest.Options{})
	_, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	release, err := store.Lock(context.Background()) // another process holds it and never lets go
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	c := sessionClient(t, srv, store, set, nil)
	rearm.ExpireAccessToken(c)
	start := time.Now()
	err = call(c)
	if err == nil || !strings.Contains(err.Error(), "the credentials store has been locked by another process for 50ms") {
		t.Fatalf("expected the lock timeout, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the wait is bounded, took %v", time.Since(start))
	}
	if n := srv.Counts(); n.Refreshes != 0 {
		t.Fatalf("no refresh without the lock, got %+v", n)
	}
}

func TestTheHardEndIsHonouredThroughTheStore(t *testing.T) {
	srv := sessiontest.NewServer(t, sessiontest.Options{})
	_, set := srv.Login()
	store := sessiontest.NewMemoryStore(set)
	c := sessionClient(t, srv, store, set, nil)
	// another process learnt the session's hard end, and it has passed
	hard := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	ended := store.Get()
	ended.AccessToken, ended.AccessTokenExpiry, ended.SessionHardExpiry = "at-last", hard, hard
	store.Set(ended)
	rearm.ExpireAccessToken(c)
	err := call(c)
	var se *rearm.SessionError
	want := "session ended at " + hard.Format(time.RFC3339) + "; run rearm login"
	if !errors.As(err, &se) || se.Code != "invalid_grant" || se.Description != want {
		t.Fatalf("expected %q, got %v", want, err)
	}
	if n := srv.Counts(); n.Refreshes != 0 {
		t.Fatalf("no refresh past the hard end, got %+v", n)
	}
}

func TestOneClientManyGoroutines(t *testing.T) {
	for _, withStore := range []bool{true, false} {
		srv := sessiontest.NewServer(t, sessiontest.Options{})
		_, set := srv.Login()
		var store rearm.SessionStore
		if withStore {
			store = sessiontest.NewMemoryStore(set)
		}
		c := sessionClient(t, srv, store, set, nil)
		srv.Advance(time.Hour)
		rearm.ExpireAccessToken(c)
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); errs <- call(c) }()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("store=%v: %v", withStore, err)
			}
		}
		if n := srv.Counts(); n.Refreshes != 1 || n.Rejected401 != 0 {
			t.Fatalf("store=%v: one refresh for twenty goroutines, got %+v", withStore, n)
		}
		noHarm(t, srv)
	}
}
