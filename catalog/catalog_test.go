package catalog

import (
	"strings"
	"testing"

	rearm "github.com/relizaio/rearm-client-go"
)

const sampleCatalog = `
kind: Catalog
authoritative: true
components:
  - name: api
    type: COMPONENT
    versionSchema: semver
    identifiers:
      - idType: PURL
        idValue: pkg:generic/acme/api
`

const sampleBranches = `
kind: Branches
component: platform
branches:
  - name: stable
    type: FEATURE
    dependencies:
      - component: api
        branch: main
    dependencyPatterns:
      - pattern: "^acme-.*"
`

func TestParseCatalog(t *testing.T) {
	f, err := Parse([]byte(sampleCatalog))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := f.(*CatalogFile)
	if !ok {
		t.Fatalf("expected *CatalogFile, got %T", f)
	}
	if c.Version != 1 || c.Authoritative == nil || !*c.Authoritative || len(c.Components) != 1 {
		t.Fatalf("unexpected catalog: %+v", c.CatalogSpecInput)
	}
	comp := c.Components[0]
	if comp.Name != "api" || comp.Type != rearm.ComponentTypeComponent || comp.VersionSchema == nil || *comp.VersionSchema != "semver" {
		t.Fatalf("unexpected component: %+v", comp)
	}
	if comp.FeatureBranchVersioning != nil {
		t.Fatalf("unset field must stay nil (unmanaged), got %v", *comp.FeatureBranchVersioning)
	}
	if len(comp.Identifiers) != 1 || comp.Identifiers[0].IdValue == nil || *comp.Identifiers[0].IdValue != "pkg:generic/acme/api" {
		t.Fatalf("identifiers not parsed: %+v", comp.Identifiers)
	}
}

func TestParseBranchesAndYAMLRoundTrip(t *testing.T) {
	f, err := Parse([]byte(sampleBranches))
	if err != nil {
		t.Fatal(err)
	}
	b := f.(*BranchesFile)
	if b.Component != "platform" || len(b.Branches) != 1 || len(b.Branches[0].Dependencies) != 1 || len(b.Branches[0].DependencyPatterns) != 1 {
		t.Fatalf("unexpected branches: %+v", b.BranchesSpecInput)
	}
	if b.Authoritative != nil {
		t.Fatalf("authoritative must stay unset (server default) when the file omits it")
	}
	y, err := ToYAML(b)
	if err != nil {
		t.Fatal(err)
	}
	s := string(y)
	if !strings.HasPrefix(s, "kind: Branches\nversion: 1\ncomponent: platform\n") {
		t.Fatalf("yaml key order wrong:\n%s", s)
	}
	if strings.Contains(s, "null") {
		t.Fatalf("nulls must be stripped:\n%s", s)
	}
	again, err := Parse(y)
	if err != nil {
		t.Fatal(err)
	}
	if again.(*BranchesFile).Branches[0].Dependencies[0].Branch == nil || *again.(*BranchesFile).Branches[0].Dependencies[0].Branch != "main" {
		t.Fatalf("round trip lost the dependency branch")
	}
}

func TestParseRejectsUnknownKind(t *testing.T) {
	if _, err := Parse([]byte("kind: Instances\n")); err == nil {
		t.Fatal("expected an error for an unsupported kind")
	}
	if _, err := Parse([]byte("version: 1\n")); err == nil {
		t.Fatal("expected an error for a missing kind")
	}
}
