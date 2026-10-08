package rearm

import "time"

// ExpireAccessToken makes a client's access token due now, as an hour passing would (tests only,
// for the external test package).
func ExpireAccessToken(c *Client) {
	c.transport.mu.Lock()
	defer c.transport.mu.Unlock()
	c.transport.bearerExp = time.Now().Add(-time.Second)
}
