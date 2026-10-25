package rearm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// The served prompt by version (task TPF-110, child 2 of TPF-97): four operations, each its legacy twin
// with the text field replaced by the by-version field and a $heldVersions variable bound to it.

const pbvVersion = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type pbvOp struct {
	name, document, legacy, byVersion string
	hasPromptVersion                  bool
}

var pbvOps = []pbvOp{
	{"AgentTaskNextByPromptVersion", AgentTaskNextByPromptVersion_Operation, AgentTaskNextProgrammatic_Operation,
		"promptByVersion(heldVersions: $heldVersions) { version status text }", true},
	{"AgentTaskAssignByPromptVersion", AgentTaskAssignByPromptVersion_Operation, AgentTaskAssignProgrammatic_Operation,
		"promptByVersion(heldVersions: $heldVersions) { version status text }", true},
	{"AgentRoleBriefByPromptVersion", AgentRoleBriefByPromptVersion_Operation, AgentRoleBriefProgrammatic_Operation,
		"promptByVersion(heldVersions: $heldVersions) { version status text }", true},
	{"AgentBoardCoordinateByPromptVersion", AgentBoardCoordinateByPromptVersion_Operation, AgentBoardCoordinateProgrammatic_Operation,
		"coordinatorPromptByVersion(heldVersions: $heldVersions) { version status text }", false},
}

// taskBlock is the operation's `task { ... }` selection, normalised, up to its matching brace.
func taskBlock(t *testing.T, op string) string {
	n := normalised(op)
	i := strings.Index(n, "task {")
	if i < 0 {
		t.Fatalf("no task block in %q", n)
	}
	depth := 0
	for j := i; j < len(n); j++ {
		switch n[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return n[i : j+1]
			}
		}
	}
	t.Fatalf("unclosed task block in %q", n)
	return ""
}

func TestTheByVersionOperationsSelectTheVersionedFieldAndNoText(t *testing.T) {
	for _, o := range pbvOps {
		op := normalised(o.document)
		if !strings.Contains(op, "$heldVersions: [String!]") {
			t.Errorf("%s does not declare $heldVersions: [String!]", o.name)
		}
		if !strings.Contains(op, o.byVersion) {
			t.Errorf("%s does not select %q", o.name, o.byVersion)
		}
		if o.hasPromptVersion && !strings.Contains(op, "promptVersion") {
			t.Errorf("%s no longer selects the pin promptVersion", o.name)
		}
		// The operation text minus the by-version selection must name no text field.
		rest := strings.Replace(op, o.byVersion, "", 1)
		for _, text := range []string{"rolePrompt", "servedPrompt", "servedCoordinatorPrompt", "coordinatorPrompt"} {
			if strings.Contains(rest, text) {
				t.Errorf("%s selects the text field %s", o.name, text)
			}
		}
	}
	if taskBlock(t, AgentTaskNextByPromptVersion_Operation) != taskBlock(t, AgentTaskNextProgrammatic_Operation) {
		t.Error("the next task block differs from AgentTaskNextProgrammatic's")
	}
	assign := taskBlock(t, AgentTaskAssignByPromptVersion_Operation)
	if assign != taskBlock(t, AgentTaskAssignProgrammatic_Operation) || !strings.Contains(assign, "documents {") {
		t.Error("the assign task block differs from AgentTaskAssignProgrammatic's, or lost its documents")
	}
	legacy := strings.Replace(normalised(AgentBoardCoordinateProgrammatic_Operation), "coordinatorPrompt servedCoordinatorPrompt ", "", 1)
	legacy = legacy[strings.Index(legacy, "agentBoardCoordinateProgrammatic("):]
	byVersion := strings.Replace(normalised(AgentBoardCoordinateByPromptVersion_Operation),
		"coordinatorPromptByVersion(heldVersions: $heldVersions) { version status text } ", "", 1)
	byVersion = byVersion[strings.Index(byVersion, "agentBoardCoordinateProgrammatic("):]
	if legacy != byVersion {
		t.Errorf("the coordinate selection differs from the legacy one beyond the prompt fields:\nlegacy %s\nnew    %s", legacy, byVersion)
	}
}

