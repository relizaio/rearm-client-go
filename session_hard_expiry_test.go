package rearm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A key's sessionMaxMinutes gives a device-login session a hard end (task RD3-7): the client learns
// it from the token responses, never refreshes past it, and works a session delivered with no
// refresh token until its one access token ends.

type hardEndServer struct {
	srv       *httptest.Server
	refreshes int
	bearers   []string
	refresh   func(w http.ResponseWriter)
}

func newHardEndServer(t *testing.T) *hardEndServer {
	h := &hardEndServer{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case TokenPath:
			h.refreshes++
			if h.refresh == nil {
				t.Errorf("no refresh expected")
				w.WriteHeader(500)
				return
			}
			h.refresh(w)
		case ProgrammaticPath:
			h.bearers = append(h.bearers, r.Header.Get("Authorization"))
			_, _ = io.WriteString(w, `{"data":{"getLatestReleaseProgrammatic":{"version":"1.2.3","artifacts":null}}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func query(c *Client) error {
	_, err := Raw(context.Background(), c, "GetLatestReleaseProgrammatic", GetLatestReleaseProgrammatic_Operation, nil)
	return err
}

func endedError(t *testing.T, err error, end time.Time) {
	t.Helper()
	var se *SessionError
	if !errors.As(err, &se) {
		t.Fatalf("expected a SessionError, got %v", err)
	}
	want := "session ended at " + end.UTC().Format(time.RFC3339) + "; run rearm login"
	if se.Code != "invalid_grant" || se.Description != want {
		t.Fatalf("expected %q, got %s / %q", want, se.Code, se.Description)
	}
}

func TestADeliveryWithoutARefreshTokenIsValidOnlyWithAHardEnd(t *testing.T) {
	hard := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
	withHardEnd := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 1795, "api_key_id": "USER__u__ord__k",
			"api_key_uuid": "k", "org": "o", "session": "s", "session_expires_at": hard.Format(time.RFC3339)}
		if withHardEnd {
			body["session_hard_expiry"] = hard.Format(time.RFC3339)
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()
	l, err := PollDeviceLogin(context.Background(), nil, srv.URL, "dc")
	if err != nil || l.Status != DeviceDelivered {
		t.Fatalf("a 30-minute session is delivered with its one token, got %v %+v", err, l)
	}
	if l.RefreshToken != "" || !l.Tokens.SessionHardExpiry.Equal(hard) {
		t.Fatalf("no refresh token and the hard end parsed, got %+v", l)
	}
	if d := time.Until(l.Tokens.AccessTokenExpiry); d > 30*time.Minute || d < 29*time.Minute {
		t.Fatalf("the token's expiry follows expires_in, got %v", d)
	}
	withHardEnd = false
	l, _ = PollDeviceLogin(context.Background(), nil, srv.URL, "dc")
	if l.Status != DeviceInvalid {
		t.Fatalf("no refresh token and no hard end is still a malformed delivery, got %+v", l)
	}
}

func TestASingleTokenSessionWorksUntilItsTokenEndsThenSaysLogInAgain(t *testing.T) {
	h := newHardEndServer(t)
	hard := time.Now().Add(20 * time.Minute)
	c, err := NewWithSession(h.srv.URL, "", SessionTokens{AccessToken: "at-only", AccessTokenExpiry: hard, SessionHardExpiry: hard}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := query(c); err != nil {
		t.Fatal(err)
	}
	if len(h.bearers) != 1 || h.bearers[0] != "Bearer at-only" || h.refreshes != 0 {
		t.Fatalf("the one token is sent and nothing is refreshed, got %v refreshes=%d", h.bearers, h.refreshes)
	}
	// its last minute is still its own: no early end
	if err := c.transport.ensureSessionToken(context.Background(), hard.Add(-30*time.Second)); err != nil {
		t.Fatalf("the token is usable to its real end, got %v", err)
	}
	endedError(t, c.transport.ensureSessionToken(context.Background(), hard.Add(time.Second)), hard)

	ended := time.Now().Add(-time.Minute)
	c, _ = NewWithSession(h.srv.URL, "", SessionTokens{AccessToken: "old", AccessTokenExpiry: ended, SessionHardExpiry: ended}, nil)
	endedError(t, query(c), ended)
	if h.refreshes != 0 || len(h.bearers) != 1 {
		t.Fatalf("an ended session makes no call, got refreshes=%d bearers=%v", h.refreshes, h.bearers)
	}
	if _, err := NewWithSession(h.srv.URL, "", SessionTokens{}, nil); err == nil {
		t.Fatal("neither a refresh token nor an access token is not a session")
	}
}

func TestNoRefreshAtOrPastTheHardEnd(t *testing.T) {
	h := newHardEndServer(t)
	hard := time.Now().Add(-time.Second)
	c, _ := NewWithSession(h.srv.URL, "rt", SessionTokens{AccessToken: "at", AccessTokenExpiry: hard, SessionHardExpiry: hard}, nil)
	endedError(t, query(c), hard)
	if h.refreshes != 0 {
		t.Fatalf("the client knows the session is over and does not ask, got %d refreshes", h.refreshes)
	}

	// a token that already runs to the end is used to it; refreshing could not buy anything
	soon := time.Now().Add(40 * time.Second)
	c, _ = NewWithSession(h.srv.URL, "rt", SessionTokens{AccessToken: "last", AccessTokenExpiry: soon, SessionHardExpiry: soon}, nil)
	if err := query(c); err != nil {
		t.Fatal(err)
	}
	if h.refreshes != 0 || h.bearers[len(h.bearers)-1] != "Bearer last" {
		t.Fatalf("no refresh inside the last token, got refreshes=%d bearers=%v", h.refreshes, h.bearers)
	}
}

func TestARefreshInsideTheWindowIsClippedToTheHardEnd(t *testing.T) {
	h := newHardEndServer(t)
	// 90 seconds left: the answer's short expires_in must not be stretched to the usual minimum past the end
	hard := time.Now().Add(90 * time.Second).UTC().Truncate(time.Second)
	h.refresh = func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-2", "token_type": "Bearer", "expires_in": 90,
			"refresh_token": "rt-2", "session_expires_at": hard.Format(time.RFC3339), "session_hard_expiry": hard.Format(time.RFC3339)})
	}
	var persisted []SessionTokens
	// the first token expired; the client does not know the hard end yet (an older credentials file)
	c, _ := NewWithSession(h.srv.URL, "rt-1", SessionTokens{AccessToken: "at-1", AccessTokenExpiry: time.Now().Add(-time.Minute)},
		func(s SessionTokens) { persisted = append(persisted, s) })
	if err := query(c); err != nil {
		t.Fatal(err)
	}
	if h.refreshes != 1 || len(persisted) != 1 || !persisted[0].SessionHardExpiry.Equal(hard) {
		t.Fatalf("one refresh, the hard end persisted, got refreshes=%d persisted=%+v", h.refreshes, persisted)
	}
	// a short expires_in is not stretched past the end
	if got := c.Tokens().AccessTokenExpiry; got.After(hard) {
		t.Fatalf("the token is planned no later than the hard end, got %v > %v", got, hard)
	}
	if !strings.HasSuffix(h.bearers[0], "at-2") {
		t.Fatalf("the refreshed token is sent, got %v", h.bearers)
	}
}
