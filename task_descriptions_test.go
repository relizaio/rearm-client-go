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

// The person's reads and the snapshot carry it too (tests/fceb1e57/run-1.md T-2): PersonTaskFields
// is behind every rearm boards verb, and the snapshot's task entry is what a board-wide read shows.
func TestPersonReadsAndTheSnapshotCarryTheDescription(t *testing.T) {
	for name, op := range map[string]string{
		"boards register": AgentTaskRegister_Operation, "boards tasks": AgentTasksOfBoard_Operation,
	} {
		if !strings.Contains(normalised(op), "externalRef title description status") {
			t.Errorf("%s does not read the description", name)
		}
	}
	if !strings.Contains(normalised(AgentBoardSnapshotProgrammatic_Operation), "task { key number uuid externalRef title description status") {
		t.Error("the snapshot's task entry does not read the description")
	}
}
