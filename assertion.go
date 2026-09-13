package rearm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Assertion credentials (RFC 7523): the client presents an identity token from an external
// issuer, such as the one GitHub Actions issues to a job, and ReARM answers with the usual
// one-hour access token when an organization's trust rule admits that identity. No secret is
// involved on either side. The assertion is fetched fresh for every exchange, because such
// tokens live for minutes and the provider hands out a new one on request.

// JwtBearerGrant is the grant type of the assertion exchange.
const JwtBearerGrant = "urn:ietf:params:oauth:grant-type:jwt-bearer"

// AssertionSource returns a fresh identity token to exchange.
type AssertionSource func(ctx context.Context) (string, error)

// AssertionError is a refused exchange: the identity is not trusted, or is trusted by several
// organizations and clientID must name one. Unwrap with errors.As.
type AssertionError struct {
	Code        string
	Description string
}

func (e *AssertionError) Error() string { return "rearm: assertion " + e.Code + ": " + e.Description }

// NewWithAssertion builds a client that authenticates with identity tokens from source. clientID
// is the ReARM organization uuid and is needed only when several organizations trust the same
// identity; pass "" otherwise. There is no Basic fallback and no legacy endpoint: assertions are
// honoured on the programmatic endpoint only.
func NewWithAssertion(baseURL, clientID string, source AssertionSource, opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("rearm: base URL is required")
	}
	if source == nil {
		return nil, fmt.Errorf("rearm: an assertion source is required")
	}
	o := &options{userAgent: "rearm-client-go/" + Version}
	for _, opt := range opts {
		opt(o)
	}
	base := o.httpClient
	if base == nil {
		base = &http.Client{Timeout: 60 * time.Second}
	}
	root := normalizeRoot(baseURL)
	t := &authTransport{next: transportOf(base), userAgent: o.userAgent, tokenURL: root + TokenPath,
		exchange: true, assertion: source, clientID: clientID}
	hc := &http.Client{Timeout: base.Timeout, Transport: t}
	endpoint := root + ProgrammaticPath
	return &Client{Client: NewGraphQLClient(endpoint, hc), endpoint: endpoint, http: hc, transport: t, root: root}, nil
}

// Identity is what the last assertion exchange established: the key the client acts as and the
// repository the identity stands for.
type Identity struct {
	APIKeyID   string
	APIKeyUUID string
	Org        string
	Repository string
}

// Identity returns the identity of the last exchange (zero values before the first call or on
// other client kinds).
func (c *Client) Identity() Identity {
	t := c.transport
	if t == nil {
		return Identity{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.identity
}

// exchangeAssertion fetches a fresh assertion and trades it for an access token.
func (t *authTransport) exchangeAssertion(ctx context.Context) error {
	assertion, err := t.assertion(ctx)
	if err != nil {
		return fmt.Errorf("rearm: assertion: %w", err)
	}
	form := url.Values{"grant_type": {JwtBearerGrant}, "assertion": {assertion}}
	if t.clientID != "" {
		form.Set("client_id", t.clientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("rearm: assertion exchange: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		APIKeyID    string `json:"api_key_id"`
		APIKeyUUID  string `json:"api_key_uuid"`
		Org         string `json:"org"`
		Identity    string `json:"identity"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &tok)
	// outcomes arrive as 200 with an error member (the ingress in front of ReARM rewrites 4xx bodies)
	if tok.Error != "" || resp.StatusCode != http.StatusOK || tok.AccessToken == "" {
		if tok.Error == "" {
			return fmt.Errorf("rearm: assertion exchange failed with status %d", resp.StatusCode)
		}
		return &AssertionError{Code: tok.Error, Description: tok.Description}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bearer = tok.AccessToken
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 2*time.Minute {
		ttl = 3 * time.Minute
	}
	t.bearerExp = time.Now().Add(ttl - time.Minute)
	t.identity = Identity{APIKeyID: tok.APIKeyID, APIKeyUUID: tok.APIKeyUUID, Org: tok.Org, Repository: tok.Identity}
	return nil
}

// GitHubActionsAssertion reads the identity token GitHub issues to a workflow job, requested
// with audience set to the ReARM URL. The job needs `permissions: id-token: write`; the two
// ACTIONS_ID_TOKEN_REQUEST_* variables are set by the runner. hc may be nil.
func GitHubActionsAssertion(audience string, hc *http.Client) AssertionSource {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	aud := strings.TrimRight(audience, "/")
	return func(ctx context.Context) (string, error) {
		reqURL := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
		reqToken := os.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
		if reqURL == "" || reqToken == "" {
			return "", fmt.Errorf("not running in GitHub Actions with id-token: write (ACTIONS_ID_TOKEN_REQUEST_URL/TOKEN are unset)")
		}
		sep := "?"
		if strings.Contains(reqURL, "?") {
			sep = "&"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL+sep+"audience="+url.QueryEscape(aud), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "bearer "+reqToken)
		req.Header.Set("Accept", "application/json; api-version=2.0")
		resp, err := hc.Do(req)
		if err != nil {
			return "", fmt.Errorf("github identity token: %w", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("github identity token request failed with status %d", resp.StatusCode)
		}
		var out struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || out.Value == "" {
			return "", fmt.Errorf("github identity token: malformed response")
		}
		return out.Value, nil
	}
}
