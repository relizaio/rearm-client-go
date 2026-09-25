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
