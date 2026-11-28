package rearm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Import summaries of uploaded VEX and SAFETY_CONTEXT artifacts (task SAFE-15, design SAFE-9 3.3).
//
// The fields of the import summary differ by server generation: older servers serve ImportSummary
// without statementsUnchanged, demotions and overrideOutcome, later ones add
// statementsAwaitingReconcile, and the oldest only VexImportSummary. Selecting a field the server
// lacks fails GraphQL validation and with it the whole upload, so the selection is built per server
// from small introspection probes instead of a fixed operation in operations/*.graphql (which
// genqlient would generate against this repository's schema only).

// ImportSummaryFields are the summary fields a caller knows, in print order.
var ImportSummaryFields = []string{
	"statementsTotal",
	"proposalsAutoAccepted",
	"proposalsCreated",
	"statementsUnchanged",
	"statementsUnmatched",
	"statementsErrored",
	"proposalsAutoRejected",
	"attestationsDeferred",
	"statementsAwaitingReconcile",
	"demotions",
	"overrideOutcome",
	"errorMessages",
}

// ImportSummaryRequired are the fields every server that has either summary type serves; a type
// missing one of them is not used.
var ImportSummaryRequired = []string{
	"statementsTotal",
	"proposalsCreated",
	"proposalsAutoAccepted",
	"proposalsAutoRejected",
	"statementsUnmatched",
	"statementsErrored",
	"errorMessages",
}

// ImportSummaryProbeOperationName is the operation name of ImportSummaryProbe_Operation.
const ImportSummaryProbeOperationName = "ImportSummaryProbe"

// ImportSummaryProbe_Operation asks the server which fields one summary type has; the variable
// `name` is ImportSummaryTypeName or VexImportSummaryTypeName. It sends no file and writes nothing.
//
// It selects exactly one __type and one fields and nothing else: servers keep graphql-java's
// good-faith introspection limit, which refuses a request with __type or __Type.fields more than
// once (classification BadFaithIntrospection), so each type is asked in a request of its own.
const ImportSummaryProbe_Operation = `query ImportSummaryProbe($name: String!) { __type(name: $name) { fields { name } } }`

// Names of the two summary types the probe asks for.
const (
	ImportSummaryTypeName    = "ImportSummary"
	VexImportSummaryTypeName = "VexImportSummary"
)

// Artifact fields that carry an import summary.
const (
	ImportSummaryField    = "importSummary"
	VexImportSummaryField = "vexImportSummary"
)

// AddArtifactWithImportsOperationName is the operation name of AddArtifactWithImportsOperation.
const AddArtifactWithImportsOperationName = "AddArtifactProgrammaticWithImports"

// ErrImportProbeMalformed is the error of a probe answer without errors whose data is not the
// shape ImportSummaryProbe_Operation asks for. Match it with errors.Is.
var ErrImportProbeMalformed = errors.New("rearm: malformed import summary probe answer")

// ImportSupport is what a server supports: Field is ImportSummaryField, VexImportSummaryField or
// empty (no import summary can be read), and Fields the subset of ImportSummaryFields the chosen
// type serves, in ImportSummaryFields order.
type ImportSupport struct {
	Field  string
	Fields []string
}

