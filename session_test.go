package rearm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"

	"github.com/Khan/genqlient/graphql"
	"strings"
	"testing"
	"time"
)

// A session client refreshes on demand, reports the new tokens, sends a bearer on the programmatic
// endpoint, and never touches the legacy path.
func TestSessionClientRefreshesAndPersists(t *testing.T) {
	var refreshes, legacyHits int
	var sawBearer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case TokenPath:
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "rt-1" {
				w.WriteHeader(400)
				return
			}
			refreshes++
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + strings.Repeat("x", refreshes), "token_type": "Bearer", "expires_in": 3600, "session_expires_at": "2026-10-12T00:00:00Z"})
		case ProgrammaticPath:
			sawBearer = r.Header.Get("Authorization")
			_, _ = io.WriteString(w, `{"data":{"getLatestReleaseProgrammatic":{"version":"1.2.3","artifacts":null}}}`)
		case LegacyPath:
			legacyHits++
			w.WriteHeader(404)
		case RevokePath:
			_ = r.ParseForm()
			if r.Form.Get("token") != "rt-1" {
				w.WriteHeader(400)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	var persisted []SessionTokens
	c, err := NewWithSession(srv.URL, "rt-1", SessionTokens{}, func(s SessionTokens) { persisted = append(persisted, s) })
	if err != nil {
		t.Fatal(err)
	}
	data, err := Raw(context.Background(), c, "GetLatestReleaseProgrammatic", GetLatestReleaseProgrammatic_Operation, map[string]any{"GetLatestReleaseInput": map[string]any{"component": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"getLatestReleaseProgrammatic":{"version":"1.2.3","artifacts":null}}` {
		t.Fatalf("raw data must be untouched, got %s", data)
	}
	if !strings.HasPrefix(sawBearer, "Bearer at-") {
		t.Fatalf("expected a bearer on the programmatic endpoint, got %q", sawBearer)
	}
	if refreshes != 1 || len(persisted) != 1 || persisted[0].AccessToken == "" || persisted[0].SessionExpiry.IsZero() {
		t.Fatalf("expected one refresh reported through persist, got refreshes=%d persisted=%v", refreshes, persisted)
	}
	// cached token is reused within its lifetime
	if _, err := Raw(context.Background(), c, "GetLatestReleaseProgrammatic", GetLatestReleaseProgrammatic_Operation, nil); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 || legacyHits != 0 {
		t.Fatalf("no second refresh and no legacy fallback expected, got refreshes=%d legacy=%d", refreshes, legacyHits)
	}
	if err := c.Revoke(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRefusedRefreshIsASessionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// outcomes come back as 200 with the error member (ingress-proof shape)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"unknown, expired or revoked refresh token; log in again"}`)
	}))
	defer srv.Close()
	c, _ := NewWithSession(srv.URL, "rt-dead", SessionTokens{}, nil)
	_, err := Raw(context.Background(), c, "Op", "query Op { x }", nil)
	var se *SessionError // wrapped by net/http in a *url.Error, so callers unwrap with errors.As
	if !errors.As(err, &se) || se.Code != "invalid_grant" {
		t.Fatalf("expected a SessionError invalid_grant, got %v", err)
	}
}

