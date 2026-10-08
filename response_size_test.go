package rearm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// How much of an answer Raw reads (task SCORE-24, design 5.4): a merged release SBOM
// the server serves under its 64 MiB rebom limit comes back whole, however much JSON escaping grows
// the answer, and an answer over the client's limit is refused as too large, never read truncated
// and reported as malformed.

// rawAnswer runs Raw against a stub whose handler writes the answer.
func rawAnswer(t *testing.T, handler http.HandlerFunc) (json.RawMessage, error) {
	t.Helper()
	srv := httptest.NewServer(handler)
	defer srv.Close()
	c, err := New(srv.URL, "id", "secret", WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	return Raw(context.Background(), c, "ReleaseSbomExportProgrammatic", ReleaseSbomExportProgrammatic_Operation,
		map[string]any{"release": "r-1"})
}

// streamedAnswer writes {"data":{"x":"aaa..."}} of exactly size bytes without holding it in memory.
func streamedAnswer(size int) http.HandlerFunc {
	const head, tail = `{"data":{"x":"`, `"}}`
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(head))
		block := bytes.Repeat([]byte{'a'}, 1<<20)
		for left := size - len(head) - len(tail); left > 0; {
			n := min(left, len(block))
			if _, err := w.Write(block[:n]); err != nil {
				return
			}
			left -= n
		}
		_, _ = w.Write([]byte(tail))
	}
}

func TestADocumentOfTheServersLimitComesBackWholeHoweverEscapingGrowsTheAnswer(t *testing.T) {
	// 64 MiB, every byte one JSON escaping doubles: the answer is twice the document, and twice
	// the 64 MiB the client read before
	doc := strings.Repeat(`"\`, 32<<20)
	answer, err := json.Marshal(map[string]any{"data": map[string]any{"releaseSbomExportProgrammatic": doc}})
	if err != nil {
		t.Fatal(err)
	}
	if len(answer) <= 2*(64<<20) {
		t.Fatalf("the answer should be over 128 MiB, is %d bytes", len(answer))
	}
	data, err := rawAnswer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(answer) })
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil || out["releaseSbomExportProgrammatic"] != doc {
		t.Fatalf("the document comes back whole (%v)", err)
	}
}

func TestAnAnswerAtTheLimitIsReadAndOneByteMoreIsTooLargeNotMalformed(t *testing.T) {
	data, err := rawAnswer(t, streamedAnswer(maxResponseBytes))
	if err != nil {
		t.Fatalf("an answer of exactly %d bytes is read: %v", maxResponseBytes, err)
	}
	if want := maxResponseBytes - len(`{"data":}`); len(data) != want {
		t.Fatalf("data of %d bytes, got %d", want, len(data))
	}
	data = nil

	for name, handler := range map[string]http.HandlerFunc{
		"one byte over, streamed": streamedAnswer(maxResponseBytes + 1),
		"declared over": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(maxResponseBytes+1))
			_, _ = w.Write([]byte(`{"data":{"x":"`))
		},
	} {
		_, err := rawAnswer(t, handler)
		if !errors.Is(err, ErrResponseTooLarge) {
			t.Fatalf("%s: want ErrResponseTooLarge, got %v", name, err)
		}
		if msg := err.Error(); !strings.Contains(msg, "response too large") || !strings.Contains(msg, "256 MiB") ||
			strings.Contains(msg, "malformed") {
			t.Fatalf("%s: the error says the answer is too large and how large the client reads, got %q", name, msg)
		}
	}
}
