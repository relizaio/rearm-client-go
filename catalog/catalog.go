// Package catalog works with declarative ReARM spec files (kind: Catalog, kind: Branches):
// load and save YAML, apply through the ReARM API, export from it, and print change sets.
// The same helpers back `rearm ... apply` in the CLI and the resources of the Terraform
// provider, so the two never disagree on what a file means.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	rearm "github.com/relizaio/rearm-client-go"
)

// Kind values are the DeclarativeKind enum of the programmatic schema (CATALOG, BRANCHES);
// spec files carry them verbatim in their `kind` field.
type Kind = rearm.DeclarativeKind

// Source records where a spec came from (repo, path, commit) and is stamped on every row the apply touches.
type Source = rearm.DeclarativeSourceInput

// CatalogFile is a `kind: CATALOG` document: components and products of one organization.
type CatalogFile struct {
	rearm.CatalogSpecInput
}

// BranchesFile is a `kind: BRANCHES` document: branches (or feature sets) of one component (or product).
type BranchesFile struct {
	rearm.BranchesSpecInput
}

// Change is one entity-level outcome of an apply.
type Change struct {
	Kind    string
	Name    string
	Action  rearm.DeclarativeAction
	Fields  []string
	Message string
}

// Result is the normalised outcome of an apply, identical for both kinds.
type Result struct {
	Kind      Kind
	DryRun    bool
	SpecHash  string
	Created   int
	Updated   int
	Unchanged int
	Archived  int
	Errors    int
	Changes   []Change
}

// Load reads a YAML (or JSON) spec file and returns *CatalogFile or *BranchesFile by its kind.
func Load(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}

// Parse decodes a YAML (or JSON) spec document and returns *CatalogFile or *BranchesFile by its kind.
func Parse(raw []byte) (any, error) {
	var generic map[string]any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("spec: %w", err)
	}
	kind, _ := generic["kind"].(string)
	js, err := json.Marshal(generic)
	if err != nil {
		return nil, err
	}
	switch Kind(kind) {
	case rearm.DeclarativeKindCatalog:
		var f CatalogFile
		if err := json.Unmarshal(js, &f); err != nil {
			return nil, fmt.Errorf("spec (%s): %w", kind, err)
		}
		if f.Version == 0 {
			f.Version = 1
		}
		return &f, nil
	case rearm.DeclarativeKindBranches:
		var f BranchesFile
		if err := json.Unmarshal(js, &f); err != nil {
			return nil, fmt.Errorf("spec (%s): %w", kind, err)
		}
		if f.Version == 0 {
			f.Version = 1
		}
		return &f, nil
	case "":
		return nil, fmt.Errorf("spec: missing 'kind' (expected %s or %s)", rearm.DeclarativeKindCatalog, rearm.DeclarativeKindBranches)
	default:
		return nil, fmt.Errorf("spec: unsupported kind %q (expected %s or %s)", kind, rearm.DeclarativeKindCatalog, rearm.DeclarativeKindBranches)
	}
}

// Apply sends a loaded spec to ReARM. With dryRun the server computes the change set without writing.
func Apply(ctx context.Context, c *rearm.Client, file any, dryRun bool, source *Source) (*Result, error) {
	switch f := file.(type) {
	case *CatalogFile:
		resp, err := rearm.ApplyCatalog(ctx, c, &f.CatalogSpecInput, &dryRun, source)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.ApplyCatalogProgrammatic == nil {
			return nil, fmt.Errorf("apply: empty response")
		}
		return fromResult(&resp.ApplyCatalogProgrammatic.ApplyResultFields), nil
	case *BranchesFile:
		resp, err := rearm.ApplyBranches(ctx, c, &f.BranchesSpecInput, &dryRun, source)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.ApplyBranchesProgrammatic == nil {
			return nil, fmt.Errorf("apply: empty response")
		}
		return fromResult(&resp.ApplyBranchesProgrammatic.ApplyResultFields), nil
	default:
		return nil, fmt.Errorf("apply: unsupported spec type %T", file)
	}
}

// ExportCatalog fetches the org's components as a Catalog file. With names it exports only those
// components and the file is marked non-authoritative.
func ExportCatalog(ctx context.Context, c *rearm.Client, names []string) (*CatalogFile, error) {
	resp, err := rearm.ExportCatalog(ctx, c, names)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.ExportCatalogProgrammatic == nil {
		return nil, fmt.Errorf("export: empty response")
	}
	var f CatalogFile
	if err := roundTrip(resp.ExportCatalogProgrammatic, &f.CatalogSpecInput); err != nil {
		return nil, err
	}
	f.Kind = rearm.DeclarativeKindCatalog
	return &f, nil
}

