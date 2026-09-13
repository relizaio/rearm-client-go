package rearm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
)

func TestAssertionClientExchangesAFreshTokenEachTime(t *testing.T) {
	var exchanges int
	var presented []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case TokenPath:
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != JwtBearerGrant || r.Form.Get("client_id") != "org-1" {
				t.Errorf("unexpected form %v", r.Form)
			}
			presented = append(presented, r.Form.Get("assertion"))
			exchanges++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer","expires_in":3600,"api_key_id":"FEDERATED__o__ord__GITHUB_ACTIONS:relizaio/x","api_key_uuid":"k","org":"org-1","identity":"relizaio/x"}`))
		case ProgrammaticPath:
			if r.Header.Get("Authorization") != "Bearer at" {
				t.Errorf("expected the bearer, got %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"__typename":"Query"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	n := 0
	source := func(ctx context.Context) (string, error) { n++; return "id-token-" + string(rune('0'+n)), nil }
	c, err := NewWithAssertion(srv.URL, "org-1", source)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct{}
	if err := c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp}); err != nil {
		t.Fatal(err)
	}
	if c.Identity().Repository != "relizaio/x" || c.Identity().Org != "org-1" {
		t.Fatalf("identity not recorded: %+v", c.Identity())
	}
	// force a second exchange: a fresh assertion must be fetched
	c.transport.mu.Lock()
	c.transport.bearerExp = time.Now().Add(-time.Minute)
	c.transport.mu.Unlock()
	if err := c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp}); err != nil {
		t.Fatal(err)
	}
	if exchanges != 2 || presented[0] == presented[1] {
		t.Fatalf("expected two exchanges with distinct assertions, got %d %v", exchanges, presented)
	}
}

func TestRefusedAssertionIsAnAssertionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"no trust rule matches this identity"}`))
	}))
	defer srv.Close()
	c, err := NewWithAssertion(srv.URL, "", func(ctx context.Context) (string, error) { return "x", nil })
	if err != nil {
		t.Fatal(err)
	}
	var resp struct{}
	err = c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp})
	var ae *AssertionError
	if !errors.As(err, &ae) || ae.Code != "invalid_grant" {
		t.Fatalf("expected an AssertionError, got %v", err)
	}
}

func TestGitHubActionsAssertionReadsTheRunnerEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "bearer runner-token" || r.URL.Query().Get("audience") != "https://rearm.example.com" {
			t.Errorf("unexpected request %s %v", r.Header.Get("Authorization"), r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":"github-id-token"}`))
	}))
	defer srv.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"/token?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "runner-token")
	tok, err := GitHubActionsAssertion("https://rearm.example.com/", nil)(context.Background())
	if err != nil || tok != "github-id-token" {
		t.Fatalf("expected the token, got %q %v", tok, err)
	}
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	if _, err := GitHubActionsAssertion("https://rearm.example.com", nil)(context.Background()); err == nil || !strings.Contains(err.Error(), "id-token") {
		t.Fatalf("expected a clear error outside GitHub Actions, got %v", err)
	}
}
