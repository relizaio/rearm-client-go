package rearm

import (
	"strings"
	"testing"
)

// A correction (task cac71351): an item a person filed while approving at a gate, open work that
// never blocks. Every task read that lists review items carries the flag, so task show prints it.
func TestTaskReadsCarryTheCorrectionFlag(t *testing.T) {
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task show (several)": AgentTasksByUuidProgrammatic_Operation,
		"person task fields": AgentTasksOfBoard_Operation,
	} {
		n := normalised(op)
		if !strings.Contains(n, "openReviewItems { id priority status title location") ||
			!strings.Contains(n, "resolvedBy resolution correction }") {
			t.Errorf("%s does not read the correction flag of open review items", name)
		}
	}
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task show (several)": AgentTasksByUuidProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "resolvedBy resolution correction } } } }") {
			t.Errorf("%s does not read the correction flag of a document's review items", name)
		}
	}
	var f AgentTaskProgrammaticAgentTaskProgrammaticAgentTaskOpenReviewItemsBoardReviewItem
	if f.GetCorrection() != nil {
		t.Error("absent means not a correction")
	}
}
