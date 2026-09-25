package rearm

import (
	"regexp"
	"strings"
	"testing"
)

// The coordinator's stop release (task c0a2134c): the board views carry the setting as set and
// resolved, the export carries it as set, a hold says which stop placed it, the coordinator's
// release takes a note, and escalate is the seat's.
func TestTheCoordinatorsStopRelease(t *testing.T) {
	for name, op := range map[string]string{
		"list": AgentBoardsProgrammatic_Operation, "show": AgentBoardProgrammatic_Operation,
	} {
		if !strings.Contains(op, "coordinatorStopRelease") || !strings.Contains(op, "effectiveCoordinatorStopRelease") {
			t.Errorf("%s does not select the stop-release setting", name)
		}
	}
	if !strings.Contains(ExportBoard_Operation, "coordinatorStopRelease") {
		t.Error("the export leaves the stop-release setting behind")
	}
	for name, op := range map[string]string{
		"hold": AgentTaskHoldProgrammatic_Operation, "release": AgentTaskReleaseHoldProgrammatic_Operation,
		"escalate": AgentTaskEscalateHoldProgrammatic_Operation, "person": AgentTaskOperatorHold_Operation,
	} {
		if !regexp.MustCompile(`heldAt\s+stop\s`).MatchString(op) {
			t.Errorf("%s does not read which stop placed the hold", name)
		}
	}
	for _, want := range []string{"$note: String", "note: $note"} {
		if !strings.Contains(AgentTaskReleaseHoldProgrammatic_Operation, want) {
			t.Errorf("the coordinator's release lacks %q", want)
		}
	}
	for _, want := range []string{"$sessionUuid: ID!", "$reason: String!", "agentTaskEscalateHoldProgrammatic("} {
		if !strings.Contains(AgentTaskEscalateHoldProgrammatic_Operation, want) {
			t.Errorf("escalate lacks %q", want)
		}
	}
	if AgentTaskHoldStopNoProgress != "NO_PROGRESS" || AgentTaskHoldStopCycleCap != "CYCLE_CAP" || AgentTaskHoldStopBudget != "BUDGET" {
		t.Error("the stop kinds")
	}
}
