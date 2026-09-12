package rearm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// DownloadArtifact streams an artifact's stored bytes (raw = the exact file as uploaded, otherwise
// the server's processed form). version 0 means the latest. The caller closes the reader.
func DownloadArtifact(ctx context.Context, c *Client, artifactUUID string, raw bool, version int) (io.ReadCloser, string, error) {
	endpoint := "/download"
	if raw {
		endpoint = "/rawdownload"
	}
	u := c.root + "/api/programmatic/v1/artifact/" + artifactUUID + endpoint
	if version > 0 {
		u += "?version=" + strconv.Itoa(version)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, "", fmt.Errorf("rearm: artifact download failed with status %d", resp.StatusCode)
	}
	return resp.Body, resp.Header.Get("Content-Disposition"), nil
}
