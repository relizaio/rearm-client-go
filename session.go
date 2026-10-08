package rearm

import (
	"context"
	"encoding/json"
	"errors"
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
// When the key bounds its sessions (sessionMaxMinutes) the session has a hard end: no token
// outlives it, the client does not refresh past it, and a session that ends inside its first
// access token is delivered with no refresh token at all.
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
	// SessionHardExpiry is the session's fixed end when the key bounds its sessions; zero when
	// nothing but the 90-day cap does. No refresh is attempted at or after it.
	SessionHardExpiry time.Time
}

// NewWithSession builds a client that acts as the key behind a browser-login session. tokens may
// carry a cached access token; persist (optional) receives every new token set so the caller can
// store it. There is no Basic fallback and no legacy endpoint: session tokens are honoured on the
// programmatic endpoint only. refreshToken may be empty for a session delivered without one (it
// ends inside its first access token): tokens must then carry that access token.
func NewWithSession(baseURL, refreshToken string, tokens SessionTokens, persist func(SessionTokens), opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("rearm: base URL is required")
	}
	if refreshToken == "" && tokens.AccessToken == "" {
		return nil, fmt.Errorf("rearm: a refresh token or an access token is required for a session client")
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
		revokeURL: root + RevokePath, exchange: true, sessionMode: true, refreshToken: refreshToken, persist: persist,
		bearer: tokens.AccessToken, sessionExp: tokens.SessionExpiry, sessionHardExp: tokens.SessionHardExpiry,
		store: o.store, renewing: make(chan struct{}, 1)}
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

// SessionStore is where every process sharing one login keeps its tokens (the CLI's credentials
// file). With a store the client renews only while holding its lock: it re-reads the store first and
// adopts a newer set another process saved instead of refreshing, and it saves a rotated refresh token
// before giving the lock back. The server binds every access token to the session's current refresh
// token and rotates that token on each refresh, so processes that refresh on their own kill each
// other's tokens; a store is how they take turns.
type SessionStore interface {
	// Lock takes the cross-process lock, waiting until ctx ends; release gives it back.
	Lock(ctx context.Context) (release func(), err error)
	// Load reads the stored set. RefreshToken is the stored (current) refresh token, always set when
	// the store holds one. A store with no session returns ErrNoSession.
	Load() (SessionTokens, error)
	// Save writes the whole set (RefreshToken is the current token). Called only between Lock and release.
	Save(SessionTokens) error
}

// ErrNoSession is what SessionStore.Load returns when the store holds no browser-login session.
var ErrNoSession = errors.New("rearm: no browser-login session in the store")

// NoSessionDescription is the refusal a renewal gives when the store holds no session any more (the
// user logged out, or logged in with a key).
const NoSessionDescription = "no browser-login session on file; run rearm login"

// LockWait bounds how long a renewal waits for the store's lock.
var LockWait = 30 * time.Second

// refreshWait bounds a refresh made while holding the store's lock, so a hung network cannot hold
// the lock for the caller's whole client timeout.
const refreshWait = 30 * time.Second

// WithSessionStore makes a session client renew through a store shared with other processes (see
// SessionStore). Without one the client refreshes on its own, as before.
func WithSessionStore(s SessionStore) Option { return func(o *options) { o.store = s } }

// ensureSessionToken refreshes when a refresh can still buy time: there is a refresh token, the
// session's hard end (if any) has not come, and the current token does not already run to it.
// Otherwise the current token is used to its real expiry, and after that the session has ended.
func (t *authTransport) ensureSessionToken(ctx context.Context, now time.Time) error {
	return t.renew(ctx, now, "")
}