// ExportBranches fetches the branches (feature sets) of one component (product) as a Branches file.
func ExportBranches(ctx context.Context, c *rearm.Client, component string) (*BranchesFile, error) {
	resp, err := rearm.ExportBranches(ctx, c, component)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.ExportBranchesProgrammatic == nil {
		return nil, fmt.Errorf("export: empty response")
	}
	var f BranchesFile
	if err := roundTrip(resp.ExportBranchesProgrammatic, &f.BranchesSpecInput); err != nil {
		return nil, err
	}
	f.Kind = rearm.DeclarativeKindBranches
	return &f, nil
}

// ToYAML renders a spec file for humans and git: nulls dropped, kind first, then version.
func ToYAML(file any) ([]byte, error) {
	js, err := json.Marshal(file)
	if err != nil {
		return nil, err
	}
	var generic map[string]any
	if err := json.Unmarshal(js, &generic); err != nil {
		return nil, err
	}
	node := toNode(stripNulls(generic), true)
	return yaml.Marshal(node)
}

// Format renders a Result as a compact, line-per-change report.
func Format(r *Result) string {
	var b strings.Builder
	mode := "applied"
	if r.DryRun {
		mode = "dry run"
	}
	fmt.Fprintf(&b, "%s (%s): %d to create, %d to update, %d unchanged, %d to archive, %d errors\n",
		r.Kind, mode, r.Created, r.Updated, r.Unchanged, r.Archived, r.Errors)
	for _, ch := range r.Changes {
		line := fmt.Sprintf("  %-9s %s %s", ch.Action, ch.Kind, ch.Name)
		if len(ch.Fields) > 0 {
			line += " [" + strings.Join(ch.Fields, ", ") + "]"
		}
		if ch.Message != "" {
			line += " — " + ch.Message
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func fromResult(r *rearm.ApplyResultFields) *Result {
	out := &Result{Kind: r.Kind, DryRun: r.DryRun, SpecHash: r.SpecHash, Created: r.Created, Updated: r.Updated,
		Unchanged: r.Unchanged, Archived: r.Archived, Errors: r.Errors}
	for _, ch := range r.Changes {
		if ch == nil {
			continue
		}
		c := Change{Kind: ch.Kind, Name: ch.Name, Action: ch.Action, Fields: ch.Fields}
		if ch.Message != nil {
			c.Message = *ch.Message
		}
		out.Changes = append(out.Changes, c)
	}
	return out
}

// roundTrip copies an export response into the matching input struct through JSON; fields the
// input does not know (provenance) fall away, which is what a re-applicable file wants.
func roundTrip(from any, to any) error {
	js, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(js, to)
}

func stripNulls(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if x == nil {
				continue
			}
			out[k] = stripNulls(x)
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, x := range t {
			out = append(out, stripNulls(x))
		}
		return out
	default:
		return v
	}
}

// Key order for the document envelope and for nested entries; anything else follows alphabetically.
var (
	topLevelKeyOrder = []string{"kind", "version", "authoritative", "component", "components", "branches"}
	entryKeyOrder    = []string{"name", "type", "component", "branch", "release", "pattern"}
)

// toNode builds a yaml.Node so map keys keep a stable, human order: preferred keys first, then alphabetical.
func toNode(v any, top bool) *yaml.Node {
	switch t := v.(type) {
	case map[string]any:
		preferred := entryKeyOrder
		if top {
			preferred = topLevelKeyOrder
		}
		n := &yaml.Node{Kind: yaml.MappingNode}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		rank := func(k string) int {
			for i, p := range preferred {
				if p == k {
					return i
				}
			}
			return len(preferred)
		}
		sort.SliceStable(keys, func(i, j int) bool {
			ri, rj := rank(keys[i]), rank(keys[j])
			if ri != rj {
				return ri < rj
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys {
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, toNode(t[k], false))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode}
		for _, x := range t {
			n.Content = append(n.Content, toNode(x, false))
		}
		return n
	default:
		n := &yaml.Node{}
		_ = n.Encode(v)
		return n
	}
}
