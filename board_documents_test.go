package rearm

import (
	"strings"
	"testing"
)

// A board's document components (task e2ae9cfa): the board reads carry the documents block and the
// specification-to-component map, the export carries the documents block, and DOCUMENT is a kind.
// The block has shared and root, and the reads carry the root resolved (task 0cc38817).
func TestBoardReadsCarryTheDocumentComponentsAndTheExportTheDocumentsBlock(t *testing.T) {
	for name, op := range map[string]string{
		"board list": AgentBoardsProgrammatic_Operation, "board show": AgentBoardProgrammatic_Operation,
	} {
		n := normalised(op)
		if !strings.Contains(n, "documents { prefix shared root }") {
			t.Errorf("%s does not read the documents block", name)
		}
		if !strings.Contains(n, "documentsRoot") {
			t.Errorf("%s does not read the resolved documents root", name)
		}
		if !strings.Contains(n, "documentComponents { specification component }") {
			t.Errorf("%s does not read the document components", name)
		}
	}
	if !strings.Contains(normalised(ExportBoard_Operation), "documents { prefix shared root }") {
		t.Error("board export does not carry the documents block")
	}
	if ComponentKindDocument != "DOCUMENT" {
		t.Errorf("ComponentKindDocument is %q", ComponentKindDocument)
	}
}
