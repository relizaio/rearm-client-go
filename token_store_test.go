package rearm_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/relizaio/rearm-client-go/sessiontest"
)

// TPF-67: key clients share their access token across processes through a TokenStore (lock,
// re-read, adopt or exchange, save), and a 401 to a key client's bearer is exchanged and sent once more.

// keyServer is a fake ReARM for key clients: the token endpoint issues tok-1, tok-2, ... in order
// and counts its POSTs; the GraphQL endpoint answers by the bearer it is shown.
type keyServer struct {
	*httptest.Server
	mu        sync.Mutex
	posts     int
	issued    int
	bearers   []string
	mine      map[string]bool // the tokens this server issued
	tokenCode int             // non-zero: the token endpoint answers this status instead of issuing
	tokenBody string          // with this body
	// accept decides the GraphQL answer to a bearer (nil: every token this server issued)
	accept func(bearer string) bool
}

const invalidTokenChallenge = `Bearer realm="rearm", error="invalid_token"`

func newKeyServer(t *testing.T) *keyServer {
	t.Helper()
	k := &keyServer{mine: map[string]bool{}}
	k.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k.mu.Lock()
		defer k.mu.Unlock()
		switch r.URL.Path {
		case rearm.TokenPath:
			k.posts++
			if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
				t.Errorf("the exchange must POST the key as Basic")
			}
			if k.tokenCode != 0 {
				w.WriteHeader(k.tokenCode)
				_, _ = w.Write([]byte(k.tokenBody))
				return
			}
			k.issued++
			tok := fmt.Sprintf("tok-%d", k.issued)
			k.mine[tok] = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, tok)
		case rearm.ProgrammaticPath:
			auth := r.Header.Get("Authorization")
			bearer := strings.TrimPrefix(auth, "Bearer ")
			if bearer == auth {
				bearer = ""
			}
			k.bearers = append(k.bearers, bearer)
			ok := k.mine[bearer]
			if k.accept != nil {
				ok = k.accept(bearer)
			}
			if !ok {
				w.Header().Set("WWW-Authenticate", invalidTokenChallenge)
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"__typename":"Query"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(k.Close)
	return k
}

func (k *keyServer) counts() (posts int, bearers []string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.posts, append([]string(nil), k.bearers...)
}

func (k *keyServer) set(f func(k *keyServer)) {
	k.mu.Lock()
	defer k.mu.Unlock()
	f(k)
}

