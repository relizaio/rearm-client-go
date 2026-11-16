package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestIsNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"classified", gqlerror.List{&gqlerror.Error{Message: "no such thing", Extensions: map[string]interface{}{"errorType": "NOT_FOUND"}}}, true},
		{"message", gqlerror.List{&gqlerror.Error{Message: "Component not found in this organization: x"}}, true},
		{"other gql error", gqlerror.List{&gqlerror.Error{Message: "Not authorized", Extensions: map[string]interface{}{"errorType": "PERMISSION_DENIED"}}}, false},
		{"wrapped list", fmt.Errorf("export: %w", gqlerror.List{&gqlerror.Error{Message: "branch not found"}}), true},
		{"plain", errors.New("connection refused"), false},
	}
	for _, c := range cases {
		if got := IsNotFound(c.err); got != c.want {
			t.Errorf("%s: IsNotFound = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsNotFoundReadsTheClassification(t *testing.T) {
	ext := func(k string, v interface{}) map[string]interface{} { return map[string]interface{}{k: v} }
	// The errors array as the server sends it for handleNotFound (InstanceDataFetcher "Wrong
	// deliverable"), decoded the way Raw and UploadMultipart decode it.
	var wire rearm.GraphQLErrors
	if err := json.Unmarshal([]byte(`[{"message":"Wrong deliverable","path":["planted"],"extensions":{"classification":"NOT_FOUND"}}]`), &wire); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// (a) to (j): the typed functions' errors (gqlerror).
		{"a wire list", gqlerror.List{&gqlerror.Error{Message: "planted miss", Extensions: ext("classification", "NOT_FOUND")}}, true},
		{"b wire single", &gqlerror.Error{Message: "planted miss", Extensions: ext("classification", "NOT_FOUND")}, true},
		{"c wrapped single", fmt.Errorf("x: %w", &gqlerror.Error{Message: "planted miss", Extensions: ext("classification", "NOT_FOUND")}), true},
		{"d lower", gqlerror.List{&gqlerror.Error{Message: "planted miss", Extensions: ext("classification", "not_found")}}, true},
		{"e second entry", gqlerror.List{&gqlerror.Error{Message: "planted refusal", Extensions: ext("classification", "BAD_REQUEST")}, &gqlerror.Error{Message: "planted miss", Extensions: ext("classification", "NOT_FOUND")}}, true},
		{"f bad request", gqlerror.List{&gqlerror.Error{Message: "planted refusal", Extensions: ext("classification", "BAD_REQUEST")}}, false},
		{"g denied", gqlerror.List{&gqlerror.Error{Message: "Not authorized", Extensions: ext("classification", "DataFetchingException")}}, false},
		{"h single bad request", &gqlerror.Error{Message: "planted refusal", Extensions: ext("classification", "BAD_REQUEST")}, false},
		{"i non-string", gqlerror.List{&gqlerror.Error{Message: "planted miss", Extensions: ext("classification", 404)}}, false},
		{"j legacy single errorType", &gqlerror.Error{Message: "planted miss", Extensions: ext("errorType", "NOT_FOUND")}, true},
		// (k) to (q): the errors of Raw and UploadMultipart (rearm.GraphQLErrors).
		{"k raw from the wire", wire, true},
		{"l raw wrapped", fmt.Errorf("x: %w", wire), true},
		{"m raw lower", rearm.GraphQLErrors{{Message: "planted miss", Extensions: ext("classification", "not_found")}}, true},
		{"n raw second entry", rearm.GraphQLErrors{{Message: "planted refusal", Extensions: ext("classification", "BAD_REQUEST")}, {Message: "planted miss", Extensions: ext("classification", "NOT_FOUND")}}, true},
		{"o raw legacy errorType", rearm.GraphQLErrors{{Message: "planted miss", Extensions: ext("errorType", "NOT_FOUND")}}, true},
		{"p raw bad request", rearm.GraphQLErrors{{Message: "planted refusal", Extensions: ext("classification", "BAD_REQUEST")}}, false},
		{"q raw non-string", rearm.GraphQLErrors{{Message: "planted miss", Extensions: ext("classification", float64(404))}}, false},
		{"r raw message only", rearm.GraphQLErrors{{Message: "Component not found in this organization: x"}}, true},
	}
	for _, c := range cases {
		if got := IsNotFound(c.err); got != c.want {
			t.Errorf("%s: IsNotFound = %v, want %v", c.name, got, c.want)
		}
	}
}
