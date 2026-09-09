// Package rearm is the Go client for the ReARM GraphQL API. It is generated from the
// ReARM schema with genqlient (see generated.go) and shared by the ReARM CLI and the
// Terraform provider. Only API-key authentication is supported: browser sessions are
// out of scope.
package rearm

import (
	"bytes"
	"context"
	"encoding/base64"
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

// Client wraps a genqlient graphql.Client with ReARM API-key auth.
type Client struct {
	graphql.Client
	endpoint string
}

// Option customises a Client.
type Option func(*options)

type options struct {
	httpClient *http.Client
	userAgent  string
	legacy     bool
}

// WithHTTPClient sets the underlying HTTP client (timeouts, proxies, TLS).
func WithHTTPClient(h *http.Client) Option { return func(o *options) { o.httpClient = h } }

// WithUserAgent sets the User-Agent header sent with every request.
func WithUserAgent(ua string) Option { return func(o *options) { o.userAgent = ua } }

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
		csrfURL: root + "/api/manual/v1/fetchCsrf", legacyURL: root + LegacyPath, legacy: o.legacy}
	hc := &http.Client{Timeout: base.Timeout, Transport: t}
	endpoint := root + ProgrammaticPath
	if o.legacy {
		endpoint = root + LegacyPath
	}
	return &Client{Client: graphql.NewClient(endpoint, hc), endpoint: endpoint}, nil
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

	mu      sync.Mutex
	legacy  bool
	session *csrfSession
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
		resp, err := t.send(req, body, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusNotFound {
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
func (t *authTransport) setLegacy()     { t.mu.Lock(); defer t.mu.Unlock(); t.legacy = true }

func (t *authTransport) send(req *http.Request, body []byte, sess *csrfSession) (*http.Response, error) {
	r := req.Clone(req.Context())
	if t.isLegacy() && r.URL.Path != LegacyPath {
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
	r.Header.Set("Authorization", t.auth)
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