func TestDeviceLoginHelpers(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case DeviceCodePath:
			if r.Form.Get("requested_from") != "my-host" {
				w.WriteHeader(400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "dc", "user_code": "WDJB-MJHT", "verification_uri": "https://x/cli-login", "verification_uri_complete": "https://x/cli-login?user_code=WDJB-MJHT", "expires_in": 600, "interval": 5})
		case TokenPath:
			if r.Form.Get("grant_type") != DeviceCodeGrant || r.Form.Get("device_code") != "dc" {
				w.WriteHeader(400)
				return
			}
			polls++
			if polls == 1 {
				_, _ = io.WriteString(w, `{"error":"authorization_pending","error_description":"not yet"}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "rt", "api_key_id": "USER__u__ord__k", "api_key_uuid": "k", "org": "o", "session": "s", "session_expires_at": "2026-10-12T00:00:00Z"})
		}
	}))
	defer srv.Close()
	d, err := StartDeviceLogin(context.Background(), nil, srv.URL, "my-host")
	if err != nil || d.UserCode != "WDJB-MJHT" || d.Interval != 5 {
		t.Fatalf("start: %v %+v", err, d)
	}
	p1, err := PollDeviceLogin(context.Background(), nil, srv.URL, d.DeviceCode)
	if err != nil || p1.Status != DevicePending {
		t.Fatalf("first poll should be pending, got %v %+v", err, p1)
	}
	p2, err := PollDeviceLogin(context.Background(), nil, srv.URL, d.DeviceCode)
	if err != nil || p2.Status != DeviceDelivered || p2.RefreshToken != "rt" || p2.APIKeyID != "USER__u__ord__k" || p2.Tokens.SessionExpiry.IsZero() {
		t.Fatalf("second poll should deliver, got %v %+v", err, p2)
	}
	if time.Until(p2.Tokens.AccessTokenExpiry) < 50*time.Minute {
		t.Fatalf("access token expiry should be about an hour out")
	}
}

func TestMultipartShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == TokenPath {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Apollo-Require-Preflight") != "true" {
			t.Errorf("missing preflight header")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("not multipart: %v", err)
		}
		if !strings.Contains(r.FormValue("operations"), `"operationName":"AddArtifactProgrammatic"`) || r.FormValue("map") != `{"0":["variables.artifactInput.file"]}` {
			t.Errorf("unexpected operations/map: %s | %s", r.FormValue("operations"), r.FormValue("map"))
		}
		f, hdr, err := r.FormFile("0")
		if err != nil || hdr.Filename != "bom.json" {
			t.Errorf("file part: %v %v", err, hdr)
		} else {
			b, _ := io.ReadAll(f)
			if string(b) != `{"bomFormat":"CycloneDX"}` {
				t.Errorf("file content: %s", b)
			}
		}
		_, _ = io.WriteString(w, `{"data":{"addArtifactProgrammatic":{"uuid":"a"}}}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "id", "secret", WithoutTokenExchange())
	data, err := UploadMultipart(context.Background(), c, "AddArtifactProgrammatic", AddArtifactProgrammatic_Operation,
		map[string]any{"artifactInput": map[string]any{"file": nil}},
		[]FilePart{{Filename: "bom.json", Content: strings.NewReader(`{"bomFormat":"CycloneDX"}`), VariablePath: "variables.artifactInput.file"}})
	if err != nil || string(data) != `{"addArtifactProgrammatic":{"uuid":"a"}}` {
		t.Fatalf("multipart: %v %s", err, data)
	}
}

func TestRawSurfacesGraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == TokenPath {
			w.WriteHeader(404)
			return
		}
		_, _ = io.WriteString(w, `{"errors":[{"message":"Not authorized","extensions":{"errorType":"PERMISSION_DENIED"}}],"data":null}`)
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "id", "secret", WithoutTokenExchange())
	_, err := Raw(context.Background(), c, "Op", "query Op { x }", nil)
	ge, ok := err.(GraphQLErrors)
	if !ok || len(ge) != 1 || ge[0].Message != "Not authorized" {
		t.Fatalf("expected GraphQLErrors, got %v", err)
	}
}

// The server rotates the refresh token on every refresh: the client must present the new one next
// time and hand it to the persist callback, since the one it logged in with is retired.
func TestRotatedRefreshTokenIsUsedAndPersisted(t *testing.T) {
	var presented []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case TokenPath:
			_ = r.ParseForm()
			presented = append(presented, r.Form.Get("refresh_token"))
			next := "rt-" + strconv.Itoa(len(presented)+1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"at-` + strconv.Itoa(len(presented)) + `","token_type":"Bearer","expires_in":3600,"refresh_token":"` + next + `","session_expires_at":"2026-10-12T00:00:00Z"}`))
		case ProgrammaticPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"__typename":"Query"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	var persisted []SessionTokens
	c, err := NewWithSession(srv.URL, "rt-1", SessionTokens{}, func(s SessionTokens) { persisted = append(persisted, s) })
	if err != nil {
		t.Fatal(err)
	}
	var resp struct{}
	if err := c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp}); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].RefreshToken != "rt-2" {
		t.Fatalf("expected the rotated token rt-2 through persist, got %+v", persisted)
	}
	// force a second refresh: it must present the rotated token
	c.transport.mu.Lock()
	c.transport.bearerExp = time.Now().Add(-time.Minute)
	c.transport.mu.Unlock()
	if err := c.MakeRequest(context.Background(), &graphql.Request{Query: "{__typename}"}, &graphql.Response{Data: &resp}); err != nil {
		t.Fatal(err)
	}
	if len(presented) != 2 || presented[0] != "rt-1" || presented[1] != "rt-2" {
		t.Fatalf("expected rt-1 then rt-2 presented, got %v", presented)
	}
	if c.Tokens().RefreshToken != "rt-3" {
		t.Fatalf("expected the current refresh token to be rt-3, got %q", c.Tokens().RefreshToken)
	}
}
