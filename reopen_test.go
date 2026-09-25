package rearm

import (
	"strings"
	"testing"
)

// The seat's reopen sends the role and the reason and reads back what the reopen recorded, so the
// CLI can show which reopen this was.
func TestTheReopenOperationSendsTheRoleAndReasonAndReadsTheReopen(t *testing.T) {
	op := AgentTaskReopenProgrammatic_Operation
	for _, want := range []string{"$role: String!", "$reason: String!", "role: $role", "reason: $reason",
		"reopenedAt", "reopenCount", "reopens {"} {
		if !strings.Contains(op, want) {
			t.Errorf("reopen operation lacks %q", want)
		}
	}
}

// A single task's reads carry its PRs' delivery state, so `task show` and a sign-off say why a
// task is DELIVERING rather than COMPLETED.
func TestSingleTaskReadsSelectThePullRequests(t *testing.T) {
	for name, op := range map[string]string{
		"show": AgentTaskProgrammatic_Operation, "linkpr": AgentTaskLinkPrProgrammatic_Operation,
		"signoff": AgentTaskSignOffProgrammatic_Operation, "complete": AgentTaskCompleteProgrammatic_Operation,
		"reopen": AgentTaskReopenProgrammatic_Operation,
	} {
		if !strings.Contains(op, "pullRequests {") || !strings.Contains(op, "registered") {
			t.Errorf("%s does not select pullRequests", name)
		}
	}
}
