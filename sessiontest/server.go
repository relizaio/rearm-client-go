// Package sessiontest is a fake ReARM token and GraphQL server with the real semantics of CLI
// browser-login sessions, plus an in-memory SessionStore, for tests of session clients (this
// module's, the CLI's, and a tester's).
//
// The semantics follow the server (CliSessionService and ProgrammaticAuthenticationFilter):
//
//   - every access token carries fp = sha256("cli-session:" + sid + ":" + currentRefreshHash)[:16],
//     and the GraphQL endpoint accepts it only while that hash is the session's current one, the token
//     has not passed its exp by the server's clock, and the session is not revoked;
//   - each refresh with the current token rotates it (previous := current, rotatedAt := now);
//   - the retired token presented within Grace of rotatedAt rotates the current token again, keeping
//     previous and its window (the first refresher's tokens are dead from then on);
//   - the retired token presented after Grace (REUSED) revokes the session;
//   - anything else (unknown token, revoked or expired session) answers invalid_grant.
//
// Refresh outcomes are answered as 200 with an error member, as the ingress in front of ReARM makes
// them. A refused GraphQL request is a 401 whose body is the server's JSON invalid_token or, with
// Options.HTML401, an HTML page as the ingress serves it.
package sessiontest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	rearm "github.com/relizaio/rearm-client-go"
)

// Options configures a Server. Zero values take the server's defaults.
type Options struct {
	// Grace is the window in which a retired refresh token re-rotates instead of revoking (2 min).
	Grace time.Duration
	// TokenTTL is an access token's lifetime (1 h).
	TokenTTL time.Duration
	// SessionTTL is how far a refresh slides the session (30 days).
	SessionTTL time.Duration
	// HTML401 answers refused GraphQL requests with an HTML page instead of the JSON invalid_token.
	HTML401 bool
	// Clock is the server's time (time.Now). Advance moves it forward on top of this.
	Clock func() time.Time
	// RefreshDelay holds every refresh answer this long after it was decided (a slow network), or until
	// the client gives up on the request.
	RefreshDelay time.Duration
	// GraphQL answers accepted GraphQL requests with the whole response body; default {"data":{}}.
	GraphQL func(opName string, vars json.RawMessage) json.RawMessage
}

// Counts are the server's counters, read with Server.Counts.
type Counts struct {
	// Refreshes is every refresh_token grant received, whatever its verdict.
	Refreshes int
	// Rotations is every refresh answered with a new token (CURRENT and PREVIOUS_IN_GRACE).
	Rotations int
	// GraceRerotations is the refreshes with a retired token inside the grace.
	GraceRerotations int
	// Reused is the refreshes with a retired token after the grace.
	Reused int
	// Revocations is the sessions revoked (REUSED, or the revoke endpoint).
	Revocations int
	// InvalidGrants is every refresh refused (REUSED, UNKNOWN, revoked or expired session).
	InvalidGrants int
	// Rejected401 is every GraphQL request refused with 401.
	Rejected401 int
	// GraphQL is every GraphQL request accepted.
	GraphQL int
}

type session struct {
	sid       string
	current   string // sha256 hex of the current refresh token
	previous  string
	rotatedAt time.Time
	expires   time.Time
	revoked   bool
}

// Server is the fake. Its URL is the base URL a client is built with.
type Server struct {
	*httptest.Server
	opts Options

	mu         sync.Mutex
	offset     time.Duration
	sessions   []*session
	seq        int
	counts     Counts
	presented  []string
	refreshLog []time.Time
	refuseAll  bool
}

