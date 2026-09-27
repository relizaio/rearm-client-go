package rearm

import (
	"strings"
	"testing"
)

// Advisory rounds (task e97fde56): task reads mark a round a role published on a task it did not
// hold, and name the role every round was published as.
func TestTaskReadsMarkAdvisoryRounds(t *testing.T) {
	for name, op := range map[string]string{"task show": AgentTaskProgrammatic_Operation, "tasks by uuid": AgentTasksByUuidProgrammatic_Operation} {
		if !strings.Contains(normalised(op), "document { specification path round task advisory publishedByRole") {
			t.Errorf("%s does not read whether a round is advisory and its role", name)
		}
	}
}
