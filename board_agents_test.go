package rearm

import (
	"strings"
	"testing"
)

// The Agents tab's read (task RD3-5).
func TestBoardAgentsOperation(t *testing.T) {
	op := strings.Join(strings.Fields(AgentBoardAgentsProgrammatic_Operation), " ")
	for _, want := range []string{"$boardUuid: ID!", "$from: DateTime", "$to: DateTime", "agents(from: $from, to: $to)",
		"agentName roles lastPollAt lastOfferAt lastActivityAt tasksCompleted costMicros cacheShare",
		"state { kind taskUuid taskKey since closedBy", "stale { rule message }", "cacheReadTokens cacheWriteTokens"} {
		if !strings.Contains(op, want) {
			t.Errorf("the agents read lacks %q", want)
		}
	}
	if BoardAgentStateKindWorking != "WORKING" || BoardAgentStateKindClosed != "CLOSED" {
		t.Error("the state kinds")
	}
}
