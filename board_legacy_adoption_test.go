package rearm

import (
	"strings"
	"testing"
)

// Whether a board adopts legacy document components (task RD2-33): the board reads carry the flag, so
// `rearm agent board show` says which rule a board follows; the export does not, since it is state.
func TestBoardReadsCarryWhetherTheBoardAdoptsLegacyDocuments(t *testing.T) {
	for name, op := range map[string]string{
		"board list": AgentBoardsProgrammatic_Operation, "board show": AgentBoardProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "adoptsLegacyDocuments") {
			t.Errorf("%s does not read adoptsLegacyDocuments", name)
		}
	}
	if strings.Contains(ExportBoard_Operation, "adoptsLegacyDocuments") {
		t.Error("the export carries state")
	}
}
