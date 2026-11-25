package rearm

import (
	"os"
	"reflect"
	"regexp"
	"testing"
)

// The schema refreshed from rearm-saas 2026-11-safety-vex (task SAFE-15, design SAFE-9 3.3 item 1,
// test 4.1 g6): the generated code carries the artifact types and import fields the CLI sends, and
// the checked-in schema the summary fields the import operation selects.

func TestRefreshedSchemaCarriesArtifactTypesAndImportFields(t *testing.T) {
	types := map[ArtifactType]bool{}
	for _, v := range AllArtifactType {
		types[v] = true
	}
	for _, want := range []ArtifactType{"SAFETY_CONTEXT", "UNKNOWN"} {
		if !types[want] {
			t.Fatalf("AllArtifactType lacks %s", want)
		}
	}

	input := reflect.TypeOf(ArtifactInput{})
	for _, f := range []string{"ImportScope", "ImportMode", "UserIssuerClassOverride"} {
		if _, ok := input.FieldByName(f); !ok {
			t.Fatalf("ArtifactInput lacks %s", f)
		}
	}

	sdl, err := os.ReadFile("schema/programmatic.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?s)\ntype ImportSummary \{(.*?)\n\}`).FindStringSubmatch(string(sdl))
	if body == nil {
		t.Fatal("type ImportSummary not found in the schema")
	}
	for _, f := range []string{"statementsUnchanged", "demotions", "overrideOutcome"} {
		if !regexp.MustCompile(`(?m)^\s*` + f + `\s*:`).MatchString(body[1]) {
			t.Fatalf("type ImportSummary lacks %s:%s", f, body[1])
		}
	}
}
