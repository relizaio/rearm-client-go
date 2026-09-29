package rearm

import (
	"encoding/json"
	"strings"
	"testing"
)

// A session parks its own hop for the operator (task RD4-5): the hold takes a level, sent as given, and a hold
// that does not name one sends null, which the server reads as the seat's COORDINATOR level.
func TestHoldSendsTheLevel(t *testing.T) {
	op := normalised(AgentTaskHoldProgrammatic_Operation)
	if !strings.Contains(op, "$level: AgentTaskHoldLevel") || !strings.Contains(op, "level: $level") {
		t.Fatal("the hold does not send the level")
	}
	operator := AgentTaskHoldLevelOperator
	for _, c := range []struct {
		level *AgentTaskHoldLevel
		want  string
	}{{nil, `"level":null`}, {&operator, `"level":"OPERATOR"`}} {
		b, err := json.Marshal(__AgentTaskHoldProgrammaticInput{Reason: "which secret model?", Level: c.level})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), c.want) {
			t.Errorf("level %v marshals as %s, want %s", c.level, b, c.want)
		}
	}
}

// A withdrawal is the cancel with the reason as its note: the operation already takes one.
func TestCancelCarriesTheNoteAWithdrawalNeeds(t *testing.T) {
	op := normalised(AgentTaskCancelProgrammatic_Operation)
	if !strings.Contains(op, "$note: String") || !strings.Contains(op, "note: $note") {
		t.Fatal("the cancel does not send a note")
	}
	if !strings.Contains(op, "statusHistory {") || !strings.Contains(op[strings.Index(op, "statusHistory {"):], "note") {
		t.Error("the cancel does not read back the row that records the withdrawal")
	}
}
