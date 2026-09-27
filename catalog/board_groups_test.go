package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	rearm "github.com/relizaio/rearm-client-go"
)

// A board file's groups (task RD2-30) pass through as written, a declared null kept, so the server
// can tell clearing a member from leaving it.
func TestABoardFileCarriesItsGroupsAsWritten(t *testing.T) {
	f, err := Parse([]byte(`
kind: BOARD
name: platform
groups:
  - key: core-work
    name: The core
  - key: ui-work
    dependsOn: [core-work]
    defaultLevel: null
`))
	if err != nil {
		t.Fatal(err)
	}
	groups := f.(*BoardFile).Spec["groups"].([]any)
	if len(groups) != 2 || groups[0].(map[string]any)["name"] != "The core" {
		t.Fatalf("groups read as %v", groups)
	}
	if v, present := groups[1].(map[string]any)["defaultLevel"]; !present || v != nil {
		t.Errorf("a declared null must stay a present null, got %v (present %v)", v, present)
	}
}

// The export selects the groups and keeps their order; the YAML leaves out a group's empty members.
func TestTheBoardExportCarriesTheGroupsInOrder(t *testing.T) {
	op := strings.Join(strings.Fields(rearm.ExportBoard_Operation), " ")
	if !strings.Contains(op, "groups { key name description dependsOn defaultLevel status }") {
		t.Fatalf("the export does not select the groups: %s", op)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"exportBoardProgrammatic": map[string]any{
			"kind": "BOARD", "version": 1, "name": "platform", "groups": []any{
				map[string]any{"key": "ui-work", "dependsOn": []any{"core-work"}, "status": "OPEN", "name": nil},
				map[string]any{"key": "core-work", "name": "The core", "status": "CLOSED"},
			}}}})
	}))
	defer srv.Close()
	c, err := rearm.New(srv.URL, "id", "secret", rearm.WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	f, err := ExportBoard(context.Background(), c, "platform")
	if err != nil {
		t.Fatal(err)
	}
	groups := f.Spec["groups"].([]any)
	var keys []string
	for _, g := range groups {
		keys = append(keys, g.(map[string]any)["key"].(string))
	}
	if !reflect.DeepEqual(keys, []string{"ui-work", "core-work"}) {
		t.Errorf("groups in the order %v", keys)
	}
	y, err := ToYAML(f)
	if err != nil {
		t.Fatal(err)
	}
	s := string(y)
	if !strings.Contains(s, "groups:\n    - key: ui-work\n      dependsOn:\n        - core-work\n      status: OPEN\n"+
		"    - key: core-work\n      name: The core\n") {
		t.Errorf("the ui group's empty members are not left out:\n%s", s)
	}
	if !strings.Contains(s, "status: CLOSED") {
		t.Errorf("a closed group is exported:\n%s", s)
	}
}

func TestFormatShowsGroupChangesAsGroups(t *testing.T) {
	out := Format(&Result{Kind: rearm.DeclarativeKindBoard, Changes: []Change{
		{Kind: rearm.DeclarativeKindBoard, Name: "core-work", Action: rearm.DeclarativeActionCreate, Message: "group"},
		{Kind: rearm.DeclarativeKindBoard, Name: "ui-work", Action: rearm.DeclarativeActionArchive, Fields: []string{"status"},
			Message: "group absent from the board's file; closed, not deleted"},
		{Kind: rearm.DeclarativeKindBoard, Name: "platform", Action: rearm.DeclarativeActionError,
			Message: "group cycle: core-work → ui-work → core-work"},
	}})
	for _, want := range []string{
		"CREATE    group     core-work\n",
		"ARCHIVE   group     ui-work [status] — group absent from the board's file; closed, not deleted\n",
		"ERROR     board     platform — group cycle: core-work → ui-work → core-work\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
