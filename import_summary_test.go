package rearm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// The import summary probe and the per-server import operation (task SAFE-15, design SAFE-9 3.3,
// tests 4.1 g1-g3, g7; the probe under the good-faith introspection limit, task SAFE-30, tests s1-s7
// against the test server of good_faith_server_test.go).

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

// g7: an empty Field is a caller error.
func TestAddArtifactWithImportsOperationPanicsWithoutAField(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic on an empty Field")
		}
	}()
	AddArtifactWithImportsOperation(ImportSelection{Support: ImportSupport{Fields: ImportSummaryRequired}})
}

// The probe text before task SAFE-30: two __type selections in one request.
const twoTypeProbe = `query ImportSummaryProbe { importSummary: __type(name: "ImportSummary") { fields { name } } vexImportSummary: __type(name: "VexImportSummary") { fields { name } } }`

const badFaithFieldsMessage = "This request is not asking for introspection in good faith - __Type.fields is present too often!"

// Names each schema generation serves, in ImportSummaryFields order.
var (
	servedCurrent = fieldsWithout(ImportSummaryFields, "statementsAwaitingReconcile")
	servedCE      = fieldsWithout(ImportSummaryFields, "statementsAwaitingReconcile", "statementsUnchanged", "demotions", "overrideOutcome")
	servedVex     = fieldsWithout(ImportSummaryFields, "statementsAwaitingReconcile", "demotions", "overrideOutcome")
)

func kbClient(t *testing.T, url string) *Client {
	t.Helper()
	c, err := New(url, "id", "secret", WithoutTokenExchange())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func assertSupport(t *testing.T, got ImportSupport, field string, fields []string) {
	t.Helper()
	if got.Field != field || !reflect.DeepEqual(append([]string{}, got.Fields...), append([]string{}, fields...)) {
		t.Fatalf("want support %q %v, got %+v", field, fields, got)
	}
}

func assertEmptySupport(t *testing.T, got ImportSupport) {
	t.Helper()
	if got.Field != "" || len(got.Fields) != 0 {
		t.Fatalf("want empty support, got %+v", got)
	}
}

// assertProbeRequests checks that every recorded request is a probe POST to ProgrammaticPath with
// the key as Basic, carrying the probe text, with the type names given, in order.
func assertProbeRequests(t *testing.T, reqs []recorded, names ...string) {
	t.Helper()
	if len(reqs) != len(names) {
		t.Fatalf("want %d requests, got %d: %+v", len(names), len(reqs), reqs)
	}
	for i, r := range reqs {
		if r.Method != http.MethodPost || r.Path != ProgrammaticPath || r.Auth != "Basic" {
			t.Fatalf("request %d: want POST %s with Basic, got %s %s %q", i+1, ProgrammaticPath, r.Method, r.Path, r.Auth)
		}
		if r.OpName != ImportSummaryProbeOperationName || r.Query != ImportSummaryProbe_Operation {
			t.Fatalf("request %d: want the probe as is, got %q %q", i+1, r.OpName, r.Query)
		}
		if !reflect.DeepEqual(r.Variables, map[string]any{"name": names[i]}) {
			t.Fatalf("request %d: want variables name=%s, got %v", i+1, names[i], r.Variables)
		}
	}
}

// s1: the test server holds the pre-fix two-type probe to graphql-java's good-faith rule.
func TestGoodFaithServerRefusesTheTwoTypeProbe(t *testing.T) {
	srv := newGoodFaithServer(t, goodFaithOpts{limit: true})
	data, err := Raw(context.Background(), kbClient(t, srv.URL), ImportSummaryProbeOperationName, twoTypeProbe, map[string]any{})
	var gqlErrs GraphQLErrors
	if !errors.As(err, &gqlErrs) || len(gqlErrs) != 1 {
		t.Fatalf("want one GraphQL error, got %v", err)
	}
	if gqlErrs[0].Message != badFaithFieldsMessage || gqlErrs[0].Classification() != ClassificationBadFaithIntrospection {
		t.Fatalf("want the good-faith refusal, got %q %q", gqlErrs[0].Message, gqlErrs[0].Classification())
	}
	if len(data) != 0 {
		t.Fatalf("want no data, got %s", data)
	}

	off := newGoodFaithServer(t, goodFaithOpts{limit: false})
	data, err = Raw(context.Background(), kbClient(t, off.URL), ImportSummaryProbeOperationName, twoTypeProbe, map[string]any{})
	if err != nil {
		t.Fatalf("limit off: want an answer, got %v", err)
	}
	var both map[string]*struct {
		Fields []struct{ Name string } `json:"fields"`
	}
	if json.Unmarshal(data, &both) != nil || both["importSummary"] == nil || both["vexImportSummary"] == nil ||
		len(both["importSummary"].Fields) == 0 || len(both["vexImportSummary"].Fields) == 0 {
		t.Fatalf("limit off: want both types answered, got %s", data)
	}
}

// s2: the probe on every schema generation, with the limit on and off.
func TestProbeImportSummaryUnderTheGoodFaithLimit(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(string) string
		field  string
		fields []string
		names  []string
	}{
		{"a current", nil, ImportSummaryField, servedCurrent, []string{ImportSummaryTypeName}},
		{"b CE generation", withoutFields(t, "ImportSummary", "statementsUnchanged", "demotions", "overrideOutcome"),
			ImportSummaryField, servedCE, []string{ImportSummaryTypeName}},
		{"c lacks a required field", withoutFields(t, "ImportSummary", "statementsErrored"),
			VexImportSummaryField, servedVex, []string{ImportSummaryTypeName, VexImportSummaryTypeName}},
		{"d VexImportSummary only", renameType("ImportSummary", "LegacyImportSummary"),
			VexImportSummaryField, servedVex, []string{ImportSummaryTypeName, VexImportSummaryTypeName}},
		{"e neither", both(renameType("ImportSummary", "LegacyImportSummary"), renameType("VexImportSummary", "LegacyVexImportSummary")),
			"", nil, []string{ImportSummaryTypeName, VexImportSummaryTypeName}},
	}
	for _, limit := range []bool{true, false} {
		for _, c := range cases {
			t.Run(c.name+map[bool]string{true: " limit on", false: " limit off"}[limit], func(t *testing.T) {
				srv := newGoodFaithServer(t, goodFaithOpts{limit: limit, edit: c.edit})
				got, err := ProbeImportSummary(context.Background(), kbClient(t, srv.URL))
				if err != nil {
					t.Fatalf("want a nil error, got %v", err)
				}
				if c.field == "" {
					assertEmptySupport(t, got)
				} else {
					assertSupport(t, got, c.field, c.fields)
				}
				assertProbeRequests(t, srv.requests(), c.names...)
			})
		}
	}
}

