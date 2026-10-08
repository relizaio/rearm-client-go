package rearm

import "time"

// ExpireAccessToken makes a client's access token due now, as an hour passing would (tests only,
// for the external test package).
func ExpireAccessToken(c *Client) {
	c.transport.mu.Lock()
	defer c.transport.mu.Unlock()
	c.transport.bearerExp = time.Now().Add(-time.Second)
}

// SetRefreshWait shortens the deadline of a refresh made under the store's lock (tests only); the
// returned func restores it.
func SetRefreshWait(d time.Duration) (restore func()) {
	saved := refreshWait
	refreshWait = d
	return func() { refreshWait = saved }
}
