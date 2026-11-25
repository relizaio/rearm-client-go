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

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// The import summary probe and the per-server import operation (task SAFE-15, design SAFE-9 3.3,
// tests 4.1 g1-g5, g7).

func loadProgrammaticSchema(t *testing.T, edit func(string) string) *ast.Schema {
	t.Helper()
	sdl, err := os.ReadFile("schema/programmatic.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	text := string(sdl)
	if edit != nil {
		text = edit(text)
	}
	schema, gerr := gqlparser.LoadSchema(&ast.Source{Name: "programmatic.graphqls", Input: text})
	if gerr != nil {
		t.Fatalf("schema does not load: %v", gerr)
	}
	return schema
}

func assertOperationLoads(t *testing.T, schema *ast.Schema, op string) {
	t.Helper()
	doc, errs := gqlparser.LoadQuery(schema, op)
	if len(errs) > 0 {
		t.Fatalf("operation does not validate: %v\n%s", errs, op)
	}
	if len(doc.Operations) != 1 || doc.Operations[0].Name != AddArtifactWithImportsOperationName {
		t.Fatalf("one operation named %s, got %d operations\n%s", AddArtifactWithImportsOperationName, len(doc.Operations), op)
	}
	if len(doc.Fragments) != 0 {
		t.Fatalf("no fragments, got %d", len(doc.Fragments))
	}
}

func assertNoSourceCodeEntrySelection(t *testing.T, op string) {
	t.Helper()
	for _, s := range []string{"sourceCodeEntryDetails", "commitsDetails"} {
		if strings.Contains(op, s) {
			t.Fatalf("the operation never selects %s:\n%s", s, op)
		}
	}
}

func fieldsWithout(names []string, drop ...string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		keep := true
		for _, d := range drop {
			if n == d {
				keep = false
			}
		}
		if keep {
			out = append(out, n)
		}
	}
	return out
}

// g1: every field the refreshed schema serves, with and without deliverables.
func TestAddArtifactWithImportsOperationValidatesAgainstTheRefreshedSchema(t *testing.T) {
	schema := loadProgrammaticSchema(t, nil)
	support := ImportSupport{Field: ImportSummaryField, Fields: fieldsWithout(ImportSummaryFields, "statementsAwaitingReconcile")}
	for _, deliverables := range []bool{false, true} {
		op := AddArtifactWithImportsOperation(ImportSelection{Support: support, Deliverables: deliverables})
		assertOperationLoads(t, schema, op)
		assertNoSourceCodeEntrySelection(t, op)
		for _, s := range []string{"inboundDeliverableDetails", "variantDetails", "outboundDeliverableDetails"} {
			if strings.Contains(op, s) != deliverables {
				t.Fatalf("Deliverables=%v: %s present=%v\n%s", deliverables, s, !deliverables, op)
			}
		}
		if !strings.Contains(op, "demotions { reason count }") || !strings.Contains(op, "tags { key value }") {
			t.Fatalf("demotions and tags are selected with their subfields:\n%s", op)
		}
		if strings.Count(op, "importSummary {") != map[bool]int{false: 1, true: 3}[deliverables] {
			t.Fatalf("the artifact selection is inlined at each place:\n%s", op)
		}
	}
}

// g2: a server that also serves statementsAwaitingReconcile (SAFE-3).
func TestAddArtifactWithImportsOperationValidatesWithAwaitingReconcile(t *testing.T) {
	schema := loadProgrammaticSchema(t, func(s string) string {
		const head = "type ImportSummary {\n"
		if !strings.Contains(s, head) {
			t.Fatal("type ImportSummary not found in the schema")
		}
		return strings.Replace(s, head, head+"  statementsAwaitingReconcile: Int\n", 1)
	})
	for _, deliverables := range []bool{false, true} {
		op := AddArtifactWithImportsOperation(ImportSelection{
			Support:      ImportSupport{Field: ImportSummaryField, Fields: ImportSummaryFields},
			Deliverables: deliverables,
		})
		assertOperationLoads(t, schema, op)
	}
}

// g3: the deprecated VexImportSummary with the required fields only.
func TestAddArtifactWithImportsOperationValidatesOnVexImportSummary(t *testing.T) {
	schema := loadProgrammaticSchema(t, nil)
	op := AddArtifactWithImportsOperation(ImportSelection{
		Support:      ImportSupport{Field: VexImportSummaryField, Fields: ImportSummaryRequired},
		Deliverables: true,
	})
	assertOperationLoads(t, schema, op)
	for _, s := range []string{"demotions", "overrideOutcome", "importSummary {"} {
		if strings.Contains(op, s) {
			t.Fatalf("%s is not selected on a vexImportSummary-only server:\n%s", s, op)
		}
	}
}

