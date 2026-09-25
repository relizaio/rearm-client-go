package rearm

import (
	"strings"
	"testing"
)

// Delivery modes and attestations (task 18c5c293).
func TestDeliveryOperations(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	agent := norm(AgentTaskDeliveredProgrammatic_Operation)
	for _, want := range []string{"$sessionUuid: ID!", "$unit: String!", "$commit: String", "$outcome: AgentDeliveryOutcome", "$note: String"} {
		if !strings.Contains(agent, want) {
			t.Errorf("the agent's attestation lacks %q", want)
		}
	}
	person := norm(AgentTaskDelivered_Operation)
	if strings.Contains(person, "sessionUuid") || !strings.Contains(person, "$unit: String!") {
		t.Errorf("a person's attestation carries no session and names the unit: %s", person)
	}
	// "mode attest " rather than "mode attest }": the policy goes on to the merge procedure (task 71a3dd22).
	for name, op := range map[string]string{"list": AgentBoardsProgrammatic_Operation, "show": AgentBoardProgrammatic_Operation} {
		if !strings.Contains(norm(op), "effectiveDeliveryPolicy { mode attest ") {
			t.Errorf("%s does not read the delivery policy", name)
		}
	}
	if !strings.Contains(norm(ExportBoard_Operation), "delivery { mode attest ") {
		t.Error("the export leaves the delivery policy behind")
	}
	show := norm(AgentTaskProgrammatic_Operation)
	if !strings.Contains(show, "attestation { unit commit outcome") || !strings.Contains(show, "deliveries { unit commit outcome") {
		t.Error("task show reads the attestations")
	}
	if AgentDeliveryOutcomeAbandoned != "ABANDONED" || AgentDeliveryModeAttested != "ATTESTED" {
		t.Error("the enums")
	}
}
