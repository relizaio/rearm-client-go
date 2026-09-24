// Package catalog works with declarative ReARM spec files (kind CATALOG, BRANCHES, BOARD and
// ROLE_PRESETS): load and save YAML, apply through the ReARM API, export from it, and print change
// sets.
// The same helpers back `rearm ... apply` in the CLI and the resources of the Terraform
// provider, so the two never disagree on what a file means.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"errors"
	rearm "github.com/relizaio/rearm-client-go"
	"github.com/vektah/gqlparser/v2/gqlerror"
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

// BoardFile is a `kind: BOARD` document: one board, its settings and its roles.
//
// It stays the map it was read into rather than a typed struct. A field set to null in the file
// clears it and a field left out leaves it alone (declarative-boards D15); a struct would turn
// both into the same zero value, so the map is what goes over the wire.
type BoardFile struct {
	Spec map[string]any
}

// RolePresetsFile is a `kind: ROLE_PRESETS` document: an organization's role presets. A map for
// the same reason as BoardFile.
type RolePresetsFile struct {
	Spec map[string]any
}

// MarshalJSON writes the file as its spec, so ToYAML and callers see the document itself.
func (f *BoardFile) MarshalJSON() ([]byte, error) { return json.Marshal(f.Spec) }

// MarshalJSON writes the file as its spec.
func (f *RolePresetsFile) MarshalJSON() ([]byte, error) { return json.Marshal(f.Spec) }

// Change is one entity-level outcome of an apply; Kind says which slice (and so which entity
// type) the entry is about.
type Change struct {
	Kind    Kind
	Name    string
	Action  rearm.DeclarativeAction
	Fields  []string
	Message string
	// Warnings are what the change leaves for someone to deal with, such as tasks waiting on a role
	// the file deactivates. Never a refusal.
	Warnings []string
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

// Load reads a YAML (or JSON) spec file and returns *CatalogFile, *BranchesFile, *BoardFile or
// *RolePresetsFile by its kind. A board or presets file has its `file:` and `promptFile:`
// references inlined, relative to the file's directory (see Inline).
func Load(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if err := Inline(f, filepath.Dir(path)); err != nil {
		return nil, err
	}
	return f, nil
}

// Parse decodes a YAML (or JSON) spec document and returns *CatalogFile, *BranchesFile, *BoardFile
// or *RolePresetsFile by its kind. References in a board or presets file are left as they are; Load
// inlines them, and a caller parsing bytes calls Inline itself.
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
	case rearm.DeclarativeKindBoard:
		spec, err := normalised(generic)
		if err != nil {
			return nil, fmt.Errorf("spec (%s): %w", kind, err)
		}
		if _, ok := spec["version"]; !ok {
			spec["version"] = 1
		}
		return &BoardFile{Spec: spec}, nil
	case rearm.DeclarativeKindRolePresets:
		spec, err := normalised(generic)
		if err != nil {
			return nil, fmt.Errorf("spec (%s): %w", kind, err)
		}
		if _, ok := spec["version"]; !ok {
			spec["version"] = 1
		}
		return &RolePresetsFile{Spec: spec}, nil
	case "":
		return nil, fmt.Errorf("spec: missing 'kind' (expected one of %s)", kindList())
	default:
		return nil, fmt.Errorf("spec: unsupported kind %q (expected one of %s)", kind, kindList())
	}
}

func kindList() string {
	return strings.Join([]string{string(rearm.DeclarativeKindCatalog), string(rearm.DeclarativeKindBranches),
		string(rearm.DeclarativeKindBoard), string(rearm.DeclarativeKindRolePresets)}, ", ")
}

