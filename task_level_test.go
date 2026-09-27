package rearm

import (
	"strings"
	"testing"
)

// Task level (RD2-1): every task read carries the level, the one the board reads, and who set it;
// both set-level operations exist.
func TestTaskReadsCarryTheLevel(t *testing.T) {
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task list": AgentTasksProgrammatic_Operation,
		"tasks by uuid": AgentTasksByUuidProgrammatic_Operation, "boards tasks": AgentTasksOfBoard_Operation,
	} {
		n := normalised(op)
		if !strings.Contains(n, "level effectiveLevel levelSetBy {") || !strings.Contains(n, "levelSetAt") {
			t.Errorf("%s does not read level, effectiveLevel, levelSetBy and levelSetAt", name)
		}
	}
	if !strings.Contains(normalised(AgentBoardSnapshotProgrammatic_Operation), "roleUuid level effectiveLevel orderIndex") {
		t.Error("the snapshot's task entry does not read effectiveLevel")
	}
	if !strings.Contains(normalised(AgentTaskSetLevel_Operation), "agentTaskSetLevel(taskUuid: $taskUuid, level: $level)") {
		t.Error("no person set-level operation")
	}
	if !strings.Contains(normalised(AgentTaskSetLevelProgrammatic_Operation),
		"agentTaskSetLevelProgrammatic(taskUuid: $taskUuid, sessionUuid: $sessionUuid, level: $level)") {
		t.Error("no seat set-level operation")
	}
}
