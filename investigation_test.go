package rearm

import (
	"encoding/json"
	"strings"
	"testing"
)

// Investigation tasks (task RD4-12): the commission mutation, the task reads that carry the kind, the
// investigation block, the returned reports and the pinned inputs, and the role reads and the export that carry a
// role's commissions.
func TestTheCommissionOperationSendsItsInputAndReadsTheInvestigation(t *testing.T) {
	op := normalised(AgentTaskCommissionProgrammatic_Operation)
	for _, want := range []string{
		"mutation AgentTaskCommissionProgrammatic ($input: AgentTaskCommissionInput!)",
		"agentTaskCommissionProgrammatic(input: $input)",
		"kind investigation { commissionedBy { role roleUuid session task by {",
		"deliverable role roleUuid review reviewUuid deadline returnTo report completedAt }",
		"requiredInputs { kind specification scope minLifecycle resolution release }",
	} {
		if !strings.Contains(op, want) {
			t.Errorf("the commission operation lacks %q", want)
		}
	}
	b, err := json.Marshal(AgentTaskCommissionInput{BoardUuid: "b1", Role: "tester", Title: "measure it"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"boardUuid":"b1"`, `"role":"tester"`, `"title":"measure it"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("the input marshals without %s: %s", key, b)
		}
	}
}

func TestTheTaskReadsCarryTheInvestigationAndThePins(t *testing.T) {
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "tasks by uuid": AgentTasksByUuidProgrammatic_Operation,
		"task list": AgentTasksProgrammatic_Operation,
	} {
		n := normalised(op)
		if !strings.Contains(n, "kind investigation { commissionedBy {") {
			t.Errorf("%s does not read the investigation", name)
		}
		if !strings.Contains(n, "reportsReturned { investigation investigationKey report session role at reoffered }") {
			t.Errorf("%s does not read the returned reports", name)
		}
	}
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "tasks by uuid": AgentTasksByUuidProgrammatic_Operation,
	} {
		n := normalised(op)
		if !strings.Contains(n, "requiredInputs { kind specification scope minLifecycle resolution release }") {
			t.Errorf("%s does not read the task's pinned inputs", name)
		}
		if !strings.Contains(n, "resolvedInputs { kind specification release version lifecycle }") {
			t.Errorf("%s does not read what the running hop is bound to", name)
		}
	}
}

func TestRoleReadsAndTheExportCarryCommissions(t *testing.T) {
	block := "commissions { roles intake defaultBudgetMicros review }"
	for name, op := range map[string]string{
		"role configs": AgentTaskRoleConfigsProgrammatic_Operation, "board export": ExportBoard_Operation,
		"presets export": ExportRolePresets_Operation,
	} {
		if !strings.Contains(normalised(op), block) {
			t.Errorf("%s does not read a role's commissions", name)
		}
	}
}
