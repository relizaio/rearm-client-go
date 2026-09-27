package rearm

import (
	"strings"
	"testing"
)

// Board enforcement (task d8e7bd7e): board reads carry what the calling key may do on the board.
func TestBoardReadsCarryMyPermissions(t *testing.T) {
	for name, op := range map[string]string{"board list": AgentBoardsProgrammatic_Operation, "board show": AgentBoardProgrammatic_Operation} {
		if !strings.Contains(normalised(op), "perspectives perspectiveNames myPermissions") {
			t.Errorf("%s does not read myPermissions", name)
		}
	}
}
