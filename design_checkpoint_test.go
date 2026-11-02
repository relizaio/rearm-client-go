package rearm

import (
	"encoding/json"
	"strings"
	"testing"
)

// The design checkpoint (task SPLIT-7, parent SPLIT-5 T-16): the role reads select the flag beside blindReview.
func TestRoleReadsSelectCoordinatorCheckpoint(t *testing.T) {
	for name, op := range map[string]string{
		"AgentTaskRoleConfigsProgrammatic": AgentTaskRoleConfigsProgrammatic_Operation,
		"ExportBoard":                      ExportBoard_Operation,
		"ExportRolePresets":                ExportRolePresets_Operation,
	} {
		if !strings.Contains(normalised(op), "blindReview coordinatorCheckpoint") {
			t.Errorf("%s does not select coordinatorCheckpoint beside blindReview", name)
		}
	}
}

// Every task selection that reads returns { ... } reads the record too, so whatever verb printed the task can name
// the checkpoint. A selection dropped from one operation fails by that operation's name.
func TestTaskSelectionsReadTheDesignCheckpoint(t *testing.T) {
	// genqlient writes the spread as "... ActorFields" in the generated text.
	const want = "designCheckpoint { reachedAt afterRole beforeRole design state settledBy { ... ActorFields } settledAt note }"
	ops := map[string]string{
		"AgentTaskRegisterProgrammatic":             AgentTaskRegisterProgrammatic_Operation,
		"AgentTaskNextProgrammatic":                 AgentTaskNextProgrammatic_Operation,
		"AgentTaskNextByPromptVersion":              AgentTaskNextByPromptVersion_Operation,
		"AgentTaskAssignProgrammatic":               AgentTaskAssignProgrammatic_Operation,
		"AgentTaskAssignByPromptVersion":            AgentTaskAssignByPromptVersion_Operation,
		"AgentTaskSignOffProgrammatic":              AgentTaskSignOffProgrammatic_Operation,
		"AgentTaskReturnProgrammatic":               AgentTaskReturnProgrammatic_Operation,
		"AgentTaskAuthorizeProgrammatic":            AgentTaskAuthorizeProgrammatic_Operation,
		"AgentTaskHoldProgrammatic":                 AgentTaskHoldProgrammatic_Operation,
		"AgentTaskDeclareDeliveryProgrammatic":      AgentTaskDeclareDeliveryProgrammatic_Operation,
		"AgentTaskSupersedePullRequestProgrammatic": AgentTaskSupersedePullRequestProgrammatic_Operation,
		"AgentTaskUnlinkPrProgrammatic":             AgentTaskUnlinkPrProgrammatic_Operation,
		"AgentTaskEscalateHoldProgrammatic":         AgentTaskEscalateHoldProgrammatic_Operation,
		"AgentTaskLiftHoldProgrammatic":             AgentTaskLiftHoldProgrammatic_Operation,
		"AgentTaskRequireHumanReviewProgrammatic":   AgentTaskRequireHumanReviewProgrammatic_Operation,
		"AgentTaskOrderProgrammatic":                AgentTaskOrderProgrammatic_Operation,
		"AgentTaskSetWorkLevelProgrammatic":         AgentTaskSetWorkLevelProgrammatic_Operation,
		"AgentTaskSplitProgrammatic":                AgentTaskSplitProgrammatic_Operation,
		"AgentTaskCompleteProgrammatic":             AgentTaskCompleteProgrammatic_Operation,
		"AgentTaskCancelProgrammatic":               AgentTaskCancelProgrammatic_Operation,
		"AgentTaskReopenProgrammatic":               AgentTaskReopenProgrammatic_Operation,
		"AgentTaskBindExternalRefProgrammatic":      AgentTaskBindExternalRefProgrammatic_Operation,
		"AgentTaskLinkPrProgrammatic":               AgentTaskLinkPrProgrammatic_Operation,
		"AgentTaskProgrammatic":                     AgentTaskProgrammatic_Operation,
		"AgentTasksByUuidProgrammatic":              AgentTasksByUuidProgrammatic_Operation,
		"AgentTasksProgrammatic":                    AgentTasksProgrammatic_Operation,
	}
	if len(ops) != 26 {
		t.Fatalf("%d operations listed, want the 26 that select returns", len(ops))
	}
	for name, op := range ops {
		if !strings.Contains(normalised(op), want) {
			t.Errorf("%s does not select the design checkpoint", name)
		}
	}
}

// The record is nullable: a task that never reached a checkpoint unmarshals to nil, a full record to the generated type.
func TestTheDesignCheckpointUnmarshals(t *testing.T) {
	var none AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
	if err := json.Unmarshal([]byte(`{"designCheckpoint":null}`), &none); err != nil {
		t.Fatal(err)
	}
	if none.DesignCheckpoint != nil {
		t.Errorf("a null record unmarshals to %+v, want nil", none.DesignCheckpoint)
	}
	for state, want := range map[string]AgentTaskDesignCheckpointState{
		"PENDING": AgentTaskDesignCheckpointStatePending,
		"SENT_ON": AgentTaskDesignCheckpointStateSentOn,
		"SPLIT":   AgentTaskDesignCheckpointStateSplit,
	} {
		var task AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
		body := `{"designCheckpoint":{"reachedAt":"2026-10-30T06:00:00Z","afterRole":"reviewer","beforeRole":"coder",` +
			`"design":["r1"],"state":"` + state + `","settledBy":{"kind":"SESSION","uuid":"s-1","name":"seat"},` +
			`"settledAt":"2026-10-30T06:05:00Z","note":"authorized for coder"}}`
		if err := json.Unmarshal([]byte(body), &task); err != nil {
			t.Fatal(err)
		}
		c := task.DesignCheckpoint
		if c == nil {
			t.Fatalf("%s: the record unmarshals to nil", state)
		}
		if c.State != want || c.BeforeRole != "coder" || c.AfterRole == nil || *c.AfterRole != "reviewer" ||
			len(c.Design) != 1 || c.Design[0] != "r1" || c.Note == nil || *c.Note != "authorized for coder" {
			t.Errorf("%s: unmarshals to %+v", state, c)
		}
		if c.SettledBy == nil || c.SettledBy.GetUuid() == nil || *c.SettledBy.GetUuid() != "s-1" ||
			c.SettledBy.GetKind() == nil || *c.SettledBy.GetKind() != AgentActorKindSession {
			t.Errorf("%s: settledBy unmarshals to %+v", state, c.SettledBy)
		}
	}
}
