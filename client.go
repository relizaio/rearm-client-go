// Package rearm is the Go client for the ReARM GraphQL API. It is generated from the
// ReARM schema with genqlient (see generated.go) and shared by the ReARM CLI and the
// Terraform provider. Only API-key authentication is supported: browser sessions are
// out of scope.
package rearm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Khan/genqlient/graphql"
)

// Version is stamped by the build; consumers may override the user agent with WithUserAgent.
var Version = "dev"

// ProgrammaticPath is the API-key GraphQL endpoint: stateless, no CSRF token, only the
// operations declared in the programmatic schema. Older servers do not have it; see LegacyPath.
const ProgrammaticPath = "/api/programmatic/graphql"

// LegacyPath is the original endpoint shared with the browser UI. Reaching it with an API key
// requires the CSRF session handshake, which the client performs when it falls back here.
const LegacyPath = "/graphql"

// TokenPath is the OAuth 2.0 token endpoint where an API key is exchanged for a short-lived
// bearer access token (grant_type=client_credentials, key id and secret as HTTP Basic). The
// client prefers this over presenting the key secret on every request; servers without it
// (404) are used with Basic directly.
const TokenPath = "/api/programmatic/token"

// Client wraps a genqlient graphql.Client with ReARM API-key auth.
type Client struct {
	graphql.Client
	endpoint  string
	http      *http.Client
	transport *authTransport
	root      string
}

// NewGraphQLClient exposes the underlying constructor for advanced callers.
func NewGraphQLClient(endpoint string, hc *http.Client) graphql.Client {
	return graphql.NewClient(endpoint, hc)
}

// Option customises a Client.
type Option func(*options)

type options struct {
	httpClient *http.Client
	userAgent  string
	legacy     bool
	noExchange bool
}

// WithHTTPClient sets the underlying HTTP client (timeouts, proxies, TLS).
func WithHTTPClient(h *http.Client) Option { return func(o *options) { o.httpClient = h } }

// WithUserAgent sets the User-Agent header sent with every request.
func WithUserAgent(ua string) Option { return func(o *options) { o.userAgent = ua } }

// WithoutTokenExchange keeps presenting the API key secret as Basic on every request instead of
// exchanging it for an access token. Only for pinning behaviour against an old server; the
// exchange is preferred and the direct form is deprecated server-side.
func WithoutTokenExchange() Option { return func(o *options) { o.noExchange = true } }

// WithLegacyEndpoint forces the shared /graphql endpoint (with the CSRF handshake) instead of
// the programmatic endpoint. Only needed to pin behaviour against an old server; by default
// the client tries the programmatic endpoint and falls back on its own when it is absent.
func WithLegacyEndpoint() Option { return func(o *options) { o.legacy = true } }

// New returns a client for the ReARM instance at baseURL (scheme and host, e.g.
// https://app.rearmhq.com) authenticating with an API key pair.
func New(baseURL, apiKeyID, apiKey string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("rearm: base URL is required")
	}
	if apiKeyID == "" || apiKey == "" {
		return nil, fmt.Errorf("rearm: API key id and secret are required")
	}
	o := &options{userAgent: "rearm-client-go/" + Version}
	for _, opt := range opts {
		opt(o)
	}
	base := o.httpClient
	if base == nil {
		base = &http.Client{Timeout: 60 * time.Second}
	}
	root := strings.TrimRight(baseURL, "/")
	if !strings.HasPrefix(root, "http://") && !strings.HasPrefix(root, "https://") {
		root = "https://" + root
	}
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(apiKeyID+":"+apiKey))
	t := &authTransport{next: transportOf(base), auth: auth, userAgent: o.userAgent,
		csrfURL: root + "/api/manual/v1/fetchCsrf", legacyURL: root + LegacyPath, legacy: o.legacy,
		tokenURL: root + TokenPath, exchange: !o.legacy && !o.noExchange}
	hc := &http.Client{Timeout: base.Timeout, Transport: t}
	endpoint := root + ProgrammaticPath
	if o.legacy {
		endpoint = root + LegacyPath
	}
	return &Client{Client: graphql.NewClient(endpoint, hc), endpoint: endpoint, http: hc, transport: t, root: root}, nil
}

