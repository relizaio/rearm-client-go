package catalog

import (
	"errors"
	"fmt"
	"testing"

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
