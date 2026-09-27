package rearm

import (
	"strings"
	"testing"
)

// What no key can do on a board (task 5c70990d): the single-board read carries it; the list does not,
// since it reads the org's keys for each board.
func TestTheBoardReadCarriesMissingCoverage(t *testing.T) {
	if !strings.Contains(normalised(AgentBoardProgrammatic_Operation), "missingCapabilities missingCoverage { function message }") {
		t.Error("the board read does not carry missingCoverage")
	}
	if strings.Contains(normalised(AgentBoardsProgrammatic_Operation), "missingCoverage") {
		t.Error("the board list reads missingCoverage: a key scan per board")
	}
}