// s3: every refusal is an error; only IntrospectionDisabled and data read as not supported.
func TestProbeImportSummaryClassifiesEveryAnswer(t *testing.T) {
	allRequired := `{"__type":{"fields":[` + func() string {
		parts := make([]string, 0, len(ImportSummaryRequired))
		for _, n := range ImportSummaryRequired {
			parts = append(parts, `{"name":"`+n+`"}`)
		}
		return strings.Join(parts, ",")
	}() + `]}}`
	typeNull := scripted{200, `{"data":{"__type":null}}`}
	disabled := gqlErrorBody("Introspection has been disabled for this request", ClassificationIntrospectionDisabled)

	type outcome int
	const (
		graphQLError outcome = iota
		notSupported
		statusError
		otherError
		malformed
	)
	cases := []struct {
		row      string
		opts     goodFaithOpts
		closed   bool
		want     outcome
		messages []string
		status   int
		body     string
		requests int
	}{
		{row: "1 bad faith", opts: goodFaithOpts{script: map[int]scripted{1: {200, badFaithBody("__Type.fields")}}},
			want: graphQLError, messages: []string{badFaithFieldsMessage}, requests: 1},
		{row: "2 null then bad faith", opts: goodFaithOpts{script: map[int]scripted{1: typeNull, 2: {200, badFaithBody("__Type.fields")}}},
			want: graphQLError, messages: []string{badFaithFieldsMessage}, requests: 2},
		{row: "3 not authorized", opts: goodFaithOpts{script: map[int]scripted{1: {200, `{"errors":[{"message":"Not authorized"}]}`}}},
			want: graphQLError, messages: []string{"Not authorized"}, requests: 1},
		{row: "4 validation error", opts: goodFaithOpts{script: map[int]scripted{1: {200, gqlErrorBody("Validation error: bad", ClassificationValidationError)}}},
			want: graphQLError, messages: []string{"Validation error: bad"}, requests: 1},
		{row: "5 errors beside data", opts: goodFaithOpts{script: map[int]scripted{1: {200, `{"data":` + allRequired + `,"errors":[{"message":"partial"}]}`}}},
			want: graphQLError, messages: []string{"partial"}, requests: 1},
		{row: "6 disabled mixed with another", opts: goodFaithOpts{script: map[int]scripted{1: {200,
			`{"errors":[{"message":"Introspection has been disabled for this request","extensions":{"classification":"IntrospectionDisabled"}},{"message":"other"}]}`}}},
			want: graphQLError, messages: []string{"Introspection has been disabled for this request", "other"}, requests: 1},
		{row: "7 introspection disabled", opts: goodFaithOpts{introspectionDisabled: true}, want: notSupported, requests: 1},
		{row: "8 null then disabled", opts: goodFaithOpts{script: map[int]scripted{1: typeNull, 2: {200, disabled}}}, want: notSupported, requests: 2},
		{row: "9 HTTP 500 text", opts: goodFaithOpts{script: map[int]scripted{1: {500, "internal error"}}},
			want: statusError, status: 500, body: "internal error", requests: 1},
		{row: "10 HTTP 502 JSON", opts: goodFaithOpts{script: map[int]scripted{1: {502, `{"message":"bad gateway"}`}}},
			want: statusError, status: 502, body: "", requests: 1},
		{row: "11 connection refused", closed: true, want: otherError, requests: 0},
		{row: "12 data null", opts: goodFaithOpts{script: map[int]scripted{1: {200, `{"data":null}`}}}, want: malformed, requests: 1},
		{row: "13 data empty object", opts: goodFaithOpts{script: map[int]scripted{1: {200, `{"data":{}}`}}}, want: malformed, requests: 1},
		{row: "14 __type a string", opts: goodFaithOpts{script: map[int]scripted{1: {200, `{"data":{"__type":"x"}}`}}}, want: malformed, requests: 1},
		{row: "15 fields entry without a string name", opts: goodFaithOpts{script: map[int]scripted{1: {200, `{"data":{"__type":{"fields":[{"name":1}]}}}`}}},
			want: malformed, requests: 1},
		{row: "16 fields entry with a null name", opts: goodFaithOpts{script: map[int]scripted{1: {200, `{"data":{"__type":{"fields":[{"name":null}]}}}`}}},
			want: malformed, requests: 1},
	}
	for _, c := range cases {
		t.Run(c.row, func(t *testing.T) {
			c.opts.limit = true
			srv := newGoodFaithServer(t, c.opts)
			client := kbClient(t, srv.URL)
			if c.closed {
				srv.Close()
			}
			got, err := ProbeImportSummary(context.Background(), client)
			assertEmptySupport(t, got)
			var gqlErrs GraphQLErrors
			var se *StatusError
			switch c.want {
			case graphQLError:
				if !errors.As(err, &gqlErrs) {
					t.Fatalf("want GraphQLErrors, got %v", err)
				}
				msgs := make([]string, 0, len(gqlErrs))
				for _, e := range gqlErrs {
					msgs = append(msgs, e.Message)
				}
				if !reflect.DeepEqual(msgs, c.messages) {
					t.Fatalf("want messages %q, got %q", c.messages, msgs)
				}
			case notSupported:
				if err != nil {
					t.Fatalf("want a nil error, got %v", err)
				}
			case statusError:
				if !errors.As(err, &se) || se.StatusCode != c.status || se.Body != c.body {
					t.Fatalf("want *StatusError %d %q, got %#v", c.status, c.body, err)
				}
			case otherError:
				if err == nil || errors.As(err, &gqlErrs) || errors.As(err, &se) {
					t.Fatalf("want an error that is neither GraphQLErrors nor *StatusError, got %v", err)
				}
			case malformed:
				if !errors.Is(err, ErrImportProbeMalformed) {
					t.Fatalf("want ErrImportProbeMalformed, got %v", err)
				}
			}
			reqs := srv.requests()
			if len(reqs) != c.requests {
				t.Fatalf("want %d requests, got %d: %+v", c.requests, len(reqs), reqs)
			}
			for i, r := range reqs {
				if r.Method != http.MethodPost || r.Path != ProgrammaticPath {
					t.Fatalf("request %d: want POST %s, got %s %s", i+1, ProgrammaticPath, r.Method, r.Path)
				}
			}
		})
	}
}

