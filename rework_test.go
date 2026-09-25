package rearm

import (
	"regexp"
	"testing"
)

// A finding names the element it is about, and every read of a task's findings carries it
// (elements.md §8): the documents' rounds, openFindings and openQuestions.
func TestFindingReadsSelectTheElementAFindingNames(t *testing.T) {
	locations := regexp.MustCompile(`location \{[^}]*\}`).FindAllString(AgentTaskProgrammatic_Operation, -1)
	if len(locations) != 3 {
		t.Fatalf("expected three location selections, got %d", len(locations))
	}
	for _, l := range locations {
		if !regexp.MustCompile(`\belement\b`).MatchString(l) {
			t.Errorf("a location selection lacks element: %q", l)
		}
	}
}

func TestTheElementViewReadsDependentsAndHistory(t *testing.T) {
	op := AgentTaskElementProgrammatic_Operation
	for _, want := range []string{"dependentsOf(element: $element, depth: $depth)", "truncated", "via {", "distance",
		"elementHistory(element: $element)", "changed"} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(op) {
			t.Errorf("element view lacks %q", want)
		}
	}
}