// normalised round-trips a YAML-decoded document through JSON, so nested maps are
// map[string]any, numbers are JSON numbers and nulls stay null -- the shape the API takes.
func normalised(generic map[string]any) (map[string]any, error) {
	js, err := json.Marshal(generic)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(js, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Inline replaces the references a board or presets file may carry with what they point at, so
// the spec that leaves the client is complete (declarative-boards D3):
//
//   - a role or preset entry `file: roles/coder.yaml` becomes that file's contents;
//   - `promptFile: roles/coder.md` on a role or preset becomes `prompt`;
//   - `coordinatorPromptFile:` on a board becomes `coordinatorPrompt`.
//
// Paths are relative to baseDir, the spec file's directory, and must stay inside it: a reference
// that escapes it, is absolute or does not exist is an error. Other kinds pass through unchanged.
func Inline(file any, baseDir string) error {
	var spec map[string]any
	var listKey string
	switch f := file.(type) {
	case *BoardFile:
		spec, listKey = f.Spec, "roles"
	case *RolePresetsFile:
		spec, listKey = f.Spec, "presets"
	default:
		return nil
	}
	if ref, ok := spec["coordinatorPromptFile"]; ok {
		text, err := readRef(baseDir, ref, "coordinatorPromptFile")
		if err != nil {
			return err
		}
		spec["coordinatorPrompt"] = text
		delete(spec, "coordinatorPromptFile")
	}
	entries, _ := spec[listKey].([]any)
	for i, e := range entries {
		entry, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if ref, ok := entry["file"]; ok {
			text, err := readRef(baseDir, ref, fmt.Sprintf("%s[%d].file", listKey, i))
			if err != nil {
				return err
			}
			var loaded map[string]any
			if err := yaml.Unmarshal([]byte(text), &loaded); err != nil {
				return fmt.Errorf("%s[%d].file %v: %w", listKey, i, ref, err)
			}
			if loaded, err = normalised(loaded); err != nil {
				return err
			}
			// Anything written beside the reference wins over the file's own value.
			for k, v := range entry {
				if k != "file" {
					loaded[k] = v
				}
			}
			entry = loaded
		}
		if ref, ok := entry["promptFile"]; ok {
			text, err := readRef(baseDir, ref, fmt.Sprintf("%s[%d].promptFile", listKey, i))
			if err != nil {
				return err
			}
			entry["prompt"] = text
			delete(entry, "promptFile")
		}
		entries[i] = entry
	}
	return nil
}

// References lists the `file:`, `promptFile:` and `coordinatorPromptFile:` entries a board or
// presets file still carries -- what a caller that cannot read files (an upload) must refuse.
func References(file any) []string {
	var spec map[string]any
	var listKey string
	switch f := file.(type) {
	case *BoardFile:
		spec, listKey = f.Spec, "roles"
	case *RolePresetsFile:
		spec, listKey = f.Spec, "presets"
	default:
		return nil
	}
	var out []string
	if ref, ok := spec["coordinatorPromptFile"]; ok {
		out = append(out, fmt.Sprintf("coordinatorPromptFile: %v", ref))
	}
	entries, _ := spec[listKey].([]any)
	for i, e := range entries {
		entry, _ := e.(map[string]any)
		for _, k := range []string{"file", "promptFile"} {
			if ref, ok := entry[k]; ok {
				out = append(out, fmt.Sprintf("%s[%d].%s: %v", listKey, i, k, ref))
			}
		}
	}
	return out
}

// readRef reads a referenced file, refusing one outside baseDir.
func readRef(baseDir string, ref any, what string) (string, error) {
	rel, ok := ref.(string)
	if !ok || strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("%s must be a path", what)
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%s %q must be relative to the spec file", what, rel)
	}
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	target := filepath.Join(base, rel)
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	} else {
		return "", fmt.Errorf("%s %q: %w", what, rel, err)
	}
	inside, err := filepath.Rel(base, target)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s %q leaves the spec file's directory", what, rel)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return "", fmt.Errorf("%s %q: %w", what, rel, err)
	}
	return string(b), nil
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
	case *BoardFile:
		if refs := References(f); len(refs) > 0 {
			return nil, fmt.Errorf("apply: references not inlined: %s", strings.Join(refs, "; "))
		}
		resp, err := rearm.ApplyBoard(ctx, c, &f.Spec, &dryRun, source)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.ApplyBoardProgrammatic == nil {
			return nil, fmt.Errorf("apply: empty response")
		}
		return fromResult(&resp.ApplyBoardProgrammatic.ApplyResultFields), nil
	case *RolePresetsFile:
		if refs := References(f); len(refs) > 0 {
			return nil, fmt.Errorf("apply: references not inlined: %s", strings.Join(refs, "; "))
		}
		resp, err := rearm.ApplyRolePresets(ctx, c, &f.Spec, &dryRun, source)
		if err != nil {
			return nil, err
		}
		if resp == nil || resp.ApplyRolePresetsProgrammatic == nil {
			return nil, fmt.Errorf("apply: empty response")
		}
		return fromResult(&resp.ApplyRolePresetsProgrammatic.ApplyResultFields), nil
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

