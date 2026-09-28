package rearm

import (
	"strings"
	"testing"
)

// The role part of a brief (task RD3-9): each role's served prompt and its version, and what it reads and
// leaves behind, in one read.
func TestTheRoleBriefReadsTheServedPromptAndTheRolesInputs(t *testing.T) {
	op := normalised(AgentRoleBriefProgrammatic_Operation)
	for _, want := range []string{"agentTaskRoleConfigsProgrammatic(boardUuid: $boardUuid)", "servedPrompt", "promptVersion",
		"requiredInputs { kind specification scope minLifecycle resolution }", "producesOutputs { specification scope required }"} {
		if !strings.Contains(op, want) {
			t.Errorf("the role brief does not select %q", want)
		}
	}
	var r AgentRoleBriefProgrammaticAgentTaskRoleConfigsProgrammaticAgentTaskRoleConfig
	_ = r.GetServedPrompt()
	_ = r.GetPromptVersion()
}
