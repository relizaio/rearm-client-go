package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rearm "github.com/relizaio/rearm-client-go"
)

// An API_KEYS file (task RD3-11) passes through as written, a declared null kept: the server tells
// clearing a setting from leaving it.
func TestAnApiKeysFileKeepsItsNulls(t *testing.T) {
	f, err := Parse([]byte(`
kind: API_KEYS
keys:
  - name: ci-release
    type: FREEFORM
    permissions:
      type: READ_WRITE
      functions: [BOARD_WRITE]
    sessionMaxMinutes: null
`))
	if err != nil {
		t.Fatal(err)
	}
	spec := f.(*ApiKeysFile).Spec
	if spec["version"] != 1 {
		t.Errorf("version defaults to 1: %v", spec["version"])
	}
	key := spec["keys"].([]any)[0].(map[string]any)
	if v, present := key["sessionMaxMinutes"]; !present || v != nil {
		t.Errorf("a declared null must stay a present null, got %v (present %v)", v, present)
	}
	if _, err := Parse([]byte("kind: NOPE\n")); err == nil || !strings.Contains(err.Error(), "API_KEYS") {
		t.Errorf("the kinds a file may name include API_KEYS: %v", err)
	}
}

// The apply sends the file as the map it was read into, nulls included, and reads the result.
func TestAnApiKeysFileAppliesAsItsMap(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		if !strings.Contains(req.Query, "applyApiKeysProgrammatic") {
			t.Errorf("not the key apply: %s", req.Query)
		}
		sent = req.Variables["spec"].(map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"applyApiKeysProgrammatic": map[string]any{
			"kind": "API_KEYS", "dryRun": true, "specHash": "h", "created": 1, "updated": 0, "unchanged": 0, "archived": 0,
			"errors": 0, "changes": []any{map[string]any{"kind": "API_KEYS", "name": "ci-release", "action": "CREATE",
				"fields": []any{"type"}, "message": nil, "warnings": nil}}}}})
	}))
	defer srv.Close()
	c, err := rearm.New(srv.URL, "id", "secret", rearm.WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	f, _ := Parse([]byte("kind: API_KEYS\nkeys:\n  - name: ci-release\n    type: FREEFORM\n    notes: null\n"))
	res, err := Apply(context.Background(), c, f, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := sent["keys"].([]any)[0].(map[string]any)
	if v, present := key["notes"]; !present || v != nil {
		t.Errorf("the null went over the wire as a null: %v", sent)
	}
	if res.Created != 1 || !strings.Contains(Format(res), "CREATE    api key   ci-release [type]") {
		t.Errorf("result: %s", Format(res))
	}
}

// The export carries no provenance -- the file applies again as it is -- and no empty members; the
// selection has no secret field to read.
func TestTheApiKeysExportAppliesAgainAsItIs(t *testing.T) {
	op := strings.Join(strings.Fields(rearm.ExportApiKeys_Operation), " ")
	for _, never := range []string{"secret ", "secretSlots", "keyId"} {
		if strings.Contains(op, never) {
			t.Errorf("the export selects %q: %s", never, op)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"exportApiKeysProgrammatic": map[string]any{
			"kind": "API_KEYS", "version": 1, "authoritative": true, "keys": []any{
				map[string]any{"name": "ci-release", "type": "FREEFORM", "object": nil, "notes": nil, "status": "ACTIVE",
					"permissions": map[string]any{"type": "READ_WRITE", "functions": []any{"BOARD_WRITE"}, "approvals": nil, "objects": nil},
					"provenance": map[string]any{"specHash": "h"}},
			}}}})
	}))
	defer srv.Close()
	c, err := rearm.New(srv.URL, "id", "secret", rearm.WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	f, err := ExportApiKeys(context.Background(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := f.Spec["keys"].([]any)[0].(map[string]any)
	if _, has := key["provenance"]; has {
		t.Errorf("provenance is left out: %v", key)
	}
	if _, has := key["notes"]; has {
		t.Errorf("an empty member is left out: %v", key)
	}
	y, err := ToYAML(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(y), "kind: API_KEYS\nversion: 1\nauthoritative: true\nkeys:\n    - name: ci-release\n      type: FREEFORM\n") {
		t.Errorf("the envelope first, then each key by name and type:\n%s", y)
	}
}
