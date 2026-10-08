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

// The merged release SBOM operations (task SCORE-24): the CLI sends them raw with every option as a
// variable, nulls included, and prints the root field's string as the server produced it.

func TestReleaseSbomOperationsPassEveryVariableThroughAndTheDocumentBack(t *testing.T) {
	const doc = "{\"bomFormat\":\"CycloneDX\",\"components\":[{\"name\":\"caf\u00e9\"}]}"
	var got struct {
		OperationName string         `json:"operationName"`
		Query         string         `json:"query"`
		Variables     map[string]any `json:"variables"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ProgrammaticPath {
			w.WriteHeader(404)
			return
		}
		got.OperationName, got.Query, got.Variables = "", "", nil
		_ = json.NewDecoder(r.Body).Decode(&got)
		root := "releaseSbomExportProgrammatic"
		if strings.HasPrefix(strings.TrimSpace(got.Query), "query") {
			root = "releaseSbomScoreProgrammatic"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{root: doc}})
	}))
	defer srv.Close()
	c, err := New(srv.URL, "id", "secret", WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}

	vars := map[string]any{"release": "r-1", "tldOnly": true, "ignoreDev": false, "structure": "FLAT",
		"belongsTo": nil, "mediaType": "JSON", "excludeCoverageTypes": []any{"DEV", "TEST", "BUILD_TIME"},
		"includeSupportMetadata": false, "includeInternalMetadata": false, "excludeFileComponents": false}
	data, err := Raw(context.Background(), c, "ReleaseSbomExportProgrammatic", ReleaseSbomExportProgrammatic_Operation, vars)
	if err != nil {
		t.Fatal(err)
	}
	if got.OperationName != "ReleaseSbomExportProgrammatic" || !strings.Contains(got.Query, "releaseSbomExportProgrammatic(release: $release") {
		t.Fatalf("the export operation, got %q %q", got.OperationName, got.Query)
	}
	if !reflect.DeepEqual(vars, got.Variables) {
		t.Fatalf("variables pass through unchanged, nulls included:\nwant %v\ngot  %v", vars, got.Variables)
	}
	var out map[string]string
	if err := json.Unmarshal(data, &out); err != nil || out["releaseSbomExportProgrammatic"] != doc {
		t.Fatalf("the document comes back as the server sent it, got %s (%v)", data, err)
	}

	scoreVars := map[string]any{"componentId": "my-product", "version": "1.2.3", "includeSupportMetadata": nil,
		"includeInternalMetadata": nil, "profiles": []any{"cisa-2026", "fda"}}
	if _, err := Raw(context.Background(), c, "ReleaseSbomScoreProgrammatic", ReleaseSbomScoreProgrammatic_Operation, scoreVars); err != nil {
		t.Fatal(err)
	}
	if got.OperationName != "ReleaseSbomScoreProgrammatic" || !reflect.DeepEqual(scoreVars, got.Variables) {
		t.Fatalf("the score operation and its variables, got %q %v", got.OperationName, got.Variables)
	}
}

// The checked-in schema is the served programmatic contract with both fields and their enums, so the
// operations above validate against it (genqlient refuses to generate otherwise).
func TestTheSchemaCarriesTheReleaseSbomFieldsAndEnums(t *testing.T) {
	sdl, err := os.ReadFile("schema/programmatic.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"releaseSbomExportProgrammatic(", "releaseSbomScoreProgrammatic(", "enum BomStructureType {",
		"enum ArtifactBelongsToEnum {", "enum BomMediaType {", "enum ArtifactCoverageType {"} {
		if !strings.Contains(string(sdl), want) {
			t.Fatalf("%s missing from schema/programmatic.graphqls", want)
		}
	}
}

// Each operation the CLI sends hands every argument of its root field the variable of the same
// name, declared with the schema's type, and leaves none out (design T-6): a rewired,
// dropped or extra argument would still send the right variables and get another document.
func TestEachOperationArgumentIsItsOwnVariableAndTheSchemaFieldsArgumentsAreAllThere(t *testing.T) {
	sdl, err := os.ReadFile("schema/programmatic.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	schema, gerr := parser.ParseSchema(&ast.Source{Name: "programmatic.graphqls", Input: string(sdl)})
	if gerr != nil {
		t.Fatal(gerr)
	}
	schemaField := func(name string) *ast.FieldDefinition {
		for _, def := range append(append(ast.DefinitionList{}, schema.Definitions...), schema.Extensions...) {
			if def.Name == "Query" || def.Name == "Mutation" {
				if f := def.Fields.ForName(name); f != nil {
					return f
				}
			}
		}
		t.Fatalf("%s is not a root field of the schema", name)
		return nil
	}
	for _, op := range []struct{ field, document string }{
		{"releaseSbomExportProgrammatic", ReleaseSbomExportProgrammatic_Operation},
		{"releaseSbomScoreProgrammatic", ReleaseSbomScoreProgrammatic_Operation},
	} {
		doc, gerr := parser.ParseQuery(&ast.Source{Input: op.document})
		if gerr != nil {
			t.Fatal(gerr)
		}
		if len(doc.Operations) != 1 || len(doc.Operations[0].SelectionSet) != 1 {
			t.Fatalf("%s: one operation selecting one root field", op.field)
		}
		operation := doc.Operations[0]
		root, ok := operation.SelectionSet[0].(*ast.Field)
		if !ok || root.Name != op.field {
			t.Fatalf("the operation selects %s, got %v", op.field, operation.SelectionSet[0])
		}
		want := schemaField(op.field).Arguments
		var wantNames, gotNames []string
		for _, a := range want {
			wantNames = append(wantNames, a.Name)
		}
		for _, a := range root.Arguments {
			gotNames = append(gotNames, a.Name)
			if a.Value.Kind != ast.Variable || a.Value.Raw != a.Name {
				t.Errorf("%s(%s:) is %s, want $%s", op.field, a.Name, a.Value.String(), a.Name)
			}
		}
		sort.Strings(wantNames)
		sort.Strings(gotNames)
		if !reflect.DeepEqual(wantNames, gotNames) {
			t.Errorf("%s: every argument of the schema field and no other:\nwant %v\ngot  %v", op.field, wantNames, gotNames)
		}
		if len(operation.VariableDefinitions) != len(want) {
			t.Errorf("%s: one variable per argument, got %d for %d", op.field, len(operation.VariableDefinitions), len(want))
		}
		for _, v := range operation.VariableDefinitions {
			arg := want.ForName(v.Variable)
			if arg == nil {
				t.Errorf("%s: $%s names no argument of the field", op.field, v.Variable)
			} else if v.Type.String() != arg.Type.String() {
				t.Errorf("%s: $%s is %s, the argument is %s", op.field, v.Variable, v.Type.String(), arg.Type.String())
			}
		}
	}
}