// NewServer starts a fake server, closed when the test ends.
func NewServer(t testing.TB, o Options) *Server {
	if o.Grace == 0 {
		o.Grace = 2 * time.Minute
	}
	if o.TokenTTL == 0 {
		o.TokenTTL = time.Hour
	}
	if o.SessionTTL == 0 {
		o.SessionTTL = 30 * 24 * time.Hour
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	s := &Server{opts: o}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Now is the server's clock.
func (s *Server) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nowLocked()
}

func (s *Server) nowLocked() time.Time { return s.opts.Clock().Add(s.offset) }

// Advance moves the server's clock forward.
func (s *Server) Advance(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offset += d
}

// Counts returns a copy of the counters.
func (s *Server) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts
}

// Presented returns every refresh token presented to the token endpoint, in order.
func (s *Server) Presented() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.presented...)
}

// RefreshLog returns the wall-clock instant (time.Now, not the server's clock) of every refresh
// grant, for ordering against a store's lock holds.
func (s *Server) RefreshLog() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.refreshLog...)
}

// Login opens a session as a delivered device login would, returning its refresh token and the
// first token set (RefreshToken included).
func (s *Server) Login() (string, rearm.SessionTokens) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	sess := &session{sid: fmt.Sprintf("sid-%d", len(s.sessions)+1), expires: now.Add(s.opts.SessionTTL)}
	s.sessions = append(s.sessions, sess)
	rt := s.newRefreshLocked(sess)
	sess.current = hash(rt)
	at, exp := s.accessLocked(sess, now)
	return rt, rearm.SessionTokens{AccessToken: at, AccessTokenExpiry: exp, RefreshToken: rt, SessionExpiry: sess.expires}
}

// RotateDirectly rotates the newest session's refresh token as a refresh by a process that shares
// nothing with the clients under test would (the token is not handed to them). It returns the new
// refresh token.
func (s *Server) RotateDirectly() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[len(s.sessions)-1]
	sess.previous = sess.current
	sess.rotatedAt = s.nowLocked()
	rt := s.newRefreshLocked(sess)
	sess.current = hash(rt)
	s.counts.Rotations++
	return rt
}

// RefuseAll makes the GraphQL endpoint refuse every request with 401 (refresh still works).
func (s *Server) RefuseAll(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refuseAll = on
}

// Revoked says whether the newest session is revoked.
func (s *Server) Revoked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[len(s.sessions)-1].revoked
}

// CurrentRefreshHash is sha256 hex of the newest session's current refresh token.
func (s *Server) CurrentRefreshHash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[len(s.sessions)-1].current
}

// IsCurrent says whether refreshToken is the newest session's current refresh token.
func (s *Server) IsCurrent(refreshToken string) bool {
	return hash(refreshToken) == s.CurrentRefreshHash()
}

func (s *Server) newRefreshLocked(sess *session) string {
	s.seq++
	return fmt.Sprintf("rt-%s-%d", sess.sid, s.seq)
}

func (s *Server) accessLocked(sess *session, now time.Time) (string, time.Time) {
	s.seq++
	exp := now.Add(s.opts.TokenTTL).Truncate(time.Second)
	return fmt.Sprintf("at-%d.%s.%d", s.seq, fp(sess.sid, sess.current), exp.Unix()), exp
}

