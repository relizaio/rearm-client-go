package rearm

import (
	"strings"
	"testing"
)

// The architect's design checkpoint waiver is a board switch (task TPF-153, backend TPF-152): the board reads carry
// it as set and as in effect, the export writes it, and the coordinate read, which reads no settings, does not.
func TestBoardReadsTheWaiverSwitch(t *testing.T) {
	for name, op := range map[string]string{
		"AgentBoardsProgrammatic": AgentBoardsProgrammatic_Operation,
		"AgentBoardProgrammatic":  AgentBoardProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "architectCheckpointWaiver effectiveArchitectCheckpointWaiver") {
			t.Errorf("%s does not read the waiver switch", name)
		}
	}
	export := normalised(ExportBoard_Operation)
	i := strings.Index(export, "settings {")
	if i < 0 {
		t.Fatal("the export reads no settings")
	}
	settings := export[i : i+strings.Index(export[i:], "}")]
	if !strings.Contains(settings, "architectCheckpointWaiver") {
		t.Error("the export does not write settings.architectCheckpointWaiver")
	}
	if strings.Contains(AgentBoardCoordinateProgrammatic_Operation, "architectCheckpointWaiver") {
		t.Error("the coordinate read selects the waiver switch")
	}
}