// ParseImportSummaryType reads the `data` of one ImportSummaryProbe_Operation answer; field is
// ImportSummaryField or VexImportSummaryField, the artifact field the probed type belongs to. A
// `__type` of null is a type the server does not have: an empty ImportSupport and nil. A type that
// lacks one of ImportSummaryRequired is not used: also empty and nil. Otherwise Field is field and
// Fields the served names of ImportSummaryFields, in that order; unknown names are ignored. Data
// that is absent, null, not an object, without a `__type` member, or with a `__type` or a `fields`
// entry of another shape is an error wrapping ErrImportProbeMalformed.
func ParseImportSummaryType(field string, data json.RawMessage) (ImportSupport, error) {
	var top map[string]json.RawMessage
	if len(data) == 0 || json.Unmarshal(data, &top) != nil || top == nil {
		return ImportSupport{}, fmt.Errorf("%w: data is not an object", ErrImportProbeMalformed)
	}
	rawType, ok := top["__type"]
	if !ok {
		return ImportSupport{}, fmt.Errorf("%w: no __type member", ErrImportProbeMalformed)
	}
	if isJSONNull(rawType) {
		return ImportSupport{}, nil
	}
	var typeInfo map[string]json.RawMessage
	if json.Unmarshal(rawType, &typeInfo) != nil || typeInfo == nil {
		return ImportSupport{}, fmt.Errorf("%w: __type is neither an object nor null", ErrImportProbeMalformed)
	}
	var entries []json.RawMessage
	if rawFields, ok := typeInfo["fields"]; ok && !isJSONNull(rawFields) {
		if json.Unmarshal(rawFields, &entries) != nil {
			return ImportSupport{}, fmt.Errorf("%w: fields is not a list", ErrImportProbeMalformed)
		}
	}
	served := make(map[string]bool, len(entries))
	for _, e := range entries {
		var entry map[string]json.RawMessage
		var name string
		if json.Unmarshal(e, &entry) != nil || entry == nil || json.Unmarshal(entry["name"], &name) != nil {
			return ImportSupport{}, fmt.Errorf("%w: a fields entry has no string name", ErrImportProbeMalformed)
		}
		served[name] = true
	}
	for _, r := range ImportSummaryRequired {
		if !served[r] {
			return ImportSupport{}, nil
		}
	}
	fields := make([]string, 0, len(ImportSummaryFields))
	for _, f := range ImportSummaryFields {
		if served[f] {
			fields = append(fields, f)
		}
	}
	return ImportSupport{Field: field, Fields: fields}, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// ProbeImportSummary asks the server for ImportSummary and, only when that type is absent or lacks
// a required field, for VexImportSummary: at most two operations, one type each.
//
// Not supported (an empty ImportSupport and a nil error) comes only from the server's data, both
// types answered and neither usable, or from an answer whose errors are all classified
// IntrospectionDisabled. Every other outcome is returned as the error, unwrapped, with an empty
// ImportSupport: GraphQL errors of any other classification (also beside data), an HTTP error
// status, a transport or session error, an oversized answer and a malformed answer
// (ErrImportProbeMalformed). Nothing is sent after an operation that ended in an error.
func ProbeImportSummary(ctx context.Context, c *Client) (ImportSupport, error) {
	probes := []struct{ typeName, field string }{
		{ImportSummaryTypeName, ImportSummaryField},
		{VexImportSummaryTypeName, VexImportSummaryField},
	}
	for _, p := range probes {
		data, err := Raw(ctx, c, ImportSummaryProbeOperationName, ImportSummaryProbe_Operation, map[string]any{"name": p.typeName})
		if err != nil {
			var gqlErrs GraphQLErrors
			if errors.As(err, &gqlErrs) && introspectionDisabled(gqlErrs) {
				return ImportSupport{}, nil
			}
			return ImportSupport{}, err
		}
		support, err := ParseImportSummaryType(p.field, data)
		if err != nil {
			return ImportSupport{}, err
		}
		if support.Field != "" {
			return support, nil
		}
	}
	return ImportSupport{}, nil
}

// introspectionDisabled is true when the server answered only that it does not allow
// introspection: every entry is classified IntrospectionDisabled.
func introspectionDisabled(errs GraphQLErrors) bool {
	if len(errs) == 0 {
		return false
	}
	for _, e := range errs {
		if e.Classification() != ClassificationIntrospectionDisabled {
			return false
		}
	}
	return true
}

// ImportSelection is what AddArtifactWithImportsOperation selects: the probed support, and whether
// the release's deliverables (inbound, and each variant's outbound) are read too. There is no
// source code entry selection: the server never returns an import result there.
type ImportSelection struct {
	Support      ImportSupport
	Deliverables bool
}

// AddArtifactWithImportsOperation builds the addArtifactProgrammatic mutation that also reads each
// stored artifact's type, display identifier, tags and import summary, for the release and, with
// Deliverables, its inbound and outbound deliverables. The artifact selection is inlined at each
// place (no fragment), so the document holds exactly one operation. It takes the same variables as
// AddArtifactProgrammatic_Operation. It panics on an empty Support.Field, which is a caller error:
// a caller without support sends AddArtifactProgrammatic_Operation instead.
func AddArtifactWithImportsOperation(sel ImportSelection) string {
	if sel.Support.Field == "" {
		panic("rearm: AddArtifactWithImportsOperation needs a probed import summary field")
	}
	summary := make([]string, 0, len(sel.Support.Fields))
	for _, f := range sel.Support.Fields {
		if f == "demotions" {
			summary = append(summary, "demotions { reason count }")
			continue
		}
		summary = append(summary, f)
	}
	artifact := "artifactDetails { uuid type displayIdentifier tags { key value } " +
		sel.Support.Field + " { " + strings.Join(summary, " ") + " } }"
	var b strings.Builder
	b.WriteString("mutation " + AddArtifactWithImportsOperationName + "($artifactInput: AddArtifactInput) {\n")
	b.WriteString("  addArtifactProgrammatic(artifactInput: $artifactInput) {\n")
	b.WriteString("    uuid version lifecycle artifacts\n")
	b.WriteString("    " + artifact + "\n")
	if sel.Deliverables {
		b.WriteString("    inboundDeliverableDetails { uuid artifacts " + artifact + " }\n")
		b.WriteString("    variantDetails { outboundDeliverableDetails { uuid artifacts " + artifact + " } }\n")
	}
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}