func pbvServer(t *testing.T, body func() string, got *map[string]any) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ProgrammaticPath {
			w.WriteHeader(404)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if got != nil {
			*got = req
		}
		_, _ = w.Write([]byte(body()))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "id", "secret", WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func servedJSON(status string, text *string) string {
	b, _ := json.Marshal(map[string]any{"version": pbvVersion, "status": status, "text": text})
	return string(b)
}

func TestTheTypedByVersionResponsesDecodeServedAndUnchanged(t *testing.T) {
	ctx := context.Background()
	text := "the served prompt"
	for _, tc := range []struct {
		status string
		text   *string
		want   ServedPromptStatus
	}{{"UNCHANGED", nil, ServedPromptStatusUnchanged}, {"SERVED", &text, ServedPromptStatusServed}} {
		pv := servedJSON(tc.status, tc.text)
		var req map[string]any

		c := pbvServer(t, func() string {
			return `{"data":{"agentTaskNextProgrammatic":{"role":"coder","promptVersion":"abc","promptByVersion":` + pv + `}}}`
		}, &req)
		next, err := AgentTaskNextByPromptVersion(ctx, c, "s-1", nil, nil, []string{pbvVersion})
		if err != nil {
			t.Fatal(err)
		}
		p := next.GetAgentTaskNextProgrammatic().GetPromptByVersion()
		checkServed(t, "next", p.GetVersion(), p.GetStatus(), p.GetText(), tc.want, tc.text)
		if v, _ := req["variables"].(map[string]any); !reflect.DeepEqual(v["heldVersions"], []any{pbvVersion}) {
			t.Errorf("the typed next sends heldVersions, got %v", req["variables"])
		}

		c = pbvServer(t, func() string {
			return `{"data":{"agentTaskAssignProgrammatic":{"role":"coder","promptVersion":"abc","promptByVersion":` + pv + `}}}`
		}, nil)
		assign, err := AgentTaskAssignByPromptVersion(ctx, c, "t-1", "s-1", nil, []string{pbvVersion})
		if err != nil {
			t.Fatal(err)
		}
		pa := assign.GetAgentTaskAssignProgrammatic().GetPromptByVersion()
		checkServed(t, "assign", pa.GetVersion(), pa.GetStatus(), pa.GetText(), tc.want, tc.text)

		c = pbvServer(t, func() string {
			return `{"data":{"agentBoardCoordinateProgrammatic":{"uuid":"b-1","coordinatorPromptByVersion":` + pv + `}}}`
		}, nil)
		board, err := AgentBoardCoordinateByPromptVersion(ctx, c, "b-1", "s-1", []string{pbvVersion})
		if err != nil {
			t.Fatal(err)
		}
		pc := board.GetAgentBoardCoordinateProgrammatic().GetCoordinatorPromptByVersion()
		checkServed(t, "coordinate", pc.GetVersion(), pc.GetStatus(), pc.GetText(), tc.want, tc.text)

		c = pbvServer(t, func() string {
			return `{"data":{"agentTaskRoleConfigsProgrammatic":[{"uuid":"r-1","name":"coder","promptVersion":"abc","promptByVersion":` + pv + `}]}}`
		}, nil)
		brief, err := AgentRoleBriefByPromptVersion(ctx, c, "b-1", []string{pbvVersion})
		if err != nil {
			t.Fatal(err)
		}
		rows := brief.GetAgentTaskRoleConfigsProgrammatic()
		if len(rows) != 1 {
			t.Fatalf("one role row, got %d", len(rows))
		}
		pb := rows[0].GetPromptByVersion()
		checkServed(t, "brief", pb.GetVersion(), pb.GetStatus(), pb.GetText(), tc.want, tc.text)
		_ = rows[0].GetPromptVersion()
	}
}

func checkServed(t *testing.T, verb, version string, status ServedPromptStatus, text *string, want ServedPromptStatus, wantText *string) {
	t.Helper()
	if version != pbvVersion || status != want {
		t.Errorf("%s: version %q status %q, want %q %q", verb, version, status, pbvVersion, want)
	}
	if (wantText == nil) != (text == nil) || (text != nil && *text != *wantText) {
		t.Errorf("%s: text %v, want %v", verb, text, wantText)
	}
}

func TestTheByVersionOperationsPassHeldVersionsThroughRaw(t *testing.T) {
	var got map[string]any
	c := pbvServer(t, func() string { return `{"data":{}}` }, &got)
	for _, tc := range []struct {
		name, op string
		vars     map[string]any
	}{
		{"AgentTaskNextByPromptVersion", AgentTaskNextByPromptVersion_Operation, map[string]any{"sessionUuid": "s-1", "boardUuid": nil}},
		{"AgentTaskAssignByPromptVersion", AgentTaskAssignByPromptVersion_Operation, map[string]any{"taskUuid": "t-1", "sessionUuid": "s-1"}},
		{"AgentBoardCoordinateByPromptVersion", AgentBoardCoordinateByPromptVersion_Operation, map[string]any{"boardUuid": "b-1", "sessionUuid": "s-1"}},
		{"AgentRoleBriefByPromptVersion", AgentRoleBriefByPromptVersion_Operation, map[string]any{"boardUuid": "b-1"}},
	} {
		with := map[string]any{"heldVersions": []any{"v1", "v2"}}
		for k, v := range tc.vars {
			with[k] = v
		}
		for _, vars := range []map[string]any{with, tc.vars} {
			got = nil
			if _, err := Raw(context.Background(), c, tc.name, tc.op, vars); err != nil {
				t.Fatal(err)
			}
			if got["operationName"] != tc.name {
				t.Errorf("%s: operationName %v", tc.name, got["operationName"])
			}
			sent, _ := got["variables"].(map[string]any)
			if !reflect.DeepEqual(vars, sent) {
				t.Errorf("%s: variables pass through unchanged:\nwant %v\ngot  %v", tc.name, vars, sent)
			}
			if _, has := vars["heldVersions"]; !has {
				if _, sentIt := sent["heldVersions"]; sentIt {
					t.Errorf("%s: an absent heldVersions is sent", tc.name)
				}
			}
		}
	}
}

func TestTheSchemaBundleCarriesTheServedPromptByVersion(t *testing.T) {
	b, err := os.ReadFile("schema/programmatic.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	sdl := string(b)
	for _, want := range []string{"type ServedPrompt {", "enum ServedPromptStatus {", "coordinatorPromptByVersion("} {
		if !strings.Contains(sdl, want) {
			t.Errorf("the bundle lacks %q", want)
		}
	}
	// AstPrinter puts field arguments on their own lines (TPF-109 F-4): compare without whitespace.
	squeezed := strings.Join(strings.Fields(sdl), "")
	if n := strings.Count(squeezed, "promptByVersion(heldVersions:[String!]):ServedPrompt"); n != 2 {
		t.Errorf("promptByVersion(heldVersions: [String!]): ServedPrompt twice (assignment, role config), got %d", n)
	}
	if n := strings.Count(squeezed, "coordinatorPromptByVersion(heldVersions:[String!]):ServedPrompt"); n != 1 {
		t.Errorf("coordinatorPromptByVersion(heldVersions: [String!]): ServedPrompt once, got %d", n)
	}
}

// The legacy operations stay as they were at 8ca23f7: older CLIs and the CLI's older-server fallback send them.
func TestTheLegacyPromptOperationsAreUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		wants    []string
	}{
		{"AgentTaskNextProgrammatic", AgentTaskNextProgrammatic_Operation, []string{"role rolePrompt promptVersion hopBudgetMicros"}},
		{"AgentTaskAssignProgrammatic", AgentTaskAssignProgrammatic_Operation, []string{"role rolePrompt promptVersion hopBudgetMicros",
			"documents { uuid version lifecycle document { specification path round task advisory publishedByRole } }"}},
		{"AgentBoardCoordinateProgrammatic", AgentBoardCoordinateProgrammatic_Operation, []string{"coordinatorPrompt servedCoordinatorPrompt missingCapabilities"}},
		{"AgentRoleBriefProgrammatic", AgentRoleBriefProgrammatic_Operation, []string{"servedPrompt promptVersion requiredCapabilities"}},
		{"AgentTaskRoleConfigsProgrammatic", AgentTaskRoleConfigsProgrammatic_Operation, []string{"uuid board org name prompt orderIndex"}},
	} {
		op := normalised(tc.op)
		for _, w := range tc.wants {
			if !strings.Contains(op, w) {
				t.Errorf("%s lost %q", tc.name, w)
			}
		}
		if strings.Contains(op, "ByVersion") || strings.Contains(op, "heldVersions") {
			t.Errorf("%s selects a by-version field", tc.name)
		}
	}
}

