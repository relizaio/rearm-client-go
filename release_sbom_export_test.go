package rearm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// The merged release SBOM operations (task SCORE-24): the CLI sends them raw with every option as a
// variable, nulls included, and prints the root field's string as the server produced it.

func TestReleaseSbomOperationsPassEveryVariableThroughAndTheDocumentBack(t *testing.T) {
	const doc = `{"bomFormat":"CycloneDX","components":[{"name":"café"}]}`
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
	if err := json.Unmarshal(data, &out); err != nil || out["releaseSbomExportProgrammatic"] != "{\"bomFormat\":\"CycloneDX\",\"components\":[{\"name\":\"café\"}]}" {
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
