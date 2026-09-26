package rearm

import (
	"strings"
	"testing"
)

// A correction (task cac71351): an item a person filed while approving at a gate, open work that
// never blocks. Every task read that lists findings carries the flag, so task show prints it.
func TestTaskReadsCarryTheCorrectionFlag(t *testing.T) {
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task show (several)": AgentTasksByUuidProgrammatic_Operation,
		"person task fields": AgentTasksOfBoard_Operation,
	} {
		n := normalised(op)
		if !strings.Contains(n, "openFindings { id priority status title location") ||
			!strings.Contains(n, "resolvedBy resolution correction }") {
			t.Errorf("%s does not read the correction flag of open findings", name)
		}
	}
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task show (several)": AgentTasksByUuidProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "resolvedBy resolution correction } } } }") {
			t.Errorf("%s does not read the correction flag of a document's findings", name)
		}
	}
	var f AgentTaskProgrammaticAgentTaskProgrammaticAgentTaskOpenFindingsFinding
	if f.GetCorrection() != nil {
		t.Error("absent means not a correction")
	}
}
