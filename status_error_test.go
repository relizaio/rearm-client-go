package rearm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// An HTTP error status without GraphQL errors is a typed *StatusError carrying the status and the
// size of the request that was sent, with the same text as before (task PFA-9, PFA-3 design 3.2,
// tests T-6 to T-8).

// statusServer answers every GraphQL POST with status and body after reading the whole request,
// and records the Content-Length it saw and the bytes it read.
type statusServer struct {
	*httptest.Server
	contentLength atomic.Int64
	bytesRead     atomic.Int64
}

func newStatusServer(t *testing.T, status int, contentType, body string) *statusServer {
	t.Helper()
	s := &statusServer{}
	s.contentLength.Store(-2)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == TokenPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		n, _ := io.Copy(io.Discard, r.Body)
		s.contentLength.Store(r.ContentLength)
		s.bytesRead.Store(n)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *statusServer) client(t *testing.T) *Client {
	t.Helper()
	c, err := New(s.URL, "id", "secret", WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// uploadOneFile sends one file part of 4096 bytes through UploadMultipart.
func uploadOneFile(c *Client) error {
	_, err := UploadMultipart(context.Background(), c, "AddArtifactProgrammatic", AddArtifactProgrammatic_Operation,
		map[string]any{"artifactInput": map[string]any{"file": nil}},
		[]FilePart{{Filename: "bom.json", Content: strings.NewReader(strings.Repeat("x", 4096)), VariablePath: "variables.artifactInput.file"}})
	return err
}

// assertSentSize checks that RequestBytes is the Content-Length the server saw, and that the server
// read exactly that many bytes.
func assertSentSize(t *testing.T, se *StatusError, s *statusServer) {
	t.Helper()
	seen := s.contentLength.Load()
	if seen <= 0 || se.RequestBytes != seen || s.bytesRead.Load() != seen {
		t.Fatalf("RequestBytes %d, server saw Content-Length %d and read %d bytes", se.RequestBytes, seen, s.bytesRead.Load())
	}
}

// T-6: a 413 page from an ingress (not JSON) on an upload.
func TestStatusErrorOfANonJSONPageCarriesTheStartOfThePageAndTheSizeSent(t *testing.T) {
	page := "\n  <html><head><title>413 Request Entity Too Large</title></head><body><center><h1>413 Request Entity Too Large</h1></center><hr><center>nginx</center>" +
		strings.Repeat("<!-- a padding to disable MSIE and Chrome friendly error page -->\n", 4) + "</body></html>\n"
	s := newStatusServer(t, http.StatusRequestEntityTooLarge, "text/html", page)
	err := uploadOneFile(s.client(t))
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected a *StatusError, got %T %v", err, err)
	}
	wantBody := strings.TrimSpace(page[:200])
	if se.StatusCode != http.StatusRequestEntityTooLarge || se.Body != wantBody {
		t.Fatalf("status %d, body %q; want 413 and %q", se.StatusCode, se.Body, wantBody)
	}
	assertSentSize(t, se, s)
	if want := "rearm: request failed with status 413: " + wantBody; err.Error() != want {
		t.Fatalf("text %q, want %q", err.Error(), want)
	}
}

// T-6, the edge of the old text: a non-JSON body that trims to nothing still ends in ": ".
func TestStatusErrorOfABlankNonJSONBodyKeepsTheOldText(t *testing.T) {
	s := newStatusServer(t, http.StatusBadGateway, "text/plain", "  \n")
	err := uploadOneFile(s.client(t))
	var se *StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusBadGateway || se.Body != "" {
		t.Fatalf("expected a *StatusError 502 with an empty body, got %T %v", err, err)
	}
	if want := "rearm: request failed with status 502: "; err.Error() != want {
		t.Fatalf("text %q, want %q", err.Error(), want)
	}
}

// T-7: the backend's own 413, a JSON body without GraphQL errors, on both entry points.
func TestStatusErrorOfAJSONBodyWithoutErrorsOnUploadAndRaw(t *testing.T) {
	const body = `{"message":"Request exceeds the upload limit"}`
	calls := map[string]func(*Client) error{
		"UploadMultipart": uploadOneFile,
		"Raw": func(c *Client) error {
			_, err := Raw(context.Background(), c, "GetLatestReleaseProgrammatic", GetLatestReleaseProgrammatic_Operation,
				map[string]any{"release": map[string]any{"component": "c"}})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			s := newStatusServer(t, http.StatusRequestEntityTooLarge, "application/json", body)
			err := call(s.client(t))
			var se *StatusError
			if !errors.As(err, &se) {
				t.Fatalf("expected a *StatusError, got %T %v", err, err)
			}
			if se.StatusCode != http.StatusRequestEntityTooLarge || se.Body != "" {
				t.Fatalf("status %d, body %q; want 413 and an empty body", se.StatusCode, se.Body)
			}
			assertSentSize(t, se, s)
			if want := "rearm: request failed with status 413"; err.Error() != want {
				t.Fatalf("text %q, want %q", err.Error(), want)
			}
		})
	}
}

// T-8: GraphQL errors keep precedence over the status.
func TestGraphQLErrorsOnAnErrorStatusAreNotAStatusError(t *testing.T) {
	s := newStatusServer(t, http.StatusBadRequest, "application/json", `{"errors":[{"message":"x"}]}`)
	err := uploadOneFile(s.client(t))
	var gqlErrs GraphQLErrors
	if !errors.As(err, &gqlErrs) || len(gqlErrs) != 1 || gqlErrs[0].Message != "x" {
		t.Fatalf("expected GraphQLErrors [x], got %T %v", err, err)
	}
	var se *StatusError
	if errors.As(err, &se) {
		t.Fatalf("GraphQL errors must not be a *StatusError, got %+v", se)
	}
}

// RequestBytes: 0 for no body, -1 for a body of unknown length, the length otherwise.
func TestRequestBytes(t *testing.T) {
	ctx := context.Background()
	noBody, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://x", nil)
	emptyBody, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://x", strings.NewReader(""))
	sized, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://x", strings.NewReader("12345"))
	unknown, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://x", io.NopCloser(strings.NewReader("12345")))
	cases := []struct {
		name string
		req  *http.Request
		want int64
	}{
		{"no body", noBody, 0},
		{"empty body", emptyBody, 0},
		{"sized body", sized, 5},
		{"unknown length", unknown, -1},
	}
	for _, tc := range cases {
		if got := requestBytes(tc.req); got != tc.want {
			t.Errorf("%s: requestBytes %d, want %d", tc.name, got, tc.want)
		}
	}
}

// The zero value with a status reads as the JSON-branch text, so callers can build one in tests.
func TestStatusErrorZeroValueText(t *testing.T) {
	if got, want := (&StatusError{StatusCode: 401}).Error(), "rearm: request failed with status 401"; got != want {
		t.Fatalf("text %q, want %q", got, want)
	}
}
