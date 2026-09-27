package rearm

import (
	"strings"
	"testing"
)

// The boards a session worked (RD2-5): the session read carries them, so a caller can tell who may
// force-close it (BOARD_WRITE on one of them) without another round trip.
func TestSessionReadCarriesTheBoardsItWorked(t *testing.T) {
	if !strings.Contains(normalised(SessionProgrammatic_Operation), "artifacts boardsWorked ") {
		t.Error("session show does not read boardsWorked")
	}
}

// The tasks a session worked (RD2-11), each with its role and board name: the session page lists them
// linked, and names the boards from them without reading each board.
func TestSessionReadCarriesTheTasksItWorked(t *testing.T) {
	if !strings.Contains(normalised(SessionProgrammatic_Operation),
		"boardsWorked tasksWorked { uuid key title role board boardName } commits") {
		t.Error("session show does not read tasksWorked with role and board name")
	}
}
