package rearm

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

const singleNote = "Actions that rules fire when the release is saved (integration triggers, external validations, emails, pull request comments, VDR snapshots) and the pull request check run are sent at that point and are not recalled when the call is refused afterwards."
const batchNote = "Actions that rules fire when a release is saved (integration triggers, external validations, emails, pull request comments, VDR snapshots) and the pull request check run are sent as each release is saved and are not recalled when a later release of the batch is refused."

var ws = regexp.MustCompile(`\s+`)

func fold(s string) string { return strings.TrimSpace(ws.ReplaceAllString(s, " ")) }

func TestTheSchemaCopyIsTheBundleAfterFDAW93(t *testing.T) {
	sdl, err := os.ReadFile("schema/programmatic.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	doc, gerr := parser.ParseSchema(&ast.Source{Name: "programmatic.graphqls", Input: string(sdl)})
	if gerr != nil {
		t.Fatal(gerr)
	}
	defs := func(name string) []*ast.Definition {
		var out []*ast.Definition
		for _, d := range append(append(ast.DefinitionList{}, doc.Definitions...), doc.Extensions...) {
			if d.Name == name {
				out = append(out, d)
			}
		}
		return out
	}
	field := func(typ, name string) *ast.FieldDefinition {
		for _, d := range defs(typ) {
			if f := d.Fields.ForName(name); f != nil {
				return f
			}
		}
		return nil
	}
	if field("Submission", "lastActKind") != nil {
		t.Error("Submission.lastActKind is still in the copy")
	}
	if f := field("Submission", "lastAct"); f == nil || f.Type.String() != "SubmissionActRef" {
		t.Error("Submission.lastAct: SubmissionActRef is missing")
	}
	if len(defs("SubmissionActOutcome")) != 0 {
		t.Error("enum SubmissionActOutcome is still in the copy")
	}
	for _, m := range []string{"createSubmission", "resubmitSubmission", "withdrawSubmission"} {
		if field("Mutation", m) != nil {
			t.Errorf("Mutation.%s is in the copy", m)
		}
	}
	if field("SubmissionDraft", "readiness") == nil {
		t.Error("SubmissionDraft.readiness is missing: the copy predates the FDAW-109 merge")
	}
	if f := field("Mutation", "addReleaseProgrammatic"); f == nil || fold(f.Description) != "Creates one release from a build. "+singleNote {
		t.Error("addReleaseProgrammatic does not carry SINGLE_NOTE")
	}
	if f := field("Mutation", "addReleasesProgrammatic"); f == nil || !strings.Contains(fold(f.Description), "none are persisted. "+batchNote+" Auto-integration of products") {
		t.Error("addReleasesProgrammatic does not carry BATCH_NOTE in place")
	}
	gen, err := os.ReadFile("generated.go")
	if err != nil {
		t.Fatal(err)
	}
	block := func(typ string) string {
		s := string(gen)
		i := strings.Index(s, "type "+typ+" struct {")
		if i < 0 {
			return ""
		}
		j := strings.Index(s[i:], "\n}")
		var b strings.Builder
		for _, l := range strings.Split(s[i:i+j], "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "//") {
				b.WriteString(strings.TrimPrefix(strings.TrimPrefix(l, "//"), " ") + " ")
			}
		}
		return fold(b.String())
	}
	if got := block("AddReleaseProgrammaticResponse"); got != "Creates one release from a build. "+singleNote {
		t.Errorf("generated AddReleaseProgrammaticResponse comment: %q", got)
	}
	if !strings.Contains(block("AddReleasesProgrammaticResponse"), "none are persisted. "+batchNote+" Auto-integration of products") {
		t.Error("generated AddReleasesProgrammaticResponse comment lacks BATCH_NOTE in place")
	}
}