// renew gets the client a token it can send. rejected is the bearer the server just refused with a
// 401 ("" when the token is merely due): that token is dead whatever its expiry says. One renewal
// runs at a time per client; a goroutine that waited for another one's renewal uses its result.
func (t *authTransport) renew(ctx context.Context, now time.Time, rejected string) error {
	if t.renewing != nil {
		select {
		case t.renewing <- struct{}{}:
			defer func() { <-t.renewing }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.mu.Lock()
	cur, fresh := t.bearer, t.bearer != "" && now.Before(t.bearerExp)
	t.mu.Unlock()
	if rejected != "" && cur != rejected {
		return nil // renewed by another goroutine since the refused request went out
	}
	if rejected == "" && fresh {
		return nil // renewed by another goroutine while this one waited
	}
	if t.store == nil {
		return t.renewInPlace(ctx, now, rejected != "")
	}
	return t.renewThroughStore(ctx, now, rejected)
}

// decideLocked applies the session's rules to the tokens in memory: whether a refresh can buy time,
// whether the current token is still within its real expiry, and when the session ends. A rejected
// token buys nothing, so a refresh is worth making until the hard end even when the token ran to it.
func (t *authTransport) decideLocked(now time.Time, rejected bool) (canRefresh, usable bool, end time.Time) {
	tokenEnd := t.bearerExp.Add(time.Minute)
	hard := t.sessionHardExp
	if rejected {
		canRefresh = t.refreshToken != "" && (hard.IsZero() || now.Before(hard))
	} else {
		canRefresh = t.refreshToken != "" && (hard.IsZero() || (now.Before(hard) && tokenEnd.Before(hard)))
	}
	usable = t.bearer != "" && now.Before(tokenEnd)
	end = hard
	if end.IsZero() {
		end = tokenEnd
	}
	return canRefresh, usable, end
}

// renewInPlace is the path without a store: refresh with the refresh token in memory.
func (t *authTransport) renewInPlace(ctx context.Context, now time.Time, rejected bool) error {
	t.mu.Lock()
	canRefresh, usable, end := t.decideLocked(now, rejected)
	t.mu.Unlock()
	if canRefresh {
		snap, err := t.refreshAccessToken(ctx)
		if err != nil {
			return err
		}
		t.report(snap)
		return nil
	}
	if usable {
		// due but nothing to refresh with: the token is used to its real end, and a refused one is
		// sent again so the caller sees the server's answer
		return nil
	}
	return &SessionError{Code: "invalid_grant", Description: SessionEndedDescription(end)}
}

// renewThroughStore: lock, re-read, adopt a newer set or refresh, save, release, then report.
func (t *authTransport) renewThroughStore(ctx context.Context, now time.Time, rejected string) error {
	lctx, cancel := context.WithTimeout(ctx, LockWait)
	release, err := t.store.Lock(lctx)
	timedOut := lctx.Err() == context.DeadlineExceeded
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if timedOut {
			return fmt.Errorf("rearm: the credentials store has been locked by another process for %s", LockWait)
		}
		return fmt.Errorf("rearm: could not lock the credentials store: %w", err)
	}
	snap, err := t.renewLocked(ctx, now, rejected)
	release()
	if snap != nil {
		t.report(*snap)
	}
	return err
}

// renewLocked runs while the store's lock is held. It returns the token set to report through
// persist, if any, after the lock is released.
func (t *authTransport) renewLocked(ctx context.Context, now time.Time, rejected string) (*SessionTokens, error) {
	on, err := t.store.Load()
	if errors.Is(err, ErrNoSession) {
		return nil, &SessionError{Code: "invalid_grant", Description: NoSessionDescription}
	}
	if err != nil {
		return nil, fmt.Errorf("rearm: could not read the credentials store: %w", err)
	}
	t.mu.Lock()
	startRefresh := t.refreshToken
	adopted := on.RefreshToken != t.refreshToken || on.AccessToken != t.bearer
	if adopted {
		// another process renewed: its set is the current one, and the refresh token held here is retired
		t.refreshToken = on.RefreshToken
		t.bearer = on.AccessToken
		t.bearerExp = time.Time{}
		if on.AccessToken != "" && !on.AccessTokenExpiry.IsZero() {
			t.bearerExp = on.AccessTokenExpiry.Add(-time.Minute)
		}
		t.sessionExp = on.SessionExpiry
		t.sessionHardExp = on.SessionHardExpiry
		t.clampLocked()
	}
	rej := rejected != "" && t.bearer == rejected
	fresh := !rej && t.bearer != "" && now.Before(t.bearerExp)
	canRefresh, usable, end := t.decideLocked(now, rej)
	t.mu.Unlock()
	var adoptedSnap *SessionTokens
	if adopted {
		s := t.snapshot(startRefresh)
		adoptedSnap = &s
	}
	if fresh {
		return adoptedSnap, nil // the refresh was already made elsewhere: no server call
	}
	if canRefresh {
		rctx, cancel := context.WithTimeout(ctx, refreshWait)
		_, err := t.refreshAccessToken(rctx)
		cancel()
		if err != nil {
			return adoptedSnap, err
		}
		// the rotated token goes to the store before the lock is given back, so a process waiting on
		// the lock reads it and never refreshes with the token just retired
		t.mu.Lock()
		full := SessionTokens{AccessToken: t.bearer, AccessTokenExpiry: t.bearerExp.Add(time.Minute), RefreshToken: t.refreshToken,
			SessionExpiry: t.sessionExp, SessionHardExpiry: t.sessionHardExp}
		t.mu.Unlock()
		if err := t.store.Save(full); err != nil {
			return nil, fmt.Errorf("rearm: session refreshed but could not be stored: %w", err)
		}
		s := t.snapshot(startRefresh)
		return &s, nil
	}
	if usable {
		// due but nothing to refresh with: the token is used to its real end, and a refused one is sent
		// again so the caller sees the server's answer
		return adoptedSnap, nil
	}
	return adoptedSnap, &SessionError{Code: "invalid_grant", Description: SessionEndedDescription(end)}
}

// clampLocked keeps the planned token end at or before the session's hard end.
func (t *authTransport) clampLocked() {
	if !t.sessionHardExp.IsZero() && !t.bearerExp.IsZero() && t.bearerExp.Add(time.Minute).After(t.sessionHardExp) {
		t.bearerExp = t.sessionHardExp.Add(-time.Minute)
	}
}

// snapshot is the set handed to persist: RefreshToken only when it differs from before.
func (t *authTransport) snapshot(before string) SessionTokens {
	t.mu.Lock()
	defer t.mu.Unlock()
	rotated := ""
	if t.refreshToken != before {
		rotated = t.refreshToken
	}
	return SessionTokens{AccessToken: t.bearer, AccessTokenExpiry: t.bearerExp.Add(time.Minute), SessionExpiry: t.sessionExp,
		SessionHardExpiry: t.sessionHardExp, RefreshToken: rotated}
}

func (t *authTransport) report(s SessionTokens) {
	if t.persist != nil {
		t.persist(s)
	}
}

// SessionEndedDescription is the refusal the server gives a refresh after the hard end; the client
// gives the same words when it knows the session is over without asking.
func SessionEndedDescription(end time.Time) string {
	return "session ended at " + end.UTC().Format(time.RFC3339) + "; run rearm login"
}

// refreshAccessToken trades the refresh token for a new access token and keeps the new set; it
// returns the set to report (RefreshToken only when rotated).
func (t *authTransport) refreshAccessToken(ctx context.Context) (SessionTokens, error) {
	t.mu.Lock()
	before := t.refreshToken
	t.mu.Unlock()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {before}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return SessionTokens{}, err
	}
	req.Header.Set("User-Agent", t.userAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return SessionTokens{}, fmt.Errorf("rearm: session refresh: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tok struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int64  `json:"expires_in"`
		RefreshToken     string `json:"refresh_token"`
		SessionExpiresAt string `json:"session_expires_at"`
		SessionHardEnd   string `json:"session_hard_expiry"`
		Error            string `json:"error"`
		Description      string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &tok)
	// outcomes arrive as 200 with an error member (the ingress in front of ReARM rewrites 4xx bodies)
	if tok.Error != "" || resp.StatusCode != http.StatusOK || tok.AccessToken == "" {
		if tok.Error == "" {
			return SessionTokens{}, fmt.Errorf("rearm: session refresh failed with status %d", resp.StatusCode)
		}
		return SessionTokens{}, &SessionError{Code: tok.Error, Description: tok.Description}
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
	if he, err := time.Parse(time.RFC3339, tok.SessionHardEnd); err == nil {
		t.sessionHardExp = he
	}
	// the token lives no longer than the session: never plan to use it past the hard end
	t.clampLocked()
	if tok.RefreshToken != "" {
		// rotation: from now on only the new token refreshes; the store and the persist callback carry it to disk
		t.refreshToken = tok.RefreshToken
	}
	t.mu.Unlock()
	return t.snapshot(before), nil
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
		return SessionTokens{SessionExpiry: t.sessionExp, SessionHardExpiry: t.sessionHardExp}
	}
	return SessionTokens{AccessToken: t.bearer, AccessTokenExpiry: t.bearerExp.Add(time.Minute), SessionExpiry: t.sessionExp,
		SessionHardExpiry: t.sessionHardExp, RefreshToken: t.refreshToken}
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
func StartDeviceLoginWithDetails(ctx context.Context, hc *http.Client, baseURL string, details DeviceDetails) (*DeviceAuthorization, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	form := url.Values{"requested_from": {details.Hostname}}
	if details.OS != "" {
		form.Set("requested_os", details.OS)
	}
	if details.TimeZone != "" {
		form.Set("requested_tz", details.TimeZone)
	}
	if details.Client != "" {
		form.Set("requested_client", details.Client)
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
// RefreshToken is empty when the session ends inside its first access token (Tokens.SessionHardExpiry).
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
		SessionHardEnd   string `json:"session_hard_expiry"`
	}
	_ = json.Unmarshal(raw, &out)
	// outcomes arrive as 200 with an error member; a non-JSON 4xx page from an ingress reads as invalid
	if out.Error != "" {
		return &DeviceLogin{Status: DevicePollStatus(out.Error), Description: out.Description}, nil
	}
	// no refresh token is a valid delivery only for a session with a hard end inside the first token
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" || (out.RefreshToken == "" && out.SessionHardEnd == "") {
		return &DeviceLogin{Status: DeviceInvalid, Description: fmt.Sprintf("unexpected response (status %d)", resp.StatusCode)}, nil
	}
	l := &DeviceLogin{Status: DeviceDelivered, RefreshToken: out.RefreshToken, APIKeyID: out.APIKeyID, APIKeyUUID: out.APIKeyUUID, Org: out.Org, SessionUUID: out.Session}
	l.Tokens.AccessToken = out.AccessToken
	l.Tokens.AccessTokenExpiry = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	if se, err := time.Parse(time.RFC3339, out.SessionExpiresAt); err == nil {
		l.Tokens.SessionExpiry = se
	}
	if he, err := time.Parse(time.RFC3339, out.SessionHardEnd); err == nil {
		l.Tokens.SessionHardExpiry = he
	}
	return l, nil
}
