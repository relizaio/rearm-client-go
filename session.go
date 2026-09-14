package rearm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CLI browser-login sessions and the device authorization endpoints behind `rearm login`.
//
// A session is JWT-only: the server hands out an opaque refresh token once (stored by the
// caller, never a key secret) and the client trades it for one-hour access tokens on demand.
// The server slides the session 30 days on each refresh, capped at 90 days after approval.
// The interactive part (printing the code, opening the browser, polling policy) stays with the
// caller; StartDeviceLogin and PollDeviceLogin are the two plain HTTP calls it needs.

const (
	DeviceCodePath  = "/api/programmatic/device/code"
	RevokePath      = "/api/programmatic/revoke"
	DeviceCodeGrant = "urn:ietf:params:oauth:grant-type:device_code"
)

// SessionTokens is what a session-mode client caches and hands back through the persist callback.
type SessionTokens struct {
	AccessToken       string
	AccessTokenExpiry time.Time
	// RefreshToken is set when the server rotated the refresh token on a refresh: the caller must
	// persist it before anything else, the token it logged in with is retired (a short grace window
	// covers a crash between receiving and persisting).
	RefreshToken string
	// SessionExpiry is when the refresh token stops working; it moves forward on each refresh.
	SessionExpiry time.Time
}

// NewWithSession builds a client that acts as the key behind a browser-login session. tokens may
// carry a cached access token; persist (optional) receives every new token set so the caller can
// store it. There is no Basic fallback and no legacy endpoint: session tokens are honoured on the
// programmatic endpoint only.
func NewWithSession(baseURL, refreshToken string, tokens SessionTokens, persist func(SessionTokens), opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("rearm: base URL is required")
	}
	if refreshToken == "" {
		return nil, fmt.Errorf("rearm: refresh token is required for a session client")
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
		revokeURL: root + RevokePath, exchange: true, refreshToken: refreshToken, persist: persist,
		bearer: tokens.AccessToken, sessionExp: tokens.SessionExpiry}
	if tokens.AccessToken != "" && !tokens.AccessTokenExpiry.IsZero() {
		t.bearerExp = tokens.AccessTokenExpiry.Add(-time.Minute)
	}
	hc := &http.Client{Timeout: base.Timeout, Transport: t}
	endpoint := root + ProgrammaticPath
	return &Client{Client: NewGraphQLClient(endpoint, hc), endpoint: endpoint, http: hc, transport: t, root: root}, nil
}

func normalizeRoot(baseURL string) string {
	root := strings.TrimRight(baseURL, "/")
	if !strings.HasPrefix(root, "http://") && !strings.HasPrefix(root, "https://") {
		root = "https://" + root
	}
	return root
}

// refreshAccessToken trades the refresh token for a new access token and reports the tokens.
func (t *authTransport) refreshAccessToken(ctx context.Context) error {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t.refreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("rearm: session refresh: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tok struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int64  `json:"expires_in"`
		RefreshToken     string `json:"refresh_token"`
		SessionExpiresAt string `json:"session_expires_at"`
		Error            string `json:"error"`
		Description      string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &tok)
	// outcomes arrive as 200 with an error member (the ingress in front of ReARM rewrites 4xx bodies)
	if tok.Error != "" || resp.StatusCode != http.StatusOK || tok.AccessToken == "" {
		if tok.Error == "" {
			return fmt.Errorf("rearm: session refresh failed with status %d", resp.StatusCode)
		}
		return &SessionError{Code: tok.Error, Description: tok.Description}
	}
	t.mu.Lock()
	t.bearer = tok.AccessToken
	ttl := time.Duration(tok.ExpiresIn) * time.Second
	if ttl <= 2*time.Minute {
		ttl = 3 * time.Minute
	}
	t.bearerExp = time.Now().Add(ttl - time.Minute)
	if se, err := time.Parse(time.RFC3339, tok.SessionExpiresAt); err == nil {
		t.sessionExp = se
	}
	rotated := ""
	if tok.RefreshToken != "" && tok.RefreshToken != t.refreshToken {
		// rotation: from now on only the new token refreshes; the persist callback carries it to disk
		t.refreshToken = tok.RefreshToken
		rotated = tok.RefreshToken
	}
	snapshot := SessionTokens{AccessToken: t.bearer, AccessTokenExpiry: t.bearerExp.Add(time.Minute), SessionExpiry: t.sessionExp, RefreshToken: rotated}
	persist := t.persist
	t.mu.Unlock()
	if persist != nil {
		persist(snapshot)
	}
	return nil
}

// SessionError is a refused refresh: the session is gone, log in again. It reaches callers wrapped
// in the transport error, so test for it with errors.As.
type SessionError struct {
	Code        string
	Description string
}

func (e *SessionError) Error() string { return "rearm: session " + e.Code + ": " + e.Description }

