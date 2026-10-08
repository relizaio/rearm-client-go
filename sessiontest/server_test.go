package sessiontest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	rearm "github.com/relizaio/rearm-client-go"
)

type grant struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
}

func refresh(t *testing.T, s *Server, rt string) grant {
	t.Helper()
	resp, err := http.PostForm(s.URL+rearm.TokenPath, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("refresh outcomes are answered 200, got %d", resp.StatusCode)
	}
	var g grant
	_ = json.NewDecoder(resp.Body).Decode(&g)
	return g
}

func gql(t *testing.T, s *Server, bearer string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.URL+rearm.ProgrammaticPath, strings.NewReader(`{"operationName":"Op","query":"query Op { x }"}`))
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// Each verdict answers as CliSessionService.verdict and refresh do.
func TestTheVerdictsFollowTheServer(t *testing.T) {
	s := NewServer(t, Options{})
	rt0, set := s.Login()
	if code, _ := gql(t, s, set.AccessToken); code != 200 {
		t.Fatalf("the delivered token is accepted, got %d", code)
	}
	// CURRENT: rotates
	g1 := refresh(t, s, rt0)
	if g1.Error != "" || g1.RefreshToken == "" || g1.RefreshToken == rt0 || g1.ExpiresIn != 3600 {
		t.Fatalf("CURRENT rotates, got %+v", g1)
	}
	// the delivered access token is bound to the retired hash: refused, though its exp is an hour away
	if code, body := gql(t, s, set.AccessToken); code != 401 || !strings.Contains(body, "invalid_token") {
		t.Fatalf("a token of a retired hash is refused with the JSON invalid_token, got %d %s", code, body)
	}
	if code, _ := gql(t, s, g1.AccessToken); code != 200 {
		t.Fatalf("the new token is accepted, got %d", code)
	}
	// PREVIOUS_IN_GRACE: rotates the current one again; the first refresher's tokens die
	s.Advance(time.Minute)
	g2 := refresh(t, s, rt0)
	if g2.Error != "" || g2.RefreshToken == g1.RefreshToken {
		t.Fatalf("PREVIOUS_IN_GRACE re-rotates, got %+v", g2)
	}
	if code, _ := gql(t, s, g1.AccessToken); code != 401 {
		t.Fatalf("the first refresher's access token is dead after the re-rotation, got %d", code)
	}
	if g := refresh(t, s, g1.RefreshToken); g.Error != "invalid_grant" {
		t.Fatalf("the first refresher's refresh token is UNKNOWN now, got %+v", g)
	}
	n := s.Counts()
	if n.Rotations != 2 || n.GraceRerotations != 1 || n.InvalidGrants != 1 || n.Revocations != 0 {
		t.Fatalf("unexpected counters %+v", n)
	}
	// UNKNOWN
	if g := refresh(t, s, "rt-nobody"); g.Error != "invalid_grant" {
		t.Fatalf("UNKNOWN is invalid_grant, got %+v", g)
	}
	// REUSED: the retired token after the grace (the window runs from the CURRENT rotation) revokes
	s.Advance(90 * time.Second)
	if g := refresh(t, s, rt0); g.Error != "invalid_grant" {
		t.Fatalf("REUSED is invalid_grant, got %+v", g)
	}
	if !s.Revoked() {
		t.Fatal("REUSED revokes the session")
	}
	if g := refresh(t, s, g2.RefreshToken); g.Error != "invalid_grant" {
		t.Fatalf("a revoked session refreshes no more, got %+v", g)
	}
	if code, _ := gql(t, s, g2.AccessToken); code != 401 {
		t.Fatalf("a revoked session's token is refused, got %d", code)
	}
	if n := s.Counts(); n.Reused != 1 || n.Revocations != 1 {
		t.Fatalf("one reuse, one revocation, got %+v", n)
	}
}

func TestAnExpiredTokenAndTheHTMLPage(t *testing.T) {
	s := NewServer(t, Options{HTML401: true, TokenTTL: 10 * time.Minute})
	_, set := s.Login()
	s.Advance(10 * time.Minute)
	code, body := gql(t, s, set.AccessToken)
	if code != 401 || !strings.Contains(body, "<html>") || strings.Contains(body, "invalid_token") {
		t.Fatalf("an expired token is refused with the HTML page, got %d %s", code, body)
	}
	if code, _ := gql(t, s, "garbage"); code != 401 {
		t.Fatal("a malformed bearer is refused")
	}
	if n := s.Counts(); n.Rejected401 != 2 || n.GraphQL != 0 {
		t.Fatalf("unexpected counters %+v", n)
	}
	if FP("sid-1", s.CurrentRefreshHash()) != strings.Split(set.AccessToken, ".")[1] {
		t.Fatal("the token carries fp of the current hash")
	}
}

func TestGraphQLAnswersAndRefuseAll(t *testing.T) {
	var seen string
	s := NewServer(t, Options{GraphQL: func(op string, _ json.RawMessage) json.RawMessage {
		seen = op
		return json.RawMessage(`{"data":{"x":1}}`)
	}})
	_, set := s.Login()
	if code, body := gql(t, s, set.AccessToken); code != 200 || body != `{"data":{"x":1}}` || seen != "Op" {
		t.Fatalf("the handler answers by operation name, got %d %s %q", code, body, seen)
	}
	s.RefuseAll(true)
	if code, _ := gql(t, s, set.AccessToken); code != 401 {
		t.Fatal("RefuseAll refuses")
	}
}

// Without a store, two clients on one login do what the field saw (S401-1 intake): the later
// refresher re-rotates inside the grace, and after it revokes the session.
func TestTwoClientsWithoutAStoreHurtEachOther(t *testing.T) {
	s := NewServer(t, Options{})
	rt, _ := s.Login()
	a, _ := rearm.NewWithSession(s.URL, rt, rearm.SessionTokens{}, nil)
	b, _ := rearm.NewWithSession(s.URL, rt, rearm.SessionTokens{}, nil)
	if _, err := rearm.Raw(t.Context(), a, "Op", "query Op { x }", nil); err != nil {
		t.Fatal(err)
	}
	s.Advance(3 * time.Minute)
	if _, err := rearm.Raw(t.Context(), b, "Op", "query Op { x }", nil); err == nil {
		t.Fatal("B refreshes with the retired token after the grace and must fail")
	}
	if n := s.Counts(); n.Reused != 1 || !s.Revoked() {
		t.Fatalf("the session is revoked, got %+v", n)
	}
}

func TestTheMemoryStoreLocksAndRecords(t *testing.T) {
	m := NewMemoryStore(rearm.SessionTokens{RefreshToken: "rt"})
	release, err := m.Lock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(rearm.SessionTokens{RefreshToken: "rt-2"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.Lock(ctx); err == nil {
		t.Fatal("a second Lock waits and gives up with its context")
	}
	release()
	release() // idempotent
	if h := m.Holds(); len(h) != 1 || len(h[0].Saves) != 1 {
		t.Fatalf("one hold with one save, got %+v", h)
	}
	m.Clear()
	if _, err := m.Load(); err != rearm.ErrNoSession {
		t.Fatalf("a cleared store has no session, got %v", err)
	}
}
