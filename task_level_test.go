package rearm

import (
	"strings"
	"testing"
)

// Task work level (RD2-1): every task read carries the work level, the one the board reads, and who set it;
// both set-work-level operations exist.
func TestTaskReadsCarryTheWorkLevel(t *testing.T) {
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task list": AgentTasksProgrammatic_Operation,
		"tasks by uuid": AgentTasksByUuidProgrammatic_Operation, "boards tasks": AgentTasksOfBoard_Operation,
	} {
		n := normalised(op)
		if !strings.Contains(n, "workLevel effectiveWorkLevel workLevelSetBy {") || !strings.Contains(n, "workLevelSetAt") {
			t.Errorf("%s does not read workLevel, effectiveWorkLevel, workLevelSetBy and workLevelSetAt", name)
		}
	}
	if !strings.Contains(normalised(AgentBoardSnapshotProgrammatic_Operation), "roleUuid workLevel effectiveWorkLevel orderIndex") {
		t.Error("the snapshot's task entry does not read effectiveWorkLevel")
	}
	if !strings.Contains(normalised(AgentTaskSetWorkLevel_Operation), "agentTaskSetWorkLevel(taskUuid: $taskUuid, workLevel: $workLevel)") {
		t.Error("no person set-work-level operation")
	}
	if !strings.Contains(normalised(AgentTaskSetWorkLevelProgrammatic_Operation),
		"agentTaskSetWorkLevelProgrammatic(taskUuid: $taskUuid, sessionUuid: $sessionUuid, workLevel: $workLevel)") {
		t.Error("no seat set-work-level operation")
	}
}
