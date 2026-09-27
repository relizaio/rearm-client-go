package rearm

import (
	"strings"
	"testing"
)

// What 'rearm agent wait --coordinator' reads (RD2-32): the snapshot carries each task's hold level
// and kind, so a coordinator-level hold wakes it and a person's does not; the DELIVERING read
// carries the PRs' states, so an unmerged PR does.
func TestTheWaitReadsCarryHoldsAndPullRequestStates(t *testing.T) {
	if !strings.Contains(normalised(AgentBoardSnapshotProgrammatic_Operation), "orderIndex hold { level kind } }") {
		t.Error("the snapshot's task does not read hold { level kind }")
	}
	d := normalised(AgentWaitDeliveringProgrammatic_Operation)
	if !strings.Contains(d, "agentTasksProgrammatic(boardUuid: $boardUuid, status: DELIVERING)") ||
		!strings.Contains(d, "pullRequests { url state }") {
		t.Errorf("the DELIVERING read is not the light one: %s", d)
	}
}
