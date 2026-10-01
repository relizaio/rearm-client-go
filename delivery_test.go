package rearm

import (
	"strings"
	"testing"
)

// Delivery modes and declarations (task 18c5c293).
func TestDeliveryOperations(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	agent := norm(AgentTaskDeclareDeliveryProgrammatic_Operation)
	for _, want := range []string{"$sessionUuid: ID!", "$unit: String!", "$commit: String", "$outcome: AgentDeliveryOutcome", "$note: String"} {
		if !strings.Contains(agent, want) {
			t.Errorf("the agent's declaration lacks %q", want)
		}
	}
	person := norm(AgentTaskDeclareDelivery_Operation)
	if strings.Contains(person, "sessionUuid") || !strings.Contains(person, "$unit: String!") {
		t.Errorf("a person's declaration carries no session and names the unit: %s", person)
	}
	// "mode awaitDeclaration " rather than "mode awaitDeclaration }": the policy goes on to the merge procedure (task 71a3dd22).
	for name, op := range map[string]string{"list": AgentBoardsProgrammatic_Operation, "show": AgentBoardProgrammatic_Operation} {
		if !strings.Contains(norm(op), "effectiveDeliveryPolicy { mode awaitDeclaration ") {
			t.Errorf("%s does not read the delivery policy", name)
		}
	}
	if !strings.Contains(norm(ExportBoard_Operation), "delivery { mode awaitDeclaration ") {
		t.Error("the export leaves the delivery policy behind")
	}
	show := norm(AgentTaskProgrammatic_Operation)
	if !strings.Contains(show, "declaration { unit commit outcome") || !strings.Contains(show, "deliveries { unit commit outcome") {
		t.Error("task show reads the declarations")
	}
	if AgentDeliveryOutcomeAbandoned != "ABANDONED" || AgentDeliveryModeDeclared != "DECLARED" {
		t.Error("the enums")
	}
}
