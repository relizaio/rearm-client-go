package rearm

import (
	"strings"
	"testing"
)

// The board's spend breakdown (task RD2-8): a read of its own, so a board list or show does not price
// the board's usage rows unless asked, selecting every part that adds up to the total.
func TestTheSpendBreakdownReadSelectsEveryPart(t *testing.T) {
	n := normalised(AgentBoardSpendBreakdownProgrammatic_Operation)
	for _, field := range []string{"totalMicros", "costComplete", "coordinatorEstimateMicros", "unattributedMicros",
		"byRole { role costMicros closedHops openHops", "bySession { session agent role costMicros"} {
		if !strings.Contains(n, field) {
			t.Errorf("the breakdown read lacks %q", field)
		}
	}
	for name, op := range map[string]string{"board list": AgentBoardsProgrammatic_Operation, "board show": AgentBoardProgrammatic_Operation} {
		if strings.Contains(op, "spendBreakdown") {
			t.Errorf("%s prices the board's usage on every read", name)
		}
	}
}