// Tokens returns the session client's current token set (zero values on a key-based client).
func (c *Client) Tokens() SessionTokens {
	t := c.transport
	if t == nil {
		return SessionTokens{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.bearer == "" {
		return SessionTokens{SessionExpiry: t.sessionExp}
	}
	return SessionTokens{AccessToken: t.bearer, AccessTokenExpiry: t.bearerExp.Add(time.Minute), SessionExpiry: t.sessionExp, RefreshToken: t.refreshToken}
}

// Revoke ends the browser-login session on the server (RFC 7009). Always succeeds from the
// server's point of view; a transport failure is returned so the caller can warn.
func (c *Client) Revoke(ctx context.Context) error {
	t := c.transport
	if t == nil || t.refreshToken == "" {
		return fmt.Errorf("rearm: not a session client")
	}
	form := url.Values{"token": {t.refreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("rearm: revoke: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

// DeviceAuthorization is the server's answer to StartDeviceLogin (RFC 8628 §3.2).
type DeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

// DeviceDetails is what the CLI reports about the device asking to log in. The approving user
// sees it next to the address the server observed, labelled as reported by the requester, so
// they can tell whether the request is the one they started. All fields are optional.
type DeviceDetails struct {
	Hostname string
	// OS such as "linux/amd64"
	OS string
	// TimeZone as the local UTC offset and abbreviation, e.g. "UTC+02:00 CEST"
	TimeZone string
	// Client is the calling program and version, e.g. "rearm-cli 26.09.1"
	Client string
}

// StartDeviceLogin asks ReARM for a device code; requestedFrom is shown to the approving user
// (typically the host name). No credentials are involved.
func StartDeviceLogin(ctx context.Context, hc *http.Client, baseURL, requestedFrom string) (*DeviceAuthorization, error) {
	return StartDeviceLoginWithDetails(ctx, hc, baseURL, DeviceDetails{Hostname: requestedFrom})
}

// StartDeviceLoginWithDetails is StartDeviceLogin with everything the CLI can report about the device.
func StartDeviceLoginWithDetails(ctx context.Context, hc *http.Client, baseURL string, d DeviceDetails) (*DeviceAuthorization, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	form := url.Values{"requested_from": {d.Hostname}}
	if d.OS != "" {
		form.Set("requested_os", d.OS)
	}
	if d.TimeZone != "" {
		form.Set("requested_tz", d.TimeZone)
	}
	if d.Client != "" {
		form.Set("requested_client", d.Client)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, normalizeRoot(baseURL)+DeviceCodePath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rearm: start login: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rearm: start login failed with status %d", resp.StatusCode)
	}
	var d DeviceAuthorization
	if err := json.Unmarshal(raw, &d); err != nil || d.DeviceCode == "" {
		return nil, fmt.Errorf("rearm: start login: malformed response")
	}
	if d.Interval <= 0 {
		d.Interval = 5
	}
	return &d, nil
}

// DevicePollStatus is the outcome of one poll.
type DevicePollStatus string

const (
	DevicePending   DevicePollStatus = "authorization_pending"
	DeviceSlowDown  DevicePollStatus = "slow_down"
	DeviceDenied    DevicePollStatus = "access_denied"
	DeviceExpired   DevicePollStatus = "expired_token"
	DeviceInvalid   DevicePollStatus = "invalid_grant"
	DeviceDelivered DevicePollStatus = "delivered"
)

// DeviceLogin is the delivered session: everything the caller stores, plus the key it acts as.
type DeviceLogin struct {
	Status       DevicePollStatus
	Description  string
	RefreshToken string
	Tokens       SessionTokens
	APIKeyID     string
	APIKeyUUID   string
	Org          string
	SessionUUID  string
}

// PollDeviceLogin performs one poll of the device-code grant; the caller sleeps Interval seconds
// between polls (add five on DeviceSlowDown) until the status is no longer pending.
func PollDeviceLogin(ctx context.Context, hc *http.Client, baseURL, deviceCode string) (*DeviceLogin, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	form := url.Values{"grant_type": {DeviceCodeGrant}, "device_code": {deviceCode}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, normalizeRoot(baseURL)+TokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rearm: poll login: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Error            string `json:"error"`
		Description      string `json:"error_description"`
		AccessToken      string `json:"access_token"`
		ExpiresIn        int64  `json:"expires_in"`
		RefreshToken     string `json:"refresh_token"`
		APIKeyID         string `json:"api_key_id"`
		APIKeyUUID       string `json:"api_key_uuid"`
		Org              string `json:"org"`
		Session          string `json:"session"`
		SessionExpiresAt string `json:"session_expires_at"`
	}
	_ = json.Unmarshal(raw, &out)
	// outcomes arrive as 200 with an error member; a non-JSON 4xx page from an ingress reads as invalid
	if out.Error != "" {
		return &DeviceLogin{Status: DevicePollStatus(out.Error), Description: out.Description}, nil
	}
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" || out.RefreshToken == "" {
		return &DeviceLogin{Status: DeviceInvalid, Description: fmt.Sprintf("unexpected response (status %d)", resp.StatusCode)}, nil
	}
	l := &DeviceLogin{Status: DeviceDelivered, RefreshToken: out.RefreshToken, APIKeyID: out.APIKeyID, APIKeyUUID: out.APIKeyUUID, Org: out.Org, SessionUUID: out.Session}
	l.Tokens.AccessToken = out.AccessToken
	l.Tokens.AccessTokenExpiry = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	if se, err := time.Parse(time.RFC3339, out.SessionExpiresAt); err == nil {
		l.Tokens.SessionExpiry = se
	}
	return l, nil
}
