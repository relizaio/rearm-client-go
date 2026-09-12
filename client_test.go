package rearm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// A server with the programmatic endpoint: no handshake, no cookies, key on every request.
func TestProgrammaticEndpointNeedsNoHandshake(t *testing.T) {
	var csrfCalls, calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/manual/v1/fetchCsrf":
			csrfCalls++
		case TokenPath:
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") || r.Method != http.MethodPost {
				t.Errorf("token endpoint must get the key as Basic on a POST")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"tok-1","token_type":"Bearer","expires_in":3600}`))
		case ProgrammaticPath:
			calls++
			if r.Header.Get("Authorization") != "Bearer tok-1" {
				t.Errorf("resource endpoint must get the exchanged bearer token, got %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("X-XSRF-TOKEN") != "" || r.Header.Get("Cookie") != "" {
				t.Errorf("programmatic endpoint must not receive csrf headers")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"__typename":"Query"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, "id", "secret")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Typename string `json:"__typename"`
	}
	if err := c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || csrfCalls != 0 || resp.Typename != "Query" {
		t.Fatalf("calls=%d csrf=%d typename=%q", calls, csrfCalls, resp.Typename)
	}
}

// An older server: 404 on the programmatic path -> fall back to /graphql with the handshake, and stay there.
func TestFallsBackToLegacyEndpointWithHandshake(t *testing.T) {
	var csrfCalls, legacyCalls, progCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case TokenPath, ProgrammaticPath:
			if r.URL.Path == ProgrammaticPath {
				progCalls++
			}
			w.WriteHeader(http.StatusNotFound)
		case "/api/manual/v1/fetchCsrf":
			csrfCalls++
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "sess1"})
			http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: "tok1"})
		case LegacyPath:
			legacyCalls++
			if r.Header.Get("X-XSRF-TOKEN") != "tok1" || !strings.Contains(r.Header.Get("Cookie"), "JSESSIONID=sess1") {
				t.Errorf("legacy endpoint must receive the csrf session, got %q / %q", r.Header.Get("X-XSRF-TOKEN"), r.Header.Get("Cookie"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"__typename":"Query"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, "id", "secret")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var resp struct {
			Typename string `json:"__typename"`
		}
		if err := c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp}); err != nil {
			t.Fatal(err)
		}
	}
	if progCalls != 1 || csrfCalls != 1 || legacyCalls != 2 {
		t.Fatalf("expected one 404 probe, one handshake and two legacy calls; got prog=%d csrf=%d legacy=%d", progCalls, csrfCalls, legacyCalls)
	}
}

// A server with the programmatic endpoint but no token endpoint keeps working with Basic.
func TestBasicWhenTokenEndpointIsAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case TokenPath:
			w.WriteHeader(http.StatusNotFound)
		case ProgrammaticPath:
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
				t.Errorf("expected Basic after a 404 from the token endpoint, got %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"__typename":"Query"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, "id", "secret")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Typename string `json:"__typename"`
	}
	if err := c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp}); err != nil {
		t.Fatal(err)
	}
}

// A server older than the programmatic chain answers the token endpoint and the programmatic
// endpoint with 401 from its browser security chain (often rewritten to a text page by the
// ingress). The client must read that as "absent" and finish on the legacy endpoint with Basic.
func TestOlderServerAnswering401FallsBackToLegacy(t *testing.T) {
	var legacyCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case TokenPath, ProgrammaticPath:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("Your request has returned an error with the code: 401 (Unauthorized)."))
		case "/api/manual/v1/fetchCsrf":
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "sess1"})
			http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: "tok1"})
		case LegacyPath:
			legacyCalls++
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
				t.Errorf("legacy endpoint must receive the key as Basic, got %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"__typename":"Query"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, "id", "secret")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var resp struct {
			Typename string `json:"__typename"`
		}
		if err := c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp}); err != nil {
			t.Fatal(err)
		}
	}
	if legacyCalls != 2 {
		t.Fatalf("expected two legacy calls, got %d", legacyCalls)
	}
}

// When the token endpoint itself refuses the key with an OAuth error body, that is the error;
// there is no fallback to guess around it.
func TestTokenEndpointRefusalIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case TokenPath:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"unknown key"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, "id", "secret")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct{}
	err = c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp})
	if err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Fatalf("expected the OAuth refusal, got %v", err)
	}
}

// A key-mode client presents the key as Basic on REST paths (the artifact download lives on the
// browser chain, which does not take the access token) and the bearer on GraphQL.
func TestKeyModeSendsBasicOnRestPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case TokenPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3600}`))
		case ProgrammaticPath:
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("graphql must carry the bearer, got %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"__typename":"Query"}}`))
		case "/api/programmatic/v1/artifact/abc/download":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
				t.Errorf("download must carry Basic, got %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Disposition", `attachment; filename="a.json"`)
			_, _ = w.Write([]byte("{}"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, "id", "secret")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct{}
	if err := c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp}); err != nil {
		t.Fatal(err)
	}
	body, cd, err := DownloadArtifact(context.Background(), c, "abc", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	if !strings.Contains(cd, "a.json") {
		t.Fatalf("expected the content disposition, got %q", cd)
	}
}
