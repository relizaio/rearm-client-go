package rearm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// A test server whose GraphQL endpoints answer introspection like graphql-java does, from the real
// programmatic schema, including its good-faith introspection limit (graphql-java
// GoodFaithIntrospection: Query.__schema, Query.__type, __Type.fields, __Type.inputFields,
// __Type.interfaces and __Type.possibleTypes are each allowed once per request). It also answers
// the token and CSRF paths by option, so the client's transport can be followed request by request.

// tokenMode is how the test server answers TokenPath.
type tokenMode int

const (
	tokenAbsent tokenMode = iota // 404, empty body (a server without the token endpoint)
	tokenIssue                   // 200, an access token tok-<k>
	tokenRefuse                  // 400, an RFC 6749 invalid_grant body
)

// scripted is a fixed answer to one request.
type scripted struct {
	status int
	body   string
}

type goodFaithOpts struct {
	limit                 bool
	introspectionDisabled bool
	edit                  func(string) string // SDL edit before the schema loads
	token                 tokenMode
	csrf                  bool
	script                map[int]scripted // ProgrammaticPath request n (from 1)
	legacyScript          map[int]scripted // LegacyPath request n (from 1)
}

// recorded is one request the test server received.
type recorded struct {
	Method    string
	Path      string
	OpName    string
	Query     string
	Variables map[string]any
	Auth      string // "Basic", "Bearer <token>" or ""
	XSRF      string
	GrantType string
}

type goodFaithServer struct {
	*httptest.Server
	t      *testing.T
	opts   goodFaithOpts
	schema *ast.Schema

	mu           sync.Mutex
	reqs         []recorded
	programmatic int
	legacy       int
	tokens       int
	csrfs        int
}

func newGoodFaithServer(t *testing.T, opts goodFaithOpts) *goodFaithServer {
	t.Helper()
	s := &goodFaithServer{t: t, opts: opts, schema: loadProgrammaticSchema(t, opts.edit)}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *goodFaithServer) requests() []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recorded(nil), s.reqs...)
}

func (s *goodFaithServer) serve(w http.ResponseWriter, r *http.Request) {
	rec := recorded{Method: r.Method, Path: r.URL.Path, XSRF: r.Header.Get("X-XSRF-TOKEN")}
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Basic ") {
		rec.Auth = "Basic"
	} else {
		rec.Auth = a
	}
	switch {
	case r.Method == http.MethodPost && (r.URL.Path == ProgrammaticPath || r.URL.Path == LegacyPath):
		var body struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.OpName, rec.Query, rec.Variables = body.OperationName, body.Query, body.Variables
		s.mu.Lock()
		s.reqs = append(s.reqs, rec)
		var answer scripted
		var found bool
		if r.URL.Path == ProgrammaticPath {
			s.programmatic++
			answer, found = s.opts.script[s.programmatic]
		} else {
			s.legacy++
			answer, found = s.opts.legacyScript[s.legacy]
		}
		s.mu.Unlock()
		if found {
			w.WriteHeader(answer.status)
			_, _ = io.WriteString(w, answer.body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, s.execute(body.Query, body.OperationName, body.Variables))
	case r.Method == http.MethodPost && r.URL.Path == TokenPath:
		_ = r.ParseForm()
		rec.GrantType = r.PostForm.Get("grant_type")
		s.mu.Lock()
		s.reqs = append(s.reqs, rec)
		s.tokens++
		k := s.tokens
		s.mu.Unlock()
		switch s.opts.token {
		case tokenIssue:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":"tok-%d","token_type":"Bearer","expires_in":3600}`, k)
		case tokenRefuse:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"session revoked"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	case r.Method == http.MethodGet && r.URL.Path == "/api/manual/v1/fetchCsrf":
		s.mu.Lock()
		s.reqs = append(s.reqs, rec)
		s.csrfs++
		k := s.csrfs
		s.mu.Unlock()
		if !s.opts.csrf {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: fmt.Sprintf("j%d", k)})
		http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: fmt.Sprintf("x%d", k)})
	default:
		s.mu.Lock()
		s.reqs = append(s.reqs, rec)
		s.mu.Unlock()
		s.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func gqlErrorBody(message string, classification ErrorClassification) string {
	e := map[string]any{"message": message}
	if classification != "" {
		e["extensions"] = map[string]any{"classification": string(classification)}
	}
	raw, _ := json.Marshal(map[string]any{"errors": []any{e}})
	return string(raw)
}

// badFaithBody is the answer graphql-java gives a request over the good-faith limit.
func badFaithBody(coordinate string) string {
	return gqlErrorBody("This request is not asking for introspection in good faith - "+coordinate+" is present too often!", ClassificationBadFaithIntrospection)
}

// goodFaithCoordinates are graphql-java's limited coordinates, in the order checked here.
var goodFaithCoordinates = []string{
	"__Type.fields", "__Type.inputFields", "__Type.interfaces", "__Type.possibleTypes", "Query.__schema", "Query.__type",
}