func keyClient(t *testing.T, k *keyServer, store rearm.TokenStore) *rearm.Client {
	t.Helper()
	var opts []rearm.Option
	if store != nil {
		opts = append(opts, rearm.WithTokenStore(store))
	}
	c, err := rearm.New(k.URL, "FREEFORM__org__ord__1", "secret", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func stored(token string, in time.Duration) rearm.SessionTokens {
	return rearm.SessionTokens{AccessToken: token, AccessTokenExpiry: time.Now().Add(in)}
}

func near(a, b time.Time, d time.Duration) bool { return a.Sub(b) < d && b.Sub(a) < d }

func TestAnEmptyTokenStoreGetsTheExchangedToken(t *testing.T) { // C1
	k := newKeyServer(t)
	store := sessiontest.NewMemoryTokenStore(rearm.SessionTokens{})
	mustCall(t, keyClient(t, k, store))
	posts, bearers := k.counts()
	saved := store.Saved()
	if posts != 1 || len(saved) != 1 || saved[0].AccessToken != "tok-1" {
		t.Fatalf("one POST and one Save of tok-1, got posts=%d saves=%+v", posts, saved)
	}
	if !near(saved[0].AccessTokenExpiry, time.Now().Add(time.Hour), time.Second) {
		t.Fatalf("the saved expiry is the server's (now + 3600 s), got %s", saved[0].AccessTokenExpiry)
	}
	if len(bearers) != 1 || bearers[0] != "tok-1" {
		t.Fatalf("the call carries the exchanged bearer, got %v", bearers)
	}
}

func TestAFreshStoredTokenIsAdoptedWithoutAnExchange(t *testing.T) { // C2
	k := newKeyServer(t)
	k.mine["tok-0"] = true // minted by this server for an earlier process
	store := sessiontest.NewMemoryTokenStore(stored("tok-0", 30*time.Minute))
	mustCall(t, keyClient(t, k, store))
	posts, bearers := k.counts()
	if posts != 0 || len(store.Saved()) != 0 {
		t.Fatalf("no POST and no Save, got posts=%d saves=%d", posts, len(store.Saved()))
	}
	if len(bearers) != 1 || bearers[0] != "tok-0" {
		t.Fatalf("the call carries the stored bearer, got %v", bearers)
	}
}

func TestAStoredTokenInsideItsLastMinuteIsReplaced(t *testing.T) { // C3
	k := newKeyServer(t)
	store := sessiontest.NewMemoryTokenStore(stored("tok-0", 30*time.Second))
	mustCall(t, keyClient(t, k, store))
	posts, bearers := k.counts()
	if posts != 1 || len(store.Saved()) != 1 || store.Get().AccessToken != "tok-1" {
		t.Fatalf("one POST and one Save of the new token, got posts=%d store=%+v", posts, store.Saved())
	}
	if len(bearers) != 1 || bearers[0] != "tok-1" {
		t.Fatalf("the new token is sent, got %v", bearers)
	}
}

func TestARejectedStoredTokenIsExchangedOnceAndResent(t *testing.T) { // C4
	k := newKeyServer(t)
	store := sessiontest.NewMemoryTokenStore(stored("tok-0", 30*time.Minute))
	mustCall(t, keyClient(t, k, store)) // tok-0 is not one the server issued: 401, then tok-1
	posts, bearers := k.counts()
	if posts != 1 || len(store.Saved()) != 1 || store.Get().AccessToken != "tok-1" {
		t.Fatalf("one POST and one Save, got posts=%d saves=%+v", posts, store.Saved())
	}
	if len(bearers) != 2 || bearers[0] != "tok-0" || bearers[1] != "tok-1" {
		t.Fatalf("two GraphQL requests, tok-0 then tok-1, got %v", bearers)
	}
}

func TestEveryBearerRefusedFromAStoredTokenIsOneExchangeAndNoLoop(t *testing.T) { // C5
	k := newKeyServer(t)
	k.accept = func(string) bool { return false }
	store := sessiontest.NewMemoryTokenStore(stored("tok-0", 30*time.Minute))
	err := call(keyClient(t, k, store))
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("the second 401 is returned, got %v", err)
	}
	posts, bearers := k.counts()
	saved := store.Saved()
	if posts != 1 || len(saved) != 1 || saved[0].AccessToken != "tok-1" {
		t.Fatalf("one POST and one Save of tok-1, got posts=%d saves=%+v", posts, saved)
	}
	if len(bearers) != 2 || bearers[0] != "tok-0" || bearers[1] != "tok-1" {
		t.Fatalf("two GraphQL requests, tok-0 then tok-1, got %v", bearers)
	}
}

func TestARefusedReExchangeRemovesTheStoredToken(t *testing.T) { // C5, second half
	k := newKeyServer(t)
	k.tokenCode, k.tokenBody = http.StatusBadRequest, `{"error":"invalid_client","error_description":"key inactive"}`
	store := sessiontest.NewMemoryTokenStore(stored("tok-0", 30*time.Minute))
	err := call(keyClient(t, k, store))
	if err == nil || !strings.Contains(err.Error(), "token exchange failed: invalid_client") {
		t.Fatalf("the exchange error is returned, got %v", err)
	}
	posts, bearers := k.counts()
	saved := store.Saved()
	if posts != 1 || len(bearers) != 1 || bearers[0] != "tok-0" {
		t.Fatalf("one refused POST and one GraphQL request (no resend), got posts=%d bearers=%v", posts, bearers)
	}
	if len(saved) != 1 || saved[0].AccessToken != "" {
		t.Fatalf("one Save of an empty token (the entry removed), got %+v", saved)
	}
	if _, err := store.Load(); !errors.Is(err, rearm.ErrNoToken) {
		t.Fatalf("a later Load finds nothing, got %v", err)
	}
}

func TestAnEmptyStoreAndEveryBearerRefusedIsTwoExchangesAndNoLoop(t *testing.T) { // C11
	k := newKeyServer(t)
	k.accept = func(string) bool { return false }
	store := sessiontest.NewMemoryTokenStore(rearm.SessionTokens{})
	err := call(keyClient(t, k, store))
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("the second 401 is returned, got %v", err)
	}
	posts, bearers := k.counts()
	saved := store.Saved()
	if posts != 2 || len(saved) != 2 || saved[0].AccessToken != "tok-1" || saved[1].AccessToken != "tok-2" {
		t.Fatalf("two POSTs and two Saves (tok-1, tok-2), got posts=%d saves=%+v", posts, saved)
	}
	if len(bearers) != 2 || bearers[0] != "tok-1" || bearers[1] != "tok-2" {
		t.Fatalf("two GraphQL requests, tok-1 then tok-2, got %v", bearers)
	}
}

func TestNoTokenEndpointWritesNothingAndSendsBasic(t *testing.T) { // C6
	k := newKeyServer(t)
	k.tokenCode = http.StatusNotFound
	k.accept = func(b string) bool { return b == "" }
	store := sessiontest.NewMemoryTokenStore(rearm.SessionTokens{})
	mustCall(t, keyClient(t, k, store))
	posts, bearers := k.counts()
	if posts != 1 || len(store.Saved()) != 0 {
		t.Fatalf("one 404 probe and no Save, got posts=%d saves=%+v", posts, store.Saved())
	}
	if len(bearers) != 1 || bearers[0] != "" {
		t.Fatalf("the call goes out as Basic, got bearers %v", bearers)
	}
}