func probeData(types map[string][]string) json.RawMessage {
	data := map[string]any{}
	for _, alias := range []string{ImportSummaryField, VexImportSummaryField} {
		names, ok := types[alias]
		if !ok {
			data[alias] = nil
			continue
		}
		fields := make([]map[string]string, 0, len(names))
		for _, n := range names {
			fields = append(fields, map[string]string{"name": n})
		}
		data[alias] = map[string]any{"fields": fields}
	}
	raw, _ := json.Marshal(data)
	return raw
}

// g4: which type and which fields the probe answer selects.
func TestParseImportSummaryProbe(t *testing.T) {
	served := fieldsWithout(ImportSummaryFields, "statementsAwaitingReconcile")
	// Reversed, so the order of the answer cannot leak into Fields.
	reversed := make([]string, len(served))
	for i, n := range served {
		reversed[len(served)-1-i] = n
	}
	cases := []struct {
		name string
		data json.RawMessage
		want ImportSupport
	}{
		{"both types complete",
			probeData(map[string][]string{ImportSummaryField: reversed, VexImportSummaryField: ImportSummaryRequired}),
			ImportSupport{Field: ImportSummaryField, Fields: served}},
		{"ImportSummary lacks statementsErrored, VexImportSummary complete",
			probeData(map[string][]string{ImportSummaryField: fieldsWithout(served, "statementsErrored"), VexImportSummaryField: append(append([]string{}, ImportSummaryRequired...), "attestationsDeferred")}),
			ImportSupport{Field: VexImportSummaryField, Fields: []string{"statementsTotal", "proposalsAutoAccepted", "proposalsCreated", "statementsUnmatched", "statementsErrored", "proposalsAutoRejected", "attestationsDeferred", "errorMessages"}}},
		{"both null",
			probeData(map[string][]string{}),
			ImportSupport{}},
		{"extra unknown field, no statementsUnchanged",
			probeData(map[string][]string{ImportSummaryField: append(fieldsWithout(served, "statementsUnchanged"), "somethingNew")}),
			ImportSupport{Field: ImportSummaryField, Fields: fieldsWithout(served, "statementsUnchanged")}},
		{"not JSON", json.RawMessage(`not json`), ImportSupport{}},
		{"empty", nil, ImportSupport{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseImportSummaryProbe(c.data)
			if got.Field != c.want.Field || !reflect.DeepEqual(append([]string{}, got.Fields...), append([]string{}, c.want.Fields...)) {
				t.Fatalf("want %+v, got %+v", c.want, got)
			}
		})
	}
}

// g5: GraphQL errors read as not supported; any other failure is an error.
func TestProbeImportSummaryErrors(t *testing.T) {
	var gotOp, gotQuery string
	status := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ProgrammaticPath {
			w.WriteHeader(404)
			return
		}
		var body struct {
			OperationName string `json:"operationName"`
			Query         string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotOp, gotQuery = body.OperationName, body.Query
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("internal error"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": nil, "errors": []map[string]any{{"message": "introspection is disabled"}}})
	}))
	defer srv.Close()
	c, err := New(srv.URL, "id", "secret", WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}

	got, err := ProbeImportSummary(context.Background(), c)
	if err != nil || got.Field != "" || len(got.Fields) != 0 {
		t.Fatalf("GraphQL errors read as not supported with a nil error, got %+v %v", got, err)
	}
	if gotOp != ImportSummaryProbeOperationName || gotQuery != ImportSummaryProbe_Operation {
		t.Fatalf("the probe is sent as is, got %q %q", gotOp, gotQuery)
	}

	status = http.StatusInternalServerError
	got, err = ProbeImportSummary(context.Background(), c)
	if err == nil || got.Field != "" {
		t.Fatalf("a 500 answer is an error, got %+v %v", got, err)
	}
}

// The probe answer of a server with the refreshed schema reads as full support (the probe text
// itself validates against the schema).
func TestProbeImportSummaryReadsAServerAnswer(t *testing.T) {
	schema := loadProgrammaticSchema(t, nil)
	if _, errs := gqlparser.LoadQuery(schema, ImportSummaryProbe_Operation); len(errs) > 0 {
		t.Fatalf("the probe validates: %v", errs)
	}
	served := fieldsWithout(ImportSummaryFields, "statementsAwaitingReconcile")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":` + string(probeData(map[string][]string{ImportSummaryField: served, VexImportSummaryField: ImportSummaryRequired})) + `}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "id", "secret", WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	got, err := ProbeImportSummary(context.Background(), c)
	if err != nil || got.Field != ImportSummaryField || !reflect.DeepEqual(got.Fields, served) {
		t.Fatalf("full support, got %+v %v", got, err)
	}
}

// g7: an empty Field is a caller error.
func TestAddArtifactWithImportsOperationPanicsWithoutAField(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic on an empty Field")
		}
	}()
	AddArtifactWithImportsOperation(ImportSelection{Support: ImportSupport{Fields: ImportSummaryRequired}})
}
