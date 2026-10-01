package rearm

import (
	"strings"
	"testing"
)

// What the board charges a task (task 02bfab7c): task show reads its budget, its coordinator
// share and spentMicros, the figure the budget is held to.
func TestTaskShowReadsTheTaskSpend(t *testing.T) {
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task show (several)": AgentTasksByUuidProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "budgetMicros coordinatorEstimateMicros spentMicros") {
			t.Errorf("%s does not read the task's spend", name)
		}
	}
}
