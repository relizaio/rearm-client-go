package rearm

import (
	"regexp"
	"strings"
	"testing"
)

// The coordinator's stop lift (task c0a2134c): the board views carry the setting as set and
// resolved, the export carries it as set, a hold says which stop placed it, the coordinator's
// lift takes a note, and escalate is the seat's.
func TestTheCoordinatorsStopLift(t *testing.T) {
	for name, op := range map[string]string{
		"list": AgentBoardsProgrammatic_Operation, "show": AgentBoardProgrammatic_Operation,
	} {
		if !strings.Contains(op, "coordinatorStopLift") || !strings.Contains(op, "effectiveCoordinatorStopLift") {
			t.Errorf("%s does not select the stop-lift setting", name)
		}
	}
	if !strings.Contains(ExportBoard_Operation, "coordinatorStopLift") {
		t.Error("the export leaves the stop-lift setting behind")
	}
	for name, op := range map[string]string{
		"hold": AgentTaskHoldProgrammatic_Operation, "lift": AgentTaskLiftHoldProgrammatic_Operation,
		"escalate": AgentTaskEscalateHoldProgrammatic_Operation, "person": AgentTaskOperatorHold_Operation,
	} {
		if !regexp.MustCompile(`heldAt\s+stop\s`).MatchString(op) {
			t.Errorf("%s does not read which stop placed the hold", name)
		}
	}
	for _, want := range []string{"$note: String", "note: $note"} {
		if !strings.Contains(AgentTaskLiftHoldProgrammatic_Operation, want) {
			t.Errorf("the coordinator's lift lacks %q", want)
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