// Endpoint is the GraphQL URL the client talks to.
func (c *Client) Endpoint() string { return c.endpoint }

func transportOf(h *http.Client) http.RoundTripper {
	if h.Transport != nil {
		return h.Transport
	}
	return http.DefaultTransport
}

// authTransport adds the API key. On the programmatic endpoint that is all a request needs.
// When the server is older and has no programmatic endpoint (404), or when the legacy
// endpoint is forced, the transport switches to /graphql and performs the CSRF session
// handshake that endpoint requires even for key-authenticated calls: a session cookie plus
// an XSRF token from fetchCsrf, sent back as Cookie and X-XSRF-TOKEN (the same handshake the
// ReARM CLI performs). The session is fetched lazily and refreshed once on 401 or 403.
type authTransport struct {
	next      http.RoundTripper
	auth      string
	userAgent string
	csrfURL   string
	legacyURL string
	tokenURL  string

	mu        sync.Mutex
	legacy    bool
	exchange  bool      // try the token endpoint; cleared for good when the server has none
	bearer    string    // current access token
	bearerExp time.Time // when to fetch a new one (a minute before the server's expiry)
	session   *csrfSession
	// browser-login session mode: no Basic credential, tokens come from the refresh token
	refreshToken string
	revokeURL    string
	sessionExp   time.Time
	persist      func(SessionTokens)
	// assertion mode: no Basic credential, tokens come from a fresh identity token each time
	assertion AssertionSource
	clientID  string
	identity  Identity
}

type csrfSession struct {
	jsessionID string
	xsrfToken  string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// GraphQL requests carry a body; keep a copy so the request can be replayed after a refresh.
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		body = b
	}
	if !t.isLegacy() {
		if err := t.ensureToken(req.Context()); err != nil {
			return nil, err
		}
		resp, err := t.send(req, body, nil)
		if err != nil {
			return nil, err
		}
		if !t.programmaticAbsent(resp.StatusCode) {
			return resp, nil
		}
		// no programmatic endpoint on this server: fall back to /graphql with the handshake, for good
		_ = resp.Body.Close()
		t.setLegacy()
	}
	sess, err := t.sessionFor(req.Context(), false)
	if err != nil {
		return nil, err
	}
	resp, err := t.send(req, body, sess)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_ = resp.Body.Close()
		sess, err = t.sessionFor(req.Context(), true)
		if err != nil {
			return nil, err
		}
		return t.send(req, body, sess)
	}
	return resp, nil
}

func (t *authTransport) isLegacy() bool { t.mu.Lock(); defer t.mu.Unlock(); return t.legacy }

// programmaticAbsent reads a first answer from the programmatic endpoint as "this server does not
// have it": a 404, or a 401/403, and in both cases only while the request went out as a Basic
// credential. Servers older than the programmatic chain answer any unknown path from their browser
// security chain with 401 or 404, and neither status is a verdict on the key while the key was
// presented as Basic; if the key really is wrong the legacy endpoint refuses it too and that error
// is the one returned.
//
// A token changes the reading of both. This server minted it, so its programmatic chain exists and
// a later 404 is a routing problem -- an edge rule, a rewrite, a missing route -- not an old server.
// Downgrading then would replay the call on /graphql, which authenticates by CSRF handshake and
// carries no token at all, and the routing fault would surface as an authorization error instead.
// So a refused or missing answer to a token-bearing request is returned as it stands.
func (t *authTransport) programmaticAbsent(status int) bool {
	switch status {
	case http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden:
		return t.currentBearer() == "" && t.refreshToken == "" && t.assertion == nil
	}
	return false
}

func (t *authTransport) currentBearer() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.bearer
}

