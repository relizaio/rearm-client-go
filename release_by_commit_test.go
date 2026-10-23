package rearm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The by-commit release lookup (task TPF-99, child 2 of TPF-72): the CLI sends
// GetReleaseByCommitProgrammatic raw with the commit and the component as variables and prints the
// root field's string as the server produced it, "{}" for every miss.

const byCommitSha = "1f2e3d4c5b6a79880716253443526170f1e2d3c4"

const byCommitFieldUndefined = "Validation error (FieldUndefined@[getReleaseByCommitProgrammatic]) : " +
	"Field 'getReleaseByCommitProgrammatic' in type 'Query' is undefined"

type byCommitRequest struct {
	OperationName string         `json:"operationName"`
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables"`
}

// byCommitServer answers every request on the programmatic path with the body answer() returns and
// records the last request it decoded.
func byCommitServer(t *testing.T, got *byCommitRequest, answer func() string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ProgrammaticPath {
			w.WriteHeader(404)
			return
		}
		*got = byCommitRequest{}
		_ = json.NewDecoder(r.Body).Decode(got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer()))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "id", "secret", WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func byCommitData(doc any) string {
	b, _ := json.Marshal(map[string]any{"data": map[string]any{"getReleaseByCommitProgrammatic": doc}})
	return string(b)
}

func TestReleaseByCommitPassesBothVariablesThroughAndTheDocumentBack(t *testing.T) {
	const doc = "{\"bomFormat\":\"CycloneDX\",\"specVersion\":\"1.6\",\"components\":[{\"name\":\"caf\u00e9\"}]}"
	var got byCommitRequest
	answer := doc
	c := byCommitServer(t, &got, func() string { return byCommitData(answer) })

	vars := map[string]any{"commit": byCommitSha, "componentId": "my-service"}
	for _, want := range []string{doc, "{}"} {
		answer = want
		data, err := Raw(context.Background(), c, "GetReleaseByCommitProgrammatic", GetReleaseByCommitProgrammatic_Operation, vars)
		if err != nil {
			t.Fatal(err)
		}
		if got.OperationName != "GetReleaseByCommitProgrammatic" {
			t.Fatalf("operation name, got %q", got.OperationName)
		}
		if !strings.Contains(got.Query, "getReleaseByCommitProgrammatic(commit: $commit, componentId: $componentId)") {
			t.Fatalf("the by-commit document, got %q", got.Query)
		}
		if !reflect.DeepEqual(vars, got.Variables) {
			t.Fatalf("variables pass through unchanged:\nwant %v\ngot  %v", vars, got.Variables)
		}
		var out map[string]string
		if err := json.Unmarshal(data, &out); err != nil || out["getReleaseByCommitProgrammatic"] != want {
			t.Fatalf("the document comes back as the server sent it, got %s (%v)", data, err)
		}
	}
}

func TestTheTypedReleaseByCommitDecodesTheMissDocumentAndNull(t *testing.T) {
	var got byCommitRequest
	answer := byCommitData("{}")
	c := byCommitServer(t, &got, func() string { return answer })

	resp, err := GetReleaseByCommitProgrammatic(context.Background(), c, byCommitSha, "my-service")
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetGetReleaseByCommitProgrammatic() == nil || *resp.GetGetReleaseByCommitProgrammatic() != "{}" {
		t.Fatalf("a miss decodes to a pointer to {}, got %v", resp.GetReleaseByCommitProgrammatic)
	}
	want := map[string]any{"commit": byCommitSha, "componentId": "my-service"}
	if got.OperationName != "GetReleaseByCommitProgrammatic" || !reflect.DeepEqual(want, got.Variables) {
		t.Fatalf("the typed call sends the operation and both variables, got %q %v", got.OperationName, got.Variables)
	}

	answer = byCommitData(nil)
	resp, err = GetReleaseByCommitProgrammatic(context.Background(), c, byCommitSha, "my-service")
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetGetReleaseByCommitProgrammatic() != nil {
		t.Fatalf("null decodes to nil, got %q", *resp.GetGetReleaseByCommitProgrammatic())
	}
}

// The checked-in schema is the bundle served at rearm-saas b92d4da7 (the TPF-98 merge), so the
// operation above validates against it (genqlient refuses to generate otherwise).
func TestTheSchemaCarriesTheByCommitFieldsAndType(t *testing.T) {
	sdl, err := os.ReadFile("schema/programmatic.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"getReleaseByCommitProgrammatic(", "getReleasesByCommitProgrammatic(", "type ReleaseByCommitMatch {"} {
		if !strings.Contains(string(sdl), want) {
			t.Fatalf("%s missing from schema/programmatic.graphqls", want)
		}
	}
}

// A server without the field (before TPF-98) refuses the document at validation with one error and
// no data; Raw returns it as GraphQLErrors so the CLI prints the one message.
func TestAnOlderServerRefusesTheByCommitDocumentWithOneFieldUndefinedError(t *testing.T) {
	var got byCommitRequest
	body, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": byCommitFieldUndefined}}})
	c := byCommitServer(t, &got, func() string { return string(body) })

	_, err := Raw(context.Background(), c, "GetReleaseByCommitProgrammatic", GetReleaseByCommitProgrammatic_Operation,
		map[string]any{"commit": byCommitSha, "componentId": "my-service"})
	var gqlErrs GraphQLErrors
	if !errors.As(err, &gqlErrs) {
		t.Fatalf("a GraphQLErrors, got %T %v", err, err)
	}
	if len(gqlErrs) != 1 || gqlErrs[0].Message != byCommitFieldUndefined {
		t.Fatalf("exactly the one FieldUndefined entry, got %v", gqlErrs)
	}
}
