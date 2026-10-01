package rearm

import (
	"strings"
	"testing"
)

// Stale detection (task RD3-4): the board reads and the export carry the staleness block, the task
// reads carry the unassignments, and both unassign operations exist.
func TestStalenessAndUnassignOperations(t *testing.T) {
	block := "staleness { roleUnstaffedMinutes hopNoProgressMinutes deliveryStuckMinutes seatSilentMinutes repeatMinutes investigationOverdueMinutes }"
	for name, op := range map[string]string{
		"board list": AgentBoardsProgrammatic_Operation, "board show": AgentBoardProgrammatic_Operation,
		"export": ExportBoard_Operation,
	} {
		if !strings.Contains(normalised(op), block) {
			t.Errorf("%s does not read the staleness block", name)
		}
	}
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "tasks by uuid": AgentTasksByUuidProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "unassignments { role session agent assignedAt unassignedAt unassignedBy {") {
			t.Errorf("%s does not read the unassignments", name)
		}
	}
	if !strings.Contains(normalised(AgentTaskUnassign_Operation), "agentTaskUnassign(taskUuid: $taskUuid, reason: $reason)") {
		t.Error("no person unassign operation")
	}
	if !strings.Contains(normalised(AgentTaskUnassignProgrammatic_Operation),
		"agentTaskUnassignProgrammatic(taskUuid: $taskUuid, sessionUuid: $sessionUuid, reason: $reason)") {
		t.Error("no seat unassign operation")
	}
}
