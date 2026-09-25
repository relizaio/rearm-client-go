package rearm

import (
	"strings"
	"testing"
)

// The board's tested heads (task 3b97ccfd): task show reads the heads the newest passing round
// covered, each linked PR's current head, and the heads a round names; every other read of a
// task's PRs carries the current head too, so a coordinator can compare them.
func TestTheTaskReadCarriesTestedAndCurrentHeads(t *testing.T) {
	show := normalised(AgentTaskProgrammatic_Operation)
	// "registered head" rather than "registered head }": task show's PR selection goes on to the
	// attestation (task 18c5c293).
	for _, want := range []string{"testedHeads { pr head }", "registered head ", "tested { pr head }"} {
		if !strings.Contains(show, want) {
			t.Errorf("task show lacks %q", want)
		}
	}
	for name, op := range map[string]string{
		"signoff": AgentTaskSignOffProgrammatic_Operation, "complete": AgentTaskCompleteProgrammatic_Operation,
		"reopen": AgentTaskReopenProgrammatic_Operation, "linkpr": AgentTaskLinkPrProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "registered head }") {
			t.Errorf("%s does not read the PR's current head", name)
		}
	}
}

// normalised is an operation with its whitespace collapsed, as genqlient reformats selections.
func normalised(op string) string {
	return strings.Join(strings.Fields(op), " ")
}
