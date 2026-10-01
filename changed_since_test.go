package rearm

import (
	"strings"
	"testing"
)

// Which tasks changed since an instant (task 9540d3b6): task list takes the cursor, and task list
// and task show read each task's updatedAt, the next cursor.
func TestTaskReadsCarryUpdatedAtAndTheListTakesACursor(t *testing.T) {
	list := normalised(AgentTasksProgrammatic_Operation)
	if !strings.Contains(list, "$changedSince: DateTime") || !strings.Contains(list, "changedSince: $changedSince") {
		t.Error("task list does not pass changedSince")
	}
	for name, op := range map[string]string{
		"task list": AgentTasksProgrammatic_Operation, "task show": AgentTaskProgrammatic_Operation,
		"task show (several)": AgentTasksByUuidProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "requiredRolesSkipped updatedAt") {
			t.Errorf("%s does not read updatedAt", name)
		}
	}
}
