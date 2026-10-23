package rearm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The typed call GetReleaseByCommitProgrammatic (task TPF-99, mutation round 2): the document it
// sends and the errors it returns.

func TestTheTypedReleaseByCommitSendsTheByCommitDocument(t *testing.T) {
	var got byCommitRequest
	c := byCommitServer(t, &got, func() string { return byCommitData("{}") })

	if _, err := GetReleaseByCommitProgrammatic(context.Background(), c, byCommitSha, "my-service"); err != nil {
		t.Fatal(err)
	}
	if got.Query != GetReleaseByCommitProgrammatic_Operation {
		t.Fatalf("the operation text of this call, got %q", got.Query)
	}
	if !strings.Contains(got.Query, "getReleaseByCommitProgrammatic(commit: $commit, componentId: $componentId)") {
		t.Fatalf("the single-release by-commit field, got %q", got.Query)
	}
	if strings.Contains(got.Query, "getReleasesByCommitProgrammatic") || strings.Contains(got.Query, "ReleaseByReleaseVersion") {
		t.Fatalf("neither the list field nor the by-version one, got %q", got.Query)
	}
}

func TestTheTypedReleaseByCommitReturnsAnErrorFromTheServer(t *testing.T) {
	var got byCommitRequest
	c := byCommitServer(t, &got, func() string {
		return `{"errors":[{"message":"` + byCommitFieldUndefined + `"}]}`
	})
	resp, err := GetReleaseByCommitProgrammatic(context.Background(), c, byCommitSha, "my-service")
	if err == nil || !strings.Contains(err.Error(), "FieldUndefined") {
		t.Fatalf("an older server's one error comes back, got %v", err)
	}
	if resp == nil || resp.GetGetReleaseByCommitProgrammatic() != nil {
		t.Fatalf("no document with an error, got %v", resp)
	}
}

func TestTheTypedReleaseByCommitReturnsAnErrorOnAnHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "id", "secret", WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := GetReleaseByCommitProgrammatic(context.Background(), c, byCommitSha, "my-service"); err == nil {
		t.Fatal("an HTTP 500 is an error")
	}
}
