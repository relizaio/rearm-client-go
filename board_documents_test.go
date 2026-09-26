package rearm

import (
	"strings"
	"testing"
)

// A board's document components (task e2ae9cfa): the board reads carry the documents block and the
// specification-to-component map, the export carries the documents block, and DOCUMENT is a kind.
func TestBoardReadsCarryTheDocumentComponentsAndTheExportTheDocumentsBlock(t *testing.T) {
	for name, op := range map[string]string{
		"board list": AgentBoardsProgrammatic_Operation, "board show": AgentBoardProgrammatic_Operation,
	} {
		n := normalised(op)
		if !strings.Contains(n, "documents { prefix }") {
			t.Errorf("%s does not read the documents block", name)
		}
		if !strings.Contains(n, "documentComponents { specification component }") {
			t.Errorf("%s does not read the document components", name)
		}
	}
	if !strings.Contains(normalised(ExportBoard_Operation), "documents { prefix }") {
		t.Error("board export does not carry the documents block")
	}
	if ComponentKindDocument != "DOCUMENT" {
		t.Errorf("ComponentKindDocument is %q", ComponentKindDocument)
	}
}
