package rearm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Raw sends an operation and returns the server's `data` member untouched, so callers that print
// or forward JSON keep the exact shape the server produced (the typed functions normalise nulls
// and field order). Query documents are the generated *_Operation constants, so no caller has to
// carry GraphQL text.
func Raw(ctx context.Context, c *Client, opName, query string, variables any) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{"operationName": opName, "query": query, "variables": variables})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.exchange(req)
}

// GraphQLError is one entry of a GraphQL `errors` array.
type GraphQLError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// GraphQLErrors is the whole array, returned as the error of Raw / UploadMultipart.
type GraphQLErrors []GraphQLError

func (e GraphQLErrors) Error() string {
	msgs := make([]string, 0, len(e))
	for _, x := range e {
		msgs = append(msgs, x.Message)
	}
	return "rearm: " + strings.Join(msgs, "; ")
}

// maxResponseBytes is the largest answer exchange reads. The largest answers are merged release
// SBOMs (releaseSbomExportProgrammatic): the server serves a document it read from rebom under a
// 64 MiB limit (rearm-saas RebomService.REBOM_RESPONSE_LIMIT, task SCORE-19), and the answer
// carries it as a JSON string, where escaping can double it (every quote and backslash of a JSON
// document gains a backslash), plus the envelope and any support metadata the server adds. Four
// times the server's limit holds all of that with room, and still bounds what a misbehaving server
// can make the client buffer (task SCORE-24).
const maxResponseBytes = 4 * (64 << 20)

// ErrResponseTooLarge is the error of an answer longer than the client reads: it is refused whole,
// never parsed truncated (which read as a malformed response).
var ErrResponseTooLarge = errors.New("rearm: response too large")

// readResponse reads the whole answer, or refuses one over maxResponseBytes.
func readResponse(resp *http.Response) ([]byte, error) {
	if resp.ContentLength > maxResponseBytes {
		return nil, responseTooLarge()
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxResponseBytes {
		return nil, responseTooLarge()
	}
	return raw, nil
}

func responseTooLarge() error {
	return fmt.Errorf("%w: the answer is over %d MiB, the most this client reads", ErrResponseTooLarge, maxResponseBytes>>20)
}

// exchange runs the request through the authenticated HTTP client and splits data from errors.
func (c *Client) exchange(req *http.Request) (json.RawMessage, error) {
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := readResponse(resp)
	if err != nil {
		return nil, err
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors GraphQLErrors   `json:"errors"`
	}
	if jerr := json.Unmarshal(raw, &env); jerr != nil {
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("rearm: request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 200)])))
		}
		return nil, fmt.Errorf("rearm: malformed response: %w", jerr)
	}
	if len(env.Errors) > 0 {
		return env.Data, env.Errors
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("rearm: request failed with status %d", resp.StatusCode)
	}
	return env.Data, nil
}
