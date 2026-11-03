package rearm

import (
	"encoding/json"
	"strings"
	"testing"
)

// The split request (task SPLIT-12, parent SPLIT-9 T-16): every task selection that reads the design checkpoint
// reads the split request too, so whatever verb printed the task can name it. A selection dropped from one
// operation fails by that operation's name.
func TestTaskSelectionsReadTheSplitRequest(t *testing.T) {
	// genqlient writes the spread as "... ActorFields" in the generated text.
	const want = "splitRequest { requestedAt role roleUuid session description proposedChildren { title description" +
		" externalRef sourceUrl dependsOnSiblingIndexes producesComponent workLevel group tags { key value } }" +
		" state settledBy { ... ActorFields } settledAt note }"
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
		t.Fatalf("%d operations listed, want the 26 that select the design checkpoint", len(ops))
	}
	for name, op := range ops {
		if !strings.Contains(normalised(op), want) {
			t.Errorf("%s does not select the split request", name)
		}
	}
}

// A return proposes children; an authorize carries the coordinator's note.
func TestTheSplitRequestArguments(t *testing.T) {
	for name, c := range map[string]struct{ op, decl, pass string }{
		"AgentTaskReturnProgrammatic":    {AgentTaskReturnProgrammatic_Operation, "$proposedChildren: [AgentTaskSplitChildInput!]", "proposedChildren: $proposedChildren"},
		"AgentTaskAuthorizeProgrammatic": {AgentTaskAuthorizeProgrammatic_Operation, "$note: String", "note: $note"},
	} {
		op := normalised(c.op)
		if !strings.Contains(op, c.decl) || !strings.Contains(op, c.pass) {
			t.Errorf("%s does not declare and pass %q", name, c.decl)
		}
	}
}

// A typed caller passing nil omits both arguments: the server refuses a proposal with any reason but SPLIT.
func TestTheSplitRequestArgumentsAreOmittedWhenNil(t *testing.T) {
	ret, err := json.Marshal(__AgentTaskReturnProgrammaticInput{TaskUuid: "t", SessionUuid: "s", Reason: AgentTaskReturnReasonOther})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ret), "proposedChildren") {
		t.Errorf("a return without children sends %s", ret)
	}
	auth, err := json.Marshal(__AgentTaskAuthorizeProgrammaticInput{TaskUuid: "t", SessionUuid: "s", Role: "coder"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(auth), `"note"`) {
		t.Errorf("an authorize without a note sends %s", auth)
	}
}

func TestTheSplitRequestEnums(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{string(AgentTaskReturnReasonSplit), "SPLIT"},
		{string(AgentTaskSplitRequestStateRequested), "REQUESTED"},
		{string(AgentTaskSplitRequestStateSplit), "SPLIT"},
		{string(AgentTaskSplitRequestStateDeclined), "DECLINED"},
		{string(AgentTaskSplitRequestStateLapsed), "LAPSED"},
	} {
		if c.got != c.want {
			t.Errorf("constant reads %q, want %q", c.got, c.want)
		}
	}
}

// The record is nullable: a task never returned with reason SPLIT unmarshals to nil, a full record to the
// generated type, children with and without group and tags.
func TestTheSplitRequestUnmarshals(t *testing.T) {
	var none AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
	if err := json.Unmarshal([]byte(`{"splitRequest":null}`), &none); err != nil {
		t.Fatal(err)
	}
	if none.SplitRequest != nil {
		t.Errorf("a null record unmarshals to %+v, want nil", none.SplitRequest)
	}
	var task AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
	body := `{"splitRequest":{"requestedAt":"2026-11-03T06:00:00Z","role":"coder","roleUuid":"r-1","session":"s-1",` +
		`"description":"two parts","proposedChildren":[` +
		`{"title":"part one","description":"d","externalRef":"X-1","sourceUrl":"https://example.test/1",` +
		`"dependsOnSiblingIndexes":[],"producesComponent":"c-1","workLevel":2,"group":"g-1","tags":[{"key":"k","value":"v"}]},` +
		`{"title":"part two","description":null,"externalRef":null,"sourceUrl":null,"dependsOnSiblingIndexes":[0],` +
		`"producesComponent":null,"workLevel":null,"group":null,"tags":null}],` +
		`"state":"REQUESTED","settledBy":null,"settledAt":null,"note":null}}`
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		t.Fatal(err)
	}
	r := task.SplitRequest
	if r == nil {
		t.Fatal("the record unmarshals to nil")
	}
	if r.State != AgentTaskSplitRequestStateRequested || r.Role != "coder" || r.Description == nil ||
		*r.Description != "two parts" || r.SettledBy != nil || r.Note != nil || len(r.ProposedChildren) != 2 {
		t.Fatalf("unmarshals to %+v", r)
	}
	one, two := r.ProposedChildren[0], r.ProposedChildren[1]
	if one.Title != "part one" || one.Group == nil || *one.Group != "g-1" || len(one.Tags) != 1 ||
		one.Tags[0].GetKey() == nil || *one.Tags[0].GetKey() != "k" ||
		one.Tags[0].GetValue() == nil || *one.Tags[0].GetValue() != "v" || one.WorkLevel == nil || *one.WorkLevel != 2 ||
		one.ProducesComponent == nil || *one.ProducesComponent != "c-1" {
		t.Errorf("the first child unmarshals to %+v", one)
	}
	if two.Title != "part two" || two.Group != nil || two.Tags != nil || len(two.DependsOnSiblingIndexes) != 1 ||
		two.DependsOnSiblingIndexes[0] != 0 || two.WorkLevel != nil {
		t.Errorf("the second child unmarshals to %+v", two)
	}
}