func hash(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

// FP is the access token's binding to the session's current refresh token, as the server computes it.
func FP(sid, refreshHash string) string {
	return fp(sid, refreshHash)
}

func fp(sid, refreshHash string) string {
	h := sha256.Sum256([]byte("cli-session:" + sid + ":" + refreshHash))
	return hex.EncodeToString(h[:])[:16]
}

// Verdict is the server's reading of a presented refresh token.
type Verdict string

const (
	Current         Verdict = "CURRENT"
	PreviousInGrace Verdict = "PREVIOUS_IN_GRACE"
	Reused          Verdict = "REUSED"
	Unknown         Verdict = "UNKNOWN"
)

// verdictLocked mirrors CliSessionService.verdict.
func (s *Server) verdictLocked(presented string, now time.Time) (*session, Verdict) {
	h := hash(presented)
	for _, sess := range s.sessions {
		if sess.current != h && sess.previous != h {
			continue
		}
		if sess.revoked || !sess.expires.After(now) {
			return sess, Unknown
		}
		if sess.current == h {
			return sess, Current
		}
		if !sess.rotatedAt.IsZero() && sess.rotatedAt.Add(s.opts.Grace).After(now) {
			return sess, PreviousInGrace
		}
		return sess, Reused
	}
	return nil, Unknown
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case rearm.TokenPath:
		s.token(w, r)
	case rearm.RevokePath:
		_ = r.ParseForm()
		s.mu.Lock()
		if sess, v := s.verdictLocked(r.PostForm.Get("token"), s.nowLocked()); sess != nil && (v == Current || v == PreviousInGrace) {
			sess.revoked = true
			s.counts.Revocations++
		}
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case rearm.ProgrammaticPath:
		s.graphql(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	w.Header().Set("Content-Type", "application/json")
	if r.PostForm.Get("grant_type") != "refresh_token" {
		_, _ = io.WriteString(w, `{"error":"unsupported_grant_type","error_description":"only refresh_token is faked"}`)
		return
	}
	body := s.refresh(r.PostForm.Get("refresh_token"))
	if s.opts.RefreshDelay > 0 {
		// the refresh is decided (and rotated) already; a client that gives up meanwhile loses the answer
		select {
		case <-time.After(s.opts.RefreshDelay):
		case <-r.Context().Done():
			return
		}
	}
	_, _ = w.Write(body)
}

// refresh decides one refresh_token grant as CliSessionService.refresh does and returns the answer.
func (s *Server) refresh(presented string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.nowLocked()
	s.counts.Refreshes++
	s.presented = append(s.presented, presented)
	s.refreshLog = append(s.refreshLog, time.Now())
	sess, v := s.verdictLocked(presented, now)
	switch v {
	case Current:
		sess.previous = sess.current
		sess.rotatedAt = now
	case PreviousInGrace:
		s.counts.GraceRerotations++ // keep the retired hash and its window; the current token is replaced once more
	case Reused:
		s.counts.Reused++
		s.counts.Revocations++
		sess.revoked = true
		fallthrough
	default:
		s.counts.InvalidGrants++
		return []byte(`{"error":"invalid_grant","error_description":"unknown, expired or revoked refresh token; log in again"}`)
	}
	s.counts.Rotations++
	rt := s.newRefreshLocked(sess)
	sess.current = hash(rt)
	sess.expires = now.Add(s.opts.SessionTTL)
	at, _ := s.accessLocked(sess, now)
	b, _ := json.Marshal(map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": int64(s.opts.TokenTTL / time.Second),
		"refresh_token": rt, "session_expires_at": sess.expires.UTC().Format(time.RFC3339)})
	return b
}

// acceptedLocked reads the bearer as the server's filter does.
func (s *Server) acceptedLocked(auth string, now time.Time) bool {
	tok, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return false
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return false
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || !now.Before(time.Unix(exp, 0)) {
		return false
	}
	for _, sess := range s.sessions {
		if !sess.revoked && fp(sess.sid, sess.current) == parts[1] {
			return true
		}
	}
	return false
}

func (s *Server) graphql(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	ok := !s.refuseAll && s.acceptedLocked(r.Header.Get("Authorization"), s.nowLocked())
	if ok {
		s.counts.GraphQL++
	} else {
		s.counts.Rejected401++
	}
	s.mu.Unlock()
	if !ok {
		if s.opts.HTML401 {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, "<html><body><h1>401</h1><p>Your request has returned an error with the code: 401 (Unauthorized).</p></body></html>")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid_token","error_description":"the access token is invalid or expired"}`)
		return
	}
	var in struct {
		OperationName string          `json:"operationName"`
		Variables     json.RawMessage `json:"variables"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	out := json.RawMessage(`{"data":{}}`)
	if s.opts.GraphQL != nil {
		out = s.opts.GraphQL(in.OperationName, in.Variables)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}
