package rearm

import (
	"strings"
	"testing"
)

// The board's event log (task 1c5442d2): read since a point, with the cursor to read on from.
func TestTheEventLogRead(t *testing.T) {
	op := strings.Join(strings.Fields(AgentBoardEventsProgrammatic_Operation), " ")
	for _, want := range []string{"$after: Long", "$since: DateTime", "$limit: Int", "seq", "nextAfter", "hasMore"} {
		if !strings.Contains(op, want) {
			t.Errorf("the event log read lacks %q", want)
		}
	}
}
