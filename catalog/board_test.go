package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	rearm "github.com/relizaio/rearm-client-go"
)

const sampleBoard = `
kind: BOARD
name: platform
target: platform-api
coordinatorPromptFile: prompts/coordinator.md
settings:
  budgetMicros: null
  cycleCap: 6
roles:
  - name: designer
    promptFile: prompts/designer.md
    orderIndex: 10
  - file: roles/coder.yaml
  - name: reviewer
    prompt: review it
    wipLimit: null
`

// writeTree lays a spec and the files it references out in a temp directory.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadBoardInlinesReferencesAndKeepsNulls(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"boards/platform.yaml":          sampleBoard,
		"boards/prompts/coordinator.md": "coordinate",
		"boards/prompts/designer.md":    "design it",
		"boards/roles/coder.yaml":       "name: coder\nprompt: code it\nwipLimit: 2\n",
	})
	f, err := Load(filepath.Join(dir, "boards/platform.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	b, ok := f.(*BoardFile)
	if !ok {
		t.Fatalf("got %T", f)
	}
	if b.Spec["version"] != 1 {
		t.Errorf("version defaults to 1, got %v", b.Spec["version"])
	}
	if b.Spec["coordinatorPrompt"] != "coordinate" || b.Spec["coordinatorPromptFile"] != nil {
		t.Errorf("coordinator prompt not inlined: %v", b.Spec)
	}
	settings := b.Spec["settings"].(map[string]any)
	if v, present := settings["budgetMicros"]; !present || v != nil {
		t.Errorf("a declared null must stay a present null, got %v (present %v)", v, present)
	}
	roles := b.Spec["roles"].([]any)
	designer := roles[0].(map[string]any)
	if designer["prompt"] != "design it" || designer["promptFile"] != nil {
		t.Errorf("promptFile not inlined: %v", designer)
	}
	coder := roles[1].(map[string]any)
	if coder["name"] != "coder" || coder["prompt"] != "code it" || coder["file"] != nil {
		t.Errorf("file: not inlined: %v", coder)
	}
	reviewer := roles[2].(map[string]any)
	if v, present := reviewer["wipLimit"]; !present || v != nil {
		t.Errorf("role null lost: %v", reviewer)
	}
	if _, present := reviewer["orderIndex"]; present {
		t.Errorf("an absent field must stay absent: %v", reviewer)
	}
	if refs := References(b); len(refs) != 0 {
		t.Errorf("references left: %v", refs)
	}
}

func TestInlineRefusesAReferenceOutsideTheSpecDirectory(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"boards/platform.yaml": "kind: BOARD\nname: p\nroles:\n  - name: x\n    promptFile: ../secret.md\n",
		"secret.md":            "outside",
	})
	if _, err := Load(filepath.Join(dir, "boards/platform.yaml")); err == nil ||
		!strings.Contains(err.Error(), "leaves the spec file's directory") {
		t.Fatalf("want an escape refusal, got %v", err)
	}
	abs := writeTree(t, map[string]string{"p.yaml": "kind: BOARD\nname: p\nroles:\n  - file: /etc/passwd\n"})
	if _, err := Load(filepath.Join(abs, "p.yaml")); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("want an absolute-path refusal, got %v", err)
	}
	missing := writeTree(t, map[string]string{"p.yaml": "kind: BOARD\nname: p\nroles:\n  - file: roles/none.yaml\n"})
	if _, err := Load(filepath.Join(missing, "p.yaml")); err == nil {
		t.Fatal("a reference to nothing must be refused")
	}
}

func TestReferencesNamesWhatAnUploadCannotInline(t *testing.T) {
	f, err := Parse([]byte(sampleBoard))
	if err != nil {
		t.Fatal(err)
	}
	refs := References(f)
	if len(refs) != 3 {
		t.Fatalf("want 3 references, got %v", refs)
	}
	if !strings.Contains(strings.Join(refs, "\n"), "roles[1].file: roles/coder.yaml") {
		t.Errorf("references should name the entry: %v", refs)
	}
}

func TestParseRolePresets(t *testing.T) {
	f, err := Parse([]byte("kind: ROLE_PRESETS\nauthoritative: true\npresets:\n  - name: coder\n    prompt: code\n"))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := f.(*RolePresetsFile)
	if !ok {
		t.Fatalf("got %T", f)
	}
	if p.Spec["authoritative"] != true || len(p.Spec["presets"].([]any)) != 1 {
		t.Errorf("unexpected presets spec: %v", p.Spec)
	}
}

func TestBoardYAMLPutsTheEnvelopeFirst(t *testing.T) {
	f := &BoardFile{Spec: map[string]any{"roles": []any{map[string]any{"prompt": "p", "name": "r"}},
		"name": "platform", "kind": "BOARD", "version": 1, "description": nil}}
	out, err := ToYAML(f)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "kind: BOARD\nversion: 1\nname: platform\n") {
		t.Errorf("envelope order:\n%s", s)
	}
	if strings.Contains(s, "description") {
		t.Errorf("nulls are dropped from exports:\n%s", s)
	}
	if i, j := strings.Index(s, "- name: r"), strings.Index(s, "prompt: p"); i < 0 || j < i {
		t.Errorf("role entries lead with the name:\n%s", s)
	}
}

func TestFormatShowsRolesAndWarnings(t *testing.T) {
	out := Format(&Result{Kind: rearm.DeclarativeKindBoard, Archived: 1, Changes: []Change{
		{Kind: rearm.DeclarativeKindBoard, Name: "platform", Action: rearm.DeclarativeActionUnchanged, Message: "board"},
		{Kind: rearm.DeclarativeKindBoard, Name: "coder", Action: rearm.DeclarativeActionArchive, Fields: []string{"active"},
			Message: "absent from the board's file", Warnings: []string{"2 task(s) wait on it"}},
	}})
	if !strings.Contains(out, "board     platform\n") {
		t.Errorf("board entry:\n%s", out)
	}
	if !strings.Contains(out, "ARCHIVE   role      coder [active] — absent from the board's file\n") {
		t.Errorf("a deactivated role reads as a role:\n%s", out)
	}
	if !strings.Contains(out, "warning: 2 task(s) wait on it") {
		t.Errorf("warnings are printed under the change:\n%s", out)
	}
}

// The day-to-day views print what these operations select, so a trimmed selection hides a board's
// settings or what a role must publish from every CLI reader (gaps §1.19).
func TestTheBoardAndRoleViewsSelectWhatTheCLIShows(t *testing.T) {
	for _, op := range []string{rearm.AgentBoardProgrammatic_Operation, rearm.AgentBoardsProgrammatic_Operation} {
		for _, field := range []string{"target", "budgetMicros", "cycleCap", "completionPriority", "declarative"} {
			if !strings.Contains(op, field) {
				t.Errorf("board operation lost %s", field)
			}
		}
	}
	for _, field := range []string{"hopBudgetMicros", "requiredInputs", "producesOutputs"} {
		if !strings.Contains(rearm.AgentTaskRoleConfigsProgrammatic_Operation, field) {
			t.Errorf("role config list lost %s", field)
		}
	}
}