// ensureToken exchanges the API key for an access token when the client has none or it is
// about to expire. A 404 from the token endpoint means an older server: the exchange is
// switched off and the key is presented as Basic from then on.
func (t *authTransport) ensureToken(ctx context.Context) error {
	t.mu.Lock()
	need := t.exchange && (t.bearer == "" || time.Now().After(t.bearerExp))
	t.mu.Unlock()
	if !need {
		return nil
	}
	if t.refreshToken != "" {
		return t.refreshAccessToken(ctx)
	}
	if t.assertion != nil {
		return t.exchangeAssertion(ctx)
	}
	form := strings.NewReader("grant_type=client_credentials")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenURL, form)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", t.auth)
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("rearm: token exchange: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	t.mu.Lock()
	defer t.mu.Unlock()
	switch resp.StatusCode {
	case http.StatusOK:
		var tok struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
			ExpiresIn   int64  `json:"expires_in"`
		}
		if err := json.Unmarshal(raw, &tok); err != nil || tok.AccessToken == "" {
			return fmt.Errorf("rearm: token exchange: malformed token response")
		}
		t.bearer = tok.AccessToken
		ttl := time.Duration(tok.ExpiresIn) * time.Second
		if ttl <= 2*time.Minute {
			ttl = 3 * time.Minute
		}
		t.bearerExp = time.Now().Add(ttl - time.Minute)
		return nil
	case http.StatusNotFound:
		t.exchange = false // older server without the token endpoint: keep using Basic
		return nil
	default:
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Error != "" {
			// the token endpoint itself refused the key (RFC 6749 error body)
			return fmt.Errorf("rearm: token exchange failed: %s: %s", e.Error, e.Description)
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			// No OAuth error body: a server older than the token endpoint answers the unknown path
			// from its browser security chain with 401, and some ingresses replace 4xx bodies with a
			// text page. Neither is a verdict on the key, so present it as Basic; the endpoint
			// fallback in RoundTrip settles the rest, and a wrong key still fails there.
			t.exchange = false
			return nil
		}
		return fmt.Errorf("rearm: token exchange failed with status %d", resp.StatusCode)
	}
}
func (t *authTransport) setLegacy() { t.mu.Lock(); defer t.mu.Unlock(); t.legacy = true }

func (t *authTransport) send(req *http.Request, body []byte, sess *csrfSession) (*http.Response, error) {
	r := req.Clone(req.Context())
	if t.isLegacy() && strings.HasSuffix(r.URL.Path, ProgrammaticPath) {
		u, err := url.Parse(t.legacyURL)
		if err != nil {
			return nil, err
		}
		r.URL = u
		r.Host = u.Host
	}
	if body != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	}
	// The access token is honoured on the programmatic GraphQL endpoint only; REST paths such as
	// the artifact download live on the browser chain, which takes the key as Basic. A session
	// client has no Basic credential and sends its bearer everywhere.
	graphQL := strings.HasSuffix(r.URL.Path, ProgrammaticPath) || strings.HasSuffix(r.URL.Path, LegacyPath)
	if b := t.currentBearer(); b != "" && (t.refreshToken != "" || t.assertion != nil || (graphQL && !t.isLegacy())) {
		r.Header.Set("Authorization", "Bearer "+b)
	} else if t.auth != "" {
		r.Header.Set("Authorization", t.auth)
	}
	r.Header.Set("User-Agent", t.userAgent)
	r.Header.Set("Accept", "application/json")
	if sess != nil {
		r.Header.Set("X-XSRF-TOKEN", sess.xsrfToken)
		r.Header.Set("Cookie", "JSESSIONID="+sess.jsessionID+"; XSRF-TOKEN="+sess.xsrfToken)
	}
	return t.next.RoundTrip(r)
}

// sessionFor returns the cached CSRF session, fetching it on first use or when refresh is set.
func (t *authTransport) sessionFor(ctx context.Context, refresh bool) (*csrfSession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.session != nil && !refresh {
		return t.session, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.csrfURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("rearm: csrf handshake: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	sess := &csrfSession{}
	for _, c := range resp.Cookies() {
		switch c.Name {
		case "JSESSIONID":
			sess.jsessionID = c.Value
		case "XSRF-TOKEN":
			sess.xsrfToken = c.Value
		}
	}
	if sess.xsrfToken == "" {
		return nil, fmt.Errorf("rearm: csrf handshake: no XSRF-TOKEN cookie from %s (status %d)", t.csrfURL, resp.StatusCode)
	}
	t.session = sess
	return sess, nil
}
