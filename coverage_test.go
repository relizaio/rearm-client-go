package rearm

import (
	"strings"
	"testing"
)

// A board's coverage gates are part of its check policy: read where the policy is read, and carried
// by the exported file (elements.md §7.5).
func TestThePolicyReadsAndTheExportCarryTheCoverageGates(t *testing.T) {
	for name, op := range map[string]string{
		"list": AgentBoardsProgrammatic_Operation, "show": AgentBoardProgrammatic_Operation, "export": ExportBoard_Operation,
	} {
		if !strings.Contains(op, "coverage") {
			t.Errorf("%s does not select the coverage gates", name)
		}
	}
}

func TestTheCatalogueIsReadable(t *testing.T) {
	for _, want := range []string{"checkCatalogue", "name", "description", "skipsWhen"} {
		if !strings.Contains(CheckCatalogue_Operation, want) {
			t.Errorf("the catalogue read lacks %q", want)
		}
	}
}
