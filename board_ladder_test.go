package rearm

import (
	"strings"
	"testing"
)

// The level ladder (task RD3-6): the board reads and the export carry it, so board show prints it and a board file
// round-trips it.
func TestLadderOperations(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	block := "ladder { levels { number name description } prompt }"
	for name, op := range map[string]string{"list": AgentBoardsProgrammatic_Operation, "show": AgentBoardProgrammatic_Operation,
		"export": ExportBoard_Operation} {
		if !strings.Contains(norm(op), block) {
			t.Errorf("%s does not read the ladder", name)
		}
	}
	auth := norm(AgentTaskAuthorizeProgrammatic_Operation)
	if !strings.Contains(auth, "$workLevel: Int") || !strings.Contains(auth, "workLevel: $workLevel") {
		t.Errorf("authorize does not carry the work level: %s", auth)
	}
}
