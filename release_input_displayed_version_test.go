package rearm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// captureVariables is a graphql.Client that records the JSON of a request's variables, exactly
// as the HTTP client would put them in the body, and sends nothing.
type captureVariables struct{ body map[string]json.RawMessage }

var errCaptured = errors.New("captured, not sent")

func (c *captureVariables) MakeRequest(_ context.Context, req *graphql.Request, _ *graphql.Response) error {
	b, err := json.Marshal(req.Variables)
	if err != nil {
		return err
	}
	c.body = map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &c.body); err != nil {
		return err
	}
	return errCaptured
}

func TestTypedReleaseCreatesLeaveOutANilDisplayedVersion(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name    string
		dv      *string
		present bool
		want    string
	}{
		{"nil", nil, false, ""},
		{"set", str("1.2 (build 7)"), true, `"1.2 (build 7)"`},
		{"empty clears on a rebuild", str(""), true, `""`},
	} {
		in := func() *ReleaseInputProg {
			return &ReleaseInputProg{Version: "planted-1.0.0", Component: str("planted-component"), DisplayedVersion: tc.dv}
		}
		check := func(op string, release json.RawMessage) {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(release, &m); err != nil {
				t.Fatalf("%s %s: %v", op, tc.name, err)
			}
			if _, ok := m["version"]; !ok {
				t.Fatalf("%s %s: the capture did not see the release input: %s", op, tc.name, release)
			}
			got, ok := m["displayedVersion"]
			if ok != tc.present || (ok && string(got) != tc.want) {
				t.Errorf("%s %s: displayedVersion present=%v value=%s, want present=%v value=%s", op, tc.name, ok, got, tc.present, tc.want)
			}
		}
		c := &captureVariables{}
		if _, err := AddReleaseProgrammatic(context.Background(), c, in()); !errors.Is(err, errCaptured) {
			t.Fatalf("single %s: %v", tc.name, err)
		}
		check("single", c.body["releaseInputProg"])
		c = &captureVariables{}
		if _, err := AddReleasesProgrammatic(context.Background(), c, []*ReleaseInputProg{in(), in()}); !errors.Is(err, errCaptured) {
			t.Fatalf("batch %s: %v", tc.name, err)
		}
		var list []json.RawMessage
		if err := json.Unmarshal(c.body["releaseInputsProg"], &list); err != nil || len(list) != 2 {
			t.Fatalf("batch %s: releaseInputsProg %s (%v)", tc.name, c.body["releaseInputsProg"], err)
		}
		for _, r := range list {
			check("batch", r)
		}
	}
}
