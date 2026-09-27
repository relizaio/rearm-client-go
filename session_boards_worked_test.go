package rearm

import (
	"strings"
	"testing"
)

// The boards a session worked (RD2-5): the session read carries them, so a caller can tell who may
// force-close it (BOARD_WRITE on one of them) without another round trip.
func TestSessionReadCarriesTheBoardsItWorked(t *testing.T) {
	if !strings.Contains(normalised(SessionProgrammatic_Operation), "artifacts boardsWorked commits") {
		t.Error("session show does not read boardsWorked")
	}
}
