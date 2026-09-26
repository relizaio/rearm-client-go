package rearm

import (
	"strings"
	"testing"
)

// Which review promoted a document (task fda2c9f1): task show reads, on each sign-off, what it
// reviewed and what a guard kept back.
func TestTaskShowReadsWhatEachSignOffReviewed(t *testing.T) {
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task show (several)": AgentTasksByUuidProgrammatic_Operation,
	} {
		n := normalised(op)
		for _, want := range []string{"reviewedInputs { release specification round promotedTo }",
			"refusedPromotions { release specification round reason }"} {
			if !strings.Contains(n, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
	}
}
