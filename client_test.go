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
		case ProgrammaticPath:
			calls++
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
				t.Errorf("missing basic auth")
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
	var resp struct{ Typename string `json:"__typename"` }
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
		case ProgrammaticPath:
			progCalls++
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
		var resp struct{ Typename string `json:"__typename"` }
		if err := c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp}); err != nil {
			t.Fatal(err)
		}
	}
	if progCalls != 1 || csrfCalls != 1 || legacyCalls != 2 {
		t.Fatalf("expected one 404 probe, one handshake and two legacy calls; got prog=%d csrf=%d legacy=%d", progCalls, csrfCalls, legacyCalls)
	}
}
