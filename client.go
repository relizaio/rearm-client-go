// Package rearm is the Go client for the ReARM GraphQL API. It is generated from the
// ReARM schema with genqlient (see generated.go) and shared by the ReARM CLI and the
// Terraform provider. Only API-key authentication is supported: browser sessions are
// out of scope.
package rearm

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
)

// Version is stamped by the build; consumers may override the user agent with WithUserAgent.
var Version = "dev"

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
}

// WithHTTPClient sets the underlying HTTP client (timeouts, proxies, TLS).
func WithHTTPClient(h *http.Client) Option { return func(o *options) { o.httpClient = h } }

// WithUserAgent sets the User-Agent header sent with every request.
func WithUserAgent(ua string) Option { return func(o *options) { o.userAgent = ua } }

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
	endpoint := strings.TrimRight(baseURL, "/") + "/graphql"
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(apiKeyID+":"+apiKey))
	hc := &http.Client{
		Timeout:   base.Timeout,
		Transport: &authTransport{next: transportOf(base), auth: auth, userAgent: o.userAgent},
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

type authTransport struct {
	next      http.RoundTripper
	auth      string
	userAgent string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", t.auth)
	r.Header.Set("User-Agent", t.userAgent)
	r.Header.Set("Accept", "application/json")
	return t.next.RoundTrip(r)
}
