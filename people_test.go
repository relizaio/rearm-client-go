package rearm

import (
	"strings"
	"testing"
)

// A person's operations carry no session: people have none, and the server acts as the owner of
// the personal key or login that sent them. A session variable here would be the agent's twin by
// mistake.
func TestPeopleOperationsCarryNoSession(t *testing.T) {
	ops := map[string]string{
		"boards": AgentBoardsOfOrg_Operation, "tasks": AgentTasksOfBoard_Operation,
		"register": AgentTaskRegister_Operation, "authorize": AgentTaskAuthorize_Operation,
		"order": AgentTaskOrder_Operation, "complete": AgentTaskComplete_Operation,
		"cancel": AgentTaskCancel_Operation, "decide": AgentTaskDecideFindings_Operation,
		"answer": AgentTaskAnswer_Operation, "review": AgentTaskHumanReview_Operation,
		"signoff": AgentTaskHumanSignOff_Operation, "hold": AgentTaskOperatorHold_Operation,
		"requireReview": AgentTaskRequireHumanReview_Operation, "strength": AgentTaskSetStrength_Operation,
		"lock": AgentBoardOperatorLock_Operation,
	}
	for name, op := range ops {
		if strings.Contains(op, "sessionUuid") {
			t.Errorf("%s sends a session", name)
		}
		if strings.Contains(op, "Programmatic(") {
			t.Errorf("%s calls an agent's operation", name)
		}
	}
}

// Every task a person acts on comes back with what they need to see the result: status, hold,
// what is still open, and who moved it.
func TestPeopleTaskResultsSelectTheOutcome(t *testing.T) {
	for _, want := range []string{"status", "hold {", "openFindings {", "openQuestions {", "statusHistory {", "actor {"} {
		if !strings.Contains(AgentTaskComplete_Operation, want) {
			t.Errorf("person task result lacks %q", want)
		}
	}
}

// A person's hold release names the role to route to, as the coordinator's does (task 4c566d0d);
// the operator hold moved to this lane, and the role must come with it.
func TestPersonHoldReleaseSendsTheRole(t *testing.T) {
	for _, want := range []string{"$role: String", "role: $role"} {
		if !strings.Contains(AgentTaskOperatorHold_Operation, want) {
			t.Errorf("person hold operation lacks %q", want)
		}
	}
}