func TestTwoClientsOnAnEmptyStoreExchangeOnce(t *testing.T) { // C7
	k := newKeyServer(t)
	store := sessiontest.NewMemoryTokenStore(rearm.SessionTokens{})
	clients := []*rearm.Client{keyClient(t, k, store), keyClient(t, k, store)}
	var wg sync.WaitGroup
	errs := make(chan error, len(clients))
	start := make(chan struct{})
	for _, c := range clients {
		wg.Add(1)
		go func(c *rearm.Client) {
			defer wg.Done()
			<-start
			errs <- call(c)
		}(c)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("both calls succeed, got %v", err)
		}
	}
	posts, _ := k.counts()
	if posts != 1 || len(store.Saved()) != 1 {
		t.Fatalf("one POST and one Save in total, got posts=%d saves=%d", posts, len(store.Saved()))
	}
}

func TestAStoreThatFailsNeverFailsTheRequest(t *testing.T) { // C8
	failure := errors.New("disk on fire")
	t.Run("Lock error", func(t *testing.T) {
		k := newKeyServer(t)
		store := sessiontest.NewMemoryTokenStore(rearm.SessionTokens{})
		store.FailLocks(failure)
		mustCall(t, keyClient(t, k, store))
		if posts, _ := k.counts(); posts != 1 || store.Loads() != 0 || len(store.Saved()) != 0 {
			t.Fatalf("one POST in place, the store untouched, got posts=%d loads=%d saves=%d", posts, store.Loads(), len(store.Saved()))
		}
	})
	t.Run("Lock timeout", func(t *testing.T) {
		saved := rearm.LockWait
		rearm.LockWait = 50 * time.Millisecond
		t.Cleanup(func() { rearm.LockWait = saved })
		k := newKeyServer(t)
		store := sessiontest.NewMemoryTokenStore(rearm.SessionTokens{})
		release := store.Hold()
		defer release()
		mustCall(t, keyClient(t, k, store))
		if posts, _ := k.counts(); posts != 1 || store.Loads() != 0 || len(store.Saved()) != 0 {
			t.Fatalf("one POST in place, the store untouched, got posts=%d loads=%d saves=%d", posts, store.Loads(), len(store.Saved()))
		}
	})
	t.Run("Save error", func(t *testing.T) {
		k := newKeyServer(t)
		store := sessiontest.NewMemoryTokenStore(rearm.SessionTokens{})
		store.FailSaves(failure)
		mustCall(t, keyClient(t, k, store))
		if posts, _ := k.counts(); posts != 1 {
			t.Fatalf("one POST, got %d", posts)
		}
	})
}

func TestSessionAndAssertionClientsIgnoreATokenStore(t *testing.T) { // C9
	t.Run("session", func(t *testing.T) {
		srv := sessiontest.NewServer(t, sessiontest.Options{})
		_, set := srv.Login()
		store := sessiontest.NewMemoryTokenStore(stored("tok-0", 30*time.Minute))
		c, err := rearm.NewWithSession(srv.URL, set.RefreshToken, set, nil, rearm.WithTokenStore(store))
		if err != nil {
			t.Fatal(err)
		}
		mustCall(t, c)
		rearm.ExpireAccessToken(c)
		mustCall(t, c)
		if store.Locks() != 0 || store.Loads() != 0 || len(store.Saved()) != 0 {
			t.Fatalf("the token store is never called, got locks=%d loads=%d saves=%d", store.Locks(), store.Loads(), len(store.Saved()))
		}
	})
	t.Run("assertion", func(t *testing.T) {
		var exchanges int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case rearm.TokenPath:
				exchanges++
				_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","expires_in":3600,"org":"org-1"}`))
			case rearm.ProgrammaticPath:
				if r.Header.Get("Authorization") != "Bearer at" {
					t.Errorf("expected the assertion's bearer, got %q", r.Header.Get("Authorization"))
				}
				_, _ = w.Write([]byte(`{"data":{"__typename":"Query"}}`))
			}
		}))
		defer srv.Close()
		store := sessiontest.NewMemoryTokenStore(stored("tok-0", 30*time.Minute))
		source := func(context.Context) (string, error) { return "id-token", nil }
		c, err := rearm.NewWithAssertion(srv.URL, "org-1", source, rearm.WithTokenStore(store))
		if err != nil {
			t.Fatal(err)
		}
		mustCall(t, c)
		if exchanges != 1 || store.Locks() != 0 || store.Loads() != 0 || len(store.Saved()) != 0 {
			t.Fatalf("one assertion exchange, the token store never called, got exchanges=%d locks=%d loads=%d saves=%d",
				exchanges, store.Locks(), store.Loads(), len(store.Saved()))
		}
	})
}
