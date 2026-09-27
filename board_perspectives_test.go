package rearm

import (
	"strings"
	"testing"
)

// Board perspectives (task b9115d09): board reads carry the perspectives and their names, and the
// export carries the names with the product: marker.
func TestBoardReadsCarryThePerspectives(t *testing.T) {
	for name, op := range map[string]string{"board list": AgentBoardsProgrammatic_Operation, "board show": AgentBoardProgrammatic_Operation} {
		if !strings.Contains(normalised(op), "perspectives perspectiveNames") {
			t.Errorf("%s does not read the perspectives", name)
		}
	}
	if !strings.Contains(normalised(ExportBoard_Operation), "documents { prefix shared root } perspectives") {
		t.Error("the board export does not carry the perspectives")
	}
}

// The read the Terraform provider maps configured perspectives by (task b9115d09, round 3): behind
// the export's gate, each perspective's uuid, name and product.
func TestTheExportPerspectivesReadCarriesUuidNameAndProduct(t *testing.T) {
	n := normalised(ExportBoardPerspectives_Operation)
	if !strings.Contains(n, "exportBoardPerspectivesProgrammatic(board: $board) { uuid name product }") {
		t.Errorf("the perspectives read selects %q", n)
	}
}
