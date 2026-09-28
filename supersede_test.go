package rearm

import (
	"strings"
	"testing"
)

// A linked PR declared superseded by its replacement (task RD3-13).
func TestSupersedeOperation(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	op := norm(AgentTaskSupersedePullRequestProgrammatic_Operation)
	for _, want := range []string{"$taskUuid: ID!", "$sessionUuid: ID!", "$oldUrl: String!", "$byUrl: String!", "$note: String",
		"agentTaskSupersedePullRequestProgrammatic(taskUuid: $taskUuid, sessionUuid: $sessionUuid, oldUrl: $oldUrl, byUrl: $byUrl, note: $note)"} {
		if !strings.Contains(op, want) {
			t.Errorf("the declaration lacks %q", want)
		}
	}
	show := norm(AgentTaskProgrammatic_Operation)
	if strings.Count(show, "at note supersededBy }") != 2 {
		t.Error("task show does not read what supersedes a PR")
	}
	if AgentDeliveryOutcomeSuperseded != "SUPERSEDED" {
		t.Error("the enum")
	}
	var d AgentTaskProgrammaticAgentTaskProgrammaticAgentTaskDeliveriesAgentTaskDelivery
	if d.GetSupersededBy() != nil {
		t.Error("a delivery with no successor reads nil")
	}
}
