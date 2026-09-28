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

// RD3-10: the brief reads the role's capabilities, to include the commit-trailers section of the
// orientation for a role that pushes code.
func TestRoleBriefReadsCapabilities(t *testing.T) {
	if !strings.Contains(normalised(AgentRoleBriefProgrammatic_Operation), "servedPrompt promptVersion requiredCapabilities") {
		t.Error("the role brief does not read requiredCapabilities")
	}
}