// s4: ParseImportSummaryType on its own.
func TestParseImportSummaryType(t *testing.T) {
	typeWith := func(names ...string) json.RawMessage {
		fields := make([]map[string]string, 0, len(names))
		for _, n := range names {
			fields = append(fields, map[string]string{"name": n})
		}
		raw, _ := json.Marshal(map[string]any{"__type": map[string]any{"fields": fields}})
		return raw
	}
	// Reversed, so the order of the answer cannot leak into Fields.
	reversed := make([]string, len(ImportSummaryFields))
	for i, n := range ImportSummaryFields {
		reversed[len(ImportSummaryFields)-1-i] = n
	}
	ok := []struct {
		name   string
		field  string
		data   json.RawMessage
		fields []string
	}{
		{"all fields", ImportSummaryField, typeWith(reversed...), ImportSummaryFields},
		{"unknown name, no statementsUnchanged", ImportSummaryField,
			typeWith(append(fieldsWithout(servedCurrent, "statementsUnchanged"), "somethingNew")...),
			fieldsWithout(servedCurrent, "statementsUnchanged")},
		{"vex", VexImportSummaryField, typeWith(servedVex...), servedVex},
		{"lacks a required field", ImportSummaryField, typeWith(fieldsWithout(servedCurrent, "statementsErrored")...), nil},
		{"fields null", ImportSummaryField, json.RawMessage(`{"__type":{"fields":null}}`), nil},
		{"fields absent", ImportSummaryField, json.RawMessage(`{"__type":{}}`), nil},
		{"type null", ImportSummaryField, json.RawMessage(`{"__type":null}`), nil},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseImportSummaryType(c.field, c.data)
			if err != nil {
				t.Fatalf("want a nil error, got %v", err)
			}
			if c.fields == nil {
				assertEmptySupport(t, got)
				return
			}
			assertSupport(t, got, c.field, c.fields)
		})
	}
	for _, data := range []string{"", "null", "{}", `{"__type":"x"}`, `{"__type":[]}`, `{"__type":{"fields":[{"name":1}]}}`,
		`{"__type":{"fields":[{"name":null}]}}`, `{"__type":{"fields":[{}]}}`, `{"__type":{"fields":[null]}}`,
		`{"__type":{"fields":["x"]}}`, `{"__type":{"fields":{}}}`, `[]`, `not json`} {
		t.Run("malformed "+data, func(t *testing.T) {
			got, err := ParseImportSummaryType(ImportSummaryField, json.RawMessage(data))
			if !errors.Is(err, ErrImportProbeMalformed) {
				t.Fatalf("want ErrImportProbeMalformed, got %v", err)
			}
			assertEmptySupport(t, got)
		})
	}
}