// The root field of each by-version operation takes exactly its twin's arguments, each its own variable;
// $heldVersions is declared once, as [String!], and bound once, to the by-version field. These operations are
// not in TestEachOperationArgumentIsItsOwnVariableAndTheSchemaFieldsArgumentsAreAllThere: it requires one
// variable per root argument, and $heldVersions binds a type-field argument.
func TestTheByVersionRootArgumentsAreTheTwinsAndHeldVersionsBindsTheVersionedField(t *testing.T) {
	sdl, err := os.ReadFile("schema/programmatic.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	schema, gerr := parser.ParseSchema(&ast.Source{Name: "programmatic.graphqls", Input: string(sdl)})
	if gerr != nil {
		t.Fatal(gerr)
	}
	rootArgs := func(name string) []string {
		for _, def := range append(append(ast.DefinitionList{}, schema.Definitions...), schema.Extensions...) {
			if def.Name == "Query" || def.Name == "Mutation" {
				if f := def.Fields.ForName(name); f != nil {
					var out []string
					for _, a := range f.Arguments {
						out = append(out, a.Name)
					}
					sort.Strings(out)
					return out
				}
			}
		}
		t.Fatalf("%s is not a root field", name)
		return nil
	}
	for _, o := range pbvOps {
		doc, gerr := parser.ParseQuery(&ast.Source{Input: o.document})
		if gerr != nil {
			t.Fatal(gerr)
		}
		if len(doc.Operations) != 1 || len(doc.Operations[0].SelectionSet) != 1 {
			t.Fatalf("%s: one operation selecting one root field", o.name)
		}
		operation := doc.Operations[0]
		root, ok := operation.SelectionSet[0].(*ast.Field)
		if !ok {
			t.Fatalf("%s: the root selection is a field", o.name)
		}
		var got []string
		for _, a := range root.Arguments {
			got = append(got, a.Name)
			if a.Value.Kind != ast.Variable || a.Value.Raw != a.Name {
				t.Errorf("%s: %s(%s:) is %s, want $%s", o.name, root.Name, a.Name, a.Value.String(), a.Name)
			}
		}
		sort.Strings(got)
		if want := rootArgs(root.Name); !reflect.DeepEqual(want, got) {
			t.Errorf("%s: the root arguments\nwant %v\ngot  %v", o.name, want, got)
		}
		held := 0
		for _, v := range operation.VariableDefinitions {
			if v.Variable == "heldVersions" {
				held++
				if v.Type.String() != "[String!]" {
					t.Errorf("%s: $heldVersions is %s", o.name, v.Type.String())
				}
			} else if !contains(got, v.Variable) {
				t.Errorf("%s: $%s binds no root argument", o.name, v.Variable)
			}
		}
		if held != 1 || len(operation.VariableDefinitions) != len(got)+1 {
			t.Errorf("%s: the root's variables plus $heldVersions once, got %d definitions", o.name, len(operation.VariableDefinitions))
		}
		uses := heldVersionUses(root.SelectionSet)
		wantField := "promptByVersion"
		if o.name == "AgentBoardCoordinateByPromptVersion" {
			wantField = "coordinatorPromptByVersion"
		}
		if !reflect.DeepEqual(uses, []string{wantField}) {
			t.Errorf("%s: $heldVersions is used by %v, want only %s(heldVersions:)", o.name, uses, wantField)
		}
	}
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// heldVersionUses names every field in the selection whose argument takes $heldVersions.
func heldVersionUses(set ast.SelectionSet) []string {
	var out []string
	for _, s := range set {
		f, ok := s.(*ast.Field)
		if !ok {
			continue
		}
		for _, a := range f.Arguments {
			if a.Value.Kind == ast.Variable && a.Value.Raw == "heldVersions" {
				if a.Name == "heldVersions" {
					out = append(out, f.Name)
				} else {
					out = append(out, f.Name+"("+a.Name+":)")
				}
			}
		}
		out = append(out, heldVersionUses(f.SelectionSet)...)
	}
	return out
}
