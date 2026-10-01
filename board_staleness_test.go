package rearm

import (
	"strings"
	"testing"
)

// Stale detection (task RD3-4): the board reads and the export carry the staleness block, the task
// reads carry the released assignments, and both release operations exist.
func TestStalenessAndReleaseOperations(t *testing.T) {
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
		if !strings.Contains(normalised(op), "releasedAssignments { role session agent assignedAt releasedAt releasedBy {") {
			t.Errorf("%s does not read the released assignments", name)
		}
	}
	if !strings.Contains(normalised(AgentTaskReleaseAssignment_Operation), "agentTaskReleaseAssignment(taskUuid: $taskUuid, reason: $reason)") {
		t.Error("no person release operation")
	}
	if !strings.Contains(normalised(AgentTaskReleaseAssignmentProgrammatic_Operation),
		"agentTaskReleaseAssignmentProgrammatic(taskUuid: $taskUuid, sessionUuid: $sessionUuid, reason: $reason)") {
		t.Error("no seat release operation")
	}
}