func (s *goodFaithServer) execute(query, opName string, variables map[string]any) string {
	doc, errs := gqlparser.LoadQuery(s.schema, query)
	if len(errs) > 0 {
		return gqlErrorBody("Validation error: "+errs[0].Message, ClassificationValidationError)
	}
	var op *ast.OperationDefinition
	if opName != "" {
		op = doc.Operations.ForName(opName)
	} else if len(doc.Operations) == 1 {
		op = doc.Operations[0]
	}
	if op == nil {
		return gqlErrorBody("Validation error: unknown operation", ClassificationValidationError)
	}
	introspection := false
	for _, f := range collectFields(op.SelectionSet) {
		if f.Name == "__schema" || f.Name == "__type" {
			introspection = true
		}
	}
	if introspection && s.opts.introspectionDisabled {
		return gqlErrorBody("Introspection has been disabled for this request", ClassificationIntrospectionDisabled)
	}
	if introspection && s.opts.limit {
		// Count the normalized fields at each coordinate: fields with the same response-key path
		// merge, as graphql-java's executable normalized operation merges them.
		paths := map[string]map[string]bool{}
		countPaths(op.SelectionSet, "", paths)
		for _, c := range goodFaithCoordinates {
			if len(paths[c]) > 1 {
				return badFaithBody(c)
			}
		}
	}
	data := map[string]any{}
	for _, f := range collectFields(op.SelectionSet) {
		key := responseKey(f)
		switch f.Name {
		case "__type":
			name, _ := f.ArgumentMap(variables)["name"].(string)
			data[key] = s.resolveType(name, f.SelectionSet)
		case "__typename":
			data[key] = "Query"
		default:
			s.t.Errorf("the test server models introspection only, got root field %s", f.Name)
			data[key] = nil
		}
	}
	raw, _ := json.Marshal(map[string]any{"data": data})
	return string(raw)
}

func (s *goodFaithServer) resolveType(name string, sel ast.SelectionSet) any {
	def := s.schema.Types[name]
	if def == nil {
		return nil
	}
	out := map[string]any{}
	for _, f := range collectFields(sel) {
		key := responseKey(f)
		switch f.Name {
		case "name":
			out[key] = def.Name
		case "fields":
			fields := []any{}
			for _, fd := range def.Fields {
				if strings.HasPrefix(fd.Name, "__") {
					continue
				}
				entry := map[string]any{}
				for _, sub := range collectFields(f.SelectionSet) {
					if sub.Name != "name" {
						s.t.Errorf("the test server models fields { name } only, got %s", sub.Name)
						continue
					}
					entry[responseKey(sub)] = fd.Name
				}
				fields = append(fields, entry)
			}
			out[key] = fields
		default:
			s.t.Errorf("the test server models __Type { name fields } only, got %s", f.Name)
		}
	}
	return out
}

func responseKey(f *ast.Field) string {
	if f.Alias != "" {
		return f.Alias
	}
	return f.Name
}

// collectFields flattens a selection set's fragment spreads and inline fragments into its fields.
func collectFields(sel ast.SelectionSet) []*ast.Field {
	var out []*ast.Field
	for _, s := range sel {
		switch it := s.(type) {
		case *ast.Field:
			out = append(out, it)
		case *ast.InlineFragment:
			out = append(out, collectFields(it.SelectionSet)...)
		case *ast.FragmentSpread:
			if it.Definition != nil {
				out = append(out, collectFields(it.Definition.SelectionSet)...)
			}
		}
	}
	return out
}

// countPaths records, for each coordinate <ObjectDefinition.Name>.<Name>, the response-key paths
// at which a field of that coordinate is selected.
func countPaths(sel ast.SelectionSet, prefix string, paths map[string]map[string]bool) {
	for _, f := range collectFields(sel) {
		path := prefix + "/" + responseKey(f)
		if f.ObjectDefinition != nil {
			c := f.ObjectDefinition.Name + "." + f.Name
			if paths[c] == nil {
				paths[c] = map[string]bool{}
			}
			paths[c][path] = true
		}
		countPaths(f.SelectionSet, path, paths)
	}
}

// SDL edits.

// withoutFields deletes the lines of the named fields inside typeName's block only.
func withoutFields(t *testing.T, typeName string, names ...string) func(string) string {
	return func(sdl string) string {
		head := "type " + typeName + " {\n"
		start := strings.Index(sdl, head)
		if start < 0 {
			t.Fatalf("type %s not found in the schema", typeName)
		}
		end := start + strings.Index(sdl[start:], "\n}") + 1 // keep the last field's newline in the block
		block := sdl[start:end]
		for _, n := range names {
			re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(n) + `\s*[:(].*\n`)
			if !re.MatchString(block) {
				t.Fatalf("field %s.%s not found in the schema", typeName, n)
			}
			block = re.ReplaceAllString(block, "")
		}
		return sdl[:start] + block + sdl[end:]
	}
}

// renameType renames a type everywhere it is named as a whole word, so ImportSummary leaves
// VexImportSummary alone; the type is then absent under its old name while the schema still loads.
func renameType(oldName, newName string) func(string) string {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(oldName) + `\b`)
	return func(sdl string) string { return re.ReplaceAllString(sdl, newName) }
}

// both applies two SDL edits in order.
func both(a, b func(string) string) func(string) string {
	return func(sdl string) string { return b(a(sdl)) }
}