// s5: GraphQLError.Classification.
func TestGraphQLErrorClassification(t *testing.T) {
	cases := []struct {
		ext  map[string]any
		want ErrorClassification
	}{
		{map[string]any{"classification": "BadFaithIntrospection"}, ClassificationBadFaithIntrospection},
		{map[string]any{"classification": 7}, ""},
		{map[string]any{"other": "x"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := (GraphQLError{Message: "m", Extensions: c.ext}).Classification(); got != c.want {
			t.Fatalf("extensions %v: want %q, got %q", c.ext, c.want, got)
		}
	}
}

// s6: the probe text selects one __type and one fields, and validates.
func TestImportSummaryProbeTextStaysWithinTheLimit(t *testing.T) {
	if strings.Count(ImportSummaryProbe_Operation, "__type") != 1 || strings.Count(ImportSummaryProbe_Operation, "fields") != 1 {
		t.Fatalf("one __type and one fields, got %q", ImportSummaryProbe_Operation)
	}
	if _, errs := gqlparser.LoadQuery(loadProgrammaticSchema(t, nil), ImportSummaryProbe_Operation); len(errs) > 0 {
		t.Fatalf("the probe validates: %v", errs)
	}
}

// wire is a compact form of a recorded request: P (ProgrammaticPath), T (TokenPath), C (CSRF) or
// L (LegacyPath), with what tells the client kinds apart.
func wire(r recorded) string {
	var parts []string
	switch {
	case r.Method == http.MethodPost && r.Path == ProgrammaticPath:
		parts = []string{"P", r.Auth}
	case r.Method == http.MethodPost && r.Path == TokenPath:
		parts = []string{"T", r.GrantType, r.Auth}
	case r.Method == http.MethodGet && r.Path == "/api/manual/v1/fetchCsrf":
		parts = []string{"C"}
	case r.Method == http.MethodPost && r.Path == LegacyPath:
		parts = []string{"L", r.Auth, r.XSRF}
	default:
		parts = []string{r.Method, r.Path}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// s7: the probe through each client kind when the programmatic endpoint answers 401, 403 or 404.
// These pin the existing transport behaviour the probe relies on.
func TestProbeImportSummaryThroughEachClientKind(t *testing.T) {
	denied := func(status int) scripted { return scripted{status, `{"message":"denied"}`} }
	kb := func(t *testing.T, url string) *Client { return kbClient(t, url) }
	kt := func(t *testing.T, url string) *Client {
		c, err := New(url, "id", "secret")
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	ks := func(t *testing.T, url string) *Client {
		c, err := NewWithSession(url, "rt-1", SessionTokens{AccessToken: "at-1", AccessTokenExpiry: time.Now().Add(time.Hour)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	ka := func(t *testing.T, url string) *Client {
		c, err := NewWithAssertion(url, "", func(context.Context) (string, error) { return "id-token", nil })
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	type outcome int
	const (
		csrfFailure outcome = iota
		statusError
		sessionError
		supported
	)
	const (
		cc  = "T client_credentials Basic"
		rt  = "T refresh_token"
		jwt = "T " + JwtBearerGrant
	)
	cases := []struct {
		row    string
		client func(*testing.T, string) *Client
		opts   goodFaithOpts
		want   outcome
		status int
		field  string
		fields []string
		wire   []string
	}{
		{"a KB 401 no CSRF", kb, goodFaithOpts{script: map[int]scripted{1: denied(401)}}, csrfFailure, 0, "", nil,
			[]string{"P Basic", "C"}},
		{"b KB 403 no CSRF", kb, goodFaithOpts{script: map[int]scripted{1: denied(403)}}, csrfFailure, 0, "", nil,
			[]string{"P Basic", "C"}},
		{"c KB 404 no CSRF", kb, goodFaithOpts{script: map[int]scripted{1: denied(404)}}, csrfFailure, 0, "", nil,
			[]string{"P Basic", "C"}},
		{"d KB 404 legacy replay decides", kb, goodFaithOpts{script: map[int]scripted{1: denied(404)}, csrf: true,
			edit: renameType("ImportSummary", "LegacyImportSummary")}, supported, 0, VexImportSummaryField, servedVex,
			[]string{"P Basic", "C", "L Basic x1", "L Basic x1"}},
		{"e KB 401 legacy 401 twice", kb, goodFaithOpts{script: map[int]scripted{1: denied(401)}, csrf: true,
			legacyScript: map[int]scripted{1: denied(401), 2: denied(401)}}, statusError, 401, "", nil,
			[]string{"P Basic", "C", "L Basic x1", "C", "L Basic x2"}},
		{"f KT 401 twice", kt, goodFaithOpts{token: tokenIssue, script: map[int]scripted{1: denied(401), 2: denied(401)}}, statusError, 401, "", nil,
			[]string{cc, "P Bearer tok-1", cc, "P Bearer tok-2"}},
		{"g KT 401 then answered", kt, goodFaithOpts{token: tokenIssue, limit: true, script: map[int]scripted{1: denied(401)}}, supported, 0,
			ImportSummaryField, servedCurrent, []string{cc, "P Bearer tok-1", cc, "P Bearer tok-2"}},
		{"h KT 403", kt, goodFaithOpts{token: tokenIssue, script: map[int]scripted{1: denied(403)}}, statusError, 403, "", nil,
			[]string{cc, "P Bearer tok-1"}},
		{"i KT 404", kt, goodFaithOpts{token: tokenIssue, script: map[int]scripted{1: denied(404)}}, statusError, 404, "", nil,
			[]string{cc, "P Bearer tok-1"}},
		{"j key, no token endpoint, 401 no CSRF", kt, goodFaithOpts{token: tokenAbsent, script: map[int]scripted{1: denied(401)}}, csrfFailure, 0, "", nil,
			[]string{cc, "P Basic", "C"}},
		{"k KS 401 twice", ks, goodFaithOpts{token: tokenIssue, script: map[int]scripted{1: denied(401), 2: denied(401)}}, statusError, 401, "", nil,
			[]string{"P Bearer at-1", rt, "P Bearer tok-1"}},
		{"l KS 401 refresh refused", ks, goodFaithOpts{token: tokenRefuse, script: map[int]scripted{1: denied(401)}}, sessionError, 0, "", nil,
			[]string{"P Bearer at-1", rt}},
		{"m KS 403", ks, goodFaithOpts{token: tokenIssue, script: map[int]scripted{1: denied(403)}}, statusError, 403, "", nil,
			[]string{"P Bearer at-1"}},
		{"n KS 404", ks, goodFaithOpts{token: tokenIssue, script: map[int]scripted{1: denied(404)}}, statusError, 404, "", nil,
			[]string{"P Bearer at-1"}},
		{"o KA 401", ka, goodFaithOpts{token: tokenIssue, script: map[int]scripted{1: denied(401)}}, statusError, 401, "", nil,
			[]string{jwt, "P Bearer tok-1"}},
		{"p KA 403", ka, goodFaithOpts{token: tokenIssue, script: map[int]scripted{1: denied(403)}}, statusError, 403, "", nil,
			[]string{jwt, "P Bearer tok-1"}},
		{"q KA 404", ka, goodFaithOpts{token: tokenIssue, script: map[int]scripted{1: denied(404)}}, statusError, 404, "", nil,
			[]string{jwt, "P Bearer tok-1"}},
	}
	for _, c := range cases {
		t.Run(c.row, func(t *testing.T) {
			srv := newGoodFaithServer(t, c.opts)
			got, err := ProbeImportSummary(context.Background(), c.client(t, srv.URL))
			var gqlErrs GraphQLErrors
			var se *StatusError
			var sessErr *SessionError
			switch c.want {
			case csrfFailure:
				if err == nil || errors.As(err, &gqlErrs) || errors.As(err, &se) || !strings.Contains(err.Error(), "csrf handshake: no XSRF-TOKEN cookie") {
					t.Fatalf("want the CSRF handshake error, got %v", err)
				}
			case statusError:
				if !errors.As(err, &se) || se.StatusCode != c.status {
					t.Fatalf("want *StatusError %d, got %v", c.status, err)
				}
			case sessionError:
				if !errors.As(err, &sessErr) || sessErr.Code != "invalid_grant" {
					t.Fatalf("want *SessionError invalid_grant, got %v", err)
				}
			case supported:
				if err != nil {
					t.Fatalf("want a nil error, got %v", err)
				}
				assertSupport(t, got, c.field, c.fields)
			}
			if c.want != supported {
				assertEmptySupport(t, got)
			}
			reqs := srv.requests()
			gotWire := make([]string, 0, len(reqs))
			for _, r := range reqs {
				gotWire = append(gotWire, wire(r))
			}
			if !reflect.DeepEqual(gotWire, c.wire) {
				t.Fatalf("want requests %q, got %q", c.wire, gotWire)
			}
			if c.row[0] == 'd' {
				// The replay decided, and the second operation never touched ProgrammaticPath.
				names := []string{}
				for _, r := range reqs {
					if r.Path == ProgrammaticPath || r.Path == LegacyPath {
						names = append(names, r.Variables["name"].(string))
					}
				}
				if !reflect.DeepEqual(names, []string{ImportSummaryTypeName, ImportSummaryTypeName, VexImportSummaryTypeName}) {
					t.Fatalf("want probe names P ImportSummary, L ImportSummary, L VexImportSummary, got %v", names)
				}
			}
		})
	}
}
