package rearm

import (
	"strings"
	"testing"
)

// Titles and descriptions (task fceb1e57): task reads carry the description after the title.
func TestTaskReadsCarryTheDescription(t *testing.T) {
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task list": AgentTasksProgrammatic_Operation,
		"task register": AgentTaskRegisterProgrammatic_Operation, "tasks by uuid": AgentTasksByUuidProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "externalRef title description sourceUrl") {
			t.Errorf("%s does not read the description", name)
		}
	}
}
