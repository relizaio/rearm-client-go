package rearm

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The by-commit operation as the generator reads it and as the client sends it (task TPF-99, mutation round):
// operations/cli.graphql is genqlient's input and generated.go its output, so the two must say the same
// query. A hand edit of either, or a regeneration that was not committed, makes them differ.
func TestTheByCommitOperationInTheGeneratedClientIsTheOneInTheSource(t *testing.T) {
	src, err := os.ReadFile("operations/cli.graphql")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)query GetReleaseByCommitProgrammatic\b[^{]*\{[^{}]*\}`).Find(src)
	if m == nil {
		t.Fatal("operations/cli.graphql has no GetReleaseByCommitProgrammatic query")
	}
	normal := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if got, want := normal(GetReleaseByCommitProgrammatic_Operation), normal(string(m)); got != want {
		t.Fatalf("generated operation differs from the source\n generated: %s\n source:    %s", got, want)
	}
}