// ExportBoard fetches one board of the key's organization as a board file, by name or uuid.
func ExportBoard(ctx context.Context, c *rearm.Client, board string) (*BoardFile, error) {
	uuid := board
	list, err := rearm.AgentBoardsProgrammatic(ctx, c)
	if err != nil {
		return nil, err
	}
	matched := false
	for _, b := range list.AgentBoardsProgrammatic {
		if b == nil || b.Uuid == nil {
			continue
		}
		if *b.Uuid == board || (b.Name != nil && *b.Name == board) {
			uuid, matched = *b.Uuid, true
			break
		}
	}
	if !matched {
		return nil, fmt.Errorf("export: no board named %q in this organization (not found)", board)
	}
	resp, err := rearm.ExportBoard(ctx, c, uuid)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.AgentBoardSpecProgrammatic == nil {
		return nil, fmt.Errorf("export: empty response")
	}
	spec := map[string]any{}
	if err := roundTrip(resp.AgentBoardSpecProgrammatic, &spec); err != nil {
		return nil, err
	}
	spec["kind"] = string(rearm.DeclarativeKindBoard)
	return &BoardFile{Spec: spec}, nil
}

// ExportRolePresets fetches the key's organization's role presets as a presets file.
func ExportRolePresets(ctx context.Context, c *rearm.Client) (*RolePresetsFile, error) {
	resp, err := rearm.ExportRolePresets(ctx, c)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.AgentRolePresetsSpecProgrammatic == nil {
		return nil, fmt.Errorf("export: empty response")
	}
	spec := map[string]any{}
	if err := roundTrip(resp.AgentRolePresetsSpecProgrammatic, &spec); err != nil {
		return nil, err
	}
	spec["kind"] = string(rearm.DeclarativeKindRolePresets)
	return &RolePresetsFile{Spec: spec}, nil
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
		line := fmt.Sprintf("  %-9s %-9s %s", ch.Action, entityOf(ch), ch.Name)
		if len(ch.Fields) > 0 {
			line += " [" + strings.Join(ch.Fields, ", ") + "]"
		}
		if msg := messageOf(ch); msg != "" {
			line += " — " + msg
		}
		b.WriteString(line + "\n")
		for _, w := range ch.Warnings {
			b.WriteString("            warning: " + w + "\n")
		}
	}
	return b.String()
}

// entityOf names the entity type a change entry refers to, for humans. A board file's entries are
// the board and its roles; the server says which in the message.
func entityOf(ch Change) string {
	switch ch.Kind {
	case rearm.DeclarativeKindCatalog:
		return "component"
	case rearm.DeclarativeKindBranches:
		return "branch"
	case rearm.DeclarativeKindBoard:
		// A board file's only archives are roles it no longer lists; the board is never archived.
		if ch.Message == "role" || ch.Action == rearm.DeclarativeActionArchive {
			return "role"
		}
		return "board"
	case rearm.DeclarativeKindRolePresets:
		return "preset"
	default:
		return string(ch.Kind)
	}
}

// messageOf drops the entity marker a board file's changes carry, since entityOf already shows it.
func messageOf(ch Change) string {
	if ch.Kind == rearm.DeclarativeKindBoard && (ch.Message == "role" || ch.Message == "board") {
		return ""
	}
	return ch.Message
}

func fromResult(r *rearm.ApplyResultFields) *Result {
	out := &Result{Kind: r.Kind, DryRun: r.DryRun, SpecHash: r.SpecHash, Created: r.Created, Updated: r.Updated,
		Unchanged: r.Unchanged, Archived: r.Archived, Errors: r.Errors}
	for _, ch := range r.Changes {
		if ch == nil {
			continue
		}
		c := Change{Kind: ch.Kind, Name: ch.Name, Action: ch.Action, Fields: ch.Fields, Warnings: ch.Warnings}
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
	topLevelKeyOrder = []string{"kind", "version", "authoritative", "name", "description", "target", "component",
		"components", "branches", "sources", "settings", "coordinatorPrompt", "roles", "presets"}
	entryKeyOrder = []string{"name", "type", "component", "branch", "release", "pattern", "orderIndex", "kind",
		"necessity", "humanGate", "prompt"}
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

// IsNotFound reports whether err is ReARM saying that the entity does not exist: a GraphQL error
// classified NOT_FOUND, or one whose message says so. Callers (the Terraform provider, scripts)
// use it to tell "gone" apart from a real failure instead of matching message text themselves.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var list gqlerror.List
	if errors.As(err, &list) {
		for _, e := range list {
			if e == nil {
				continue
			}
			if t, ok := e.Extensions["errorType"].(string); ok && strings.EqualFold(t, "NOT_FOUND") {
				return true
			}
			if strings.Contains(strings.ToLower(e.Message), "not found") {
				return true
			}
		}
		return false
	}
	var single *gqlerror.Error
	if errors.As(err, &single) && single != nil {
		if t, ok := single.Extensions["errorType"].(string); ok && strings.EqualFold(t, "NOT_FOUND") {
			return true
		}
		return strings.Contains(strings.ToLower(single.Message), "not found")
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}
