package rearm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Import summaries of uploaded VEX and SAFETY_CONTEXT artifacts (task SAFE-15, design SAFE-9 3.3).
//
// The fields of the import summary differ by server generation: older servers serve ImportSummary
// without statementsUnchanged, demotions and overrideOutcome, later ones add
// statementsAwaitingReconcile, and the oldest only VexImportSummary. Selecting a field the server
// lacks fails GraphQL validation and with it the whole upload, so the selection is built per server
// from a small introspection probe instead of a fixed operation in operations/*.graphql (which
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

// ImportSummaryProbe_Operation asks the server which fields its two summary types have. It sends no
// file and writes nothing.
const ImportSummaryProbe_Operation = `query ImportSummaryProbe { importSummary: __type(name: "ImportSummary") { fields { name } } vexImportSummary: __type(name: "VexImportSummary") { fields { name } } }`

// Artifact fields that carry an import summary.
const (
	ImportSummaryField    = "importSummary"
	VexImportSummaryField = "vexImportSummary"
)

// AddArtifactWithImportsOperationName is the operation name of AddArtifactWithImportsOperation.
const AddArtifactWithImportsOperationName = "AddArtifactProgrammaticWithImports"

// ImportSupport is what a server supports: Field is ImportSummaryField, VexImportSummaryField or
// empty (no import summary can be read), and Fields the subset of ImportSummaryFields the chosen
// type serves, in ImportSummaryFields order.
type ImportSupport struct {
	Field  string
	Fields []string
}

// ParseImportSummaryProbe reads the `data` of ImportSummaryProbe_Operation. ImportSummary is chosen
// when it serves every required field, else VexImportSummary under the same test, else neither.
// Unknown field names are ignored; a document that does not parse reads as not supported.
func ParseImportSummaryProbe(data json.RawMessage) ImportSupport {
	type typeInfo struct {
		Fields []struct {
			Name string `json:"name"`
		} `json:"fields"`
	}
	var probe struct {
		ImportSummary    *typeInfo `json:"importSummary"`
		VexImportSummary *typeInfo `json:"vexImportSummary"`
	}
	if len(data) == 0 || json.Unmarshal(data, &probe) != nil {
		return ImportSupport{}
	}
	choose := func(field string, t *typeInfo) (ImportSupport, bool) {
		if t == nil {
			return ImportSupport{}, false
		}
		served := make(map[string]bool, len(t.Fields))
		for _, f := range t.Fields {
			served[f.Name] = true
		}
		for _, r := range ImportSummaryRequired {
			if !served[r] {
				return ImportSupport{}, false
			}
		}
		fields := make([]string, 0, len(ImportSummaryFields))
		for _, f := range ImportSummaryFields {
			if served[f] {
				fields = append(fields, f)
			}
		}
		return ImportSupport{Field: field, Fields: fields}, true
	}
	if s, ok := choose(ImportSummaryField, probe.ImportSummary); ok {
		return s
	}
	if s, ok := choose(VexImportSummaryField, probe.VexImportSummary); ok {
		return s
	}
	return ImportSupport{}
}

// ProbeImportSummary sends ImportSummaryProbe_Operation. An answer with GraphQL errors (a server
// that refuses introspection) reads as not supported: an empty ImportSupport and a nil error. Any
// other error (transport, HTTP status, malformed answer) is returned.
func ProbeImportSummary(ctx context.Context, c *Client) (ImportSupport, error) {
	data, err := Raw(ctx, c, ImportSummaryProbeOperationName, ImportSummaryProbe_Operation, map[string]any{})
	if err != nil {
		var gqlErrs GraphQLErrors
		if errors.As(err, &gqlErrs) {
			return ImportSupport{}, nil
		}
		return ImportSupport{}, err
	}
	return ParseImportSummaryProbe(data), nil
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
