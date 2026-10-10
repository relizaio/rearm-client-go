package rearm

import (
	"slices"
	"strings"
	"testing"
)

// A linked PR unlinked from its task, read back with what remains and what was unlinked; and the delivery unit
// of each linked PR (task t20261010-033523-18839, t20261010-033522-23606).
func TestUnlinkOperation(t *testing.T) {
	op := normalised(AgentTaskUnlinkPrProgrammatic_Operation)
	for _, want := range []string{"$taskUuid: ID!", "$sessionUuid: ID!", "$prUrl: String!", "$note: String",
		"agentTaskUnlinkPrProgrammatic(taskUuid: $taskUuid, sessionUuid: $sessionUuid, prUrl: $prUrl, note: $note)"} {
		if !strings.Contains(op, want) {
			t.Errorf("the unlink lacks %q", want)
		}
	}
	for _, want := range []string{"registered head unit }", "unlinkedPrs { url by { ... ActorFields } at note relinkedBy relinkedAt }"} {
		if !strings.Contains(op, want) {
			t.Errorf("the unlink does not read back %q", want)
		}
	}
	for name, read := range map[string]string{
		"show": AgentTaskProgrammatic_Operation, "by uuid": AgentTasksByUuidProgrammatic_Operation,
		"signoff": AgentTaskSignOffProgrammatic_Operation, "complete": AgentTaskCompleteProgrammatic_Operation,
		"reopen": AgentTaskReopenProgrammatic_Operation, "linkpr": AgentTaskLinkPrProgrammatic_Operation} {
		if !strings.Contains(normalised(read), "registered head unit") {
			t.Errorf("%s does not read the PR's delivery unit", name)
		}
	}
	if !strings.Contains(normalised(AgentTaskProgrammatic_Operation), "unlinkedPrs { url by { ... ActorFields } at note relinkedBy relinkedAt }") {
		t.Error("task show does not read the unlinked PRs (tasks by uuid follows it: tasks_by_uuid_test.go)")
	}
	for _, u := range []AgentDeliveryUnit{AgentDeliveryUnitDelivered, AgentDeliveryUnitWaiting, AgentDeliveryUnitBlocked, AgentDeliveryUnitSuperseded} {
		if !slices.Contains(AllAgentDeliveryUnit, u) {
			t.Errorf("the enum lacks %s", u)
		}
	}
	var pr AgentTaskProgrammaticAgentTaskProgrammaticAgentTaskPullRequestsAgentTaskPullRequest
	if pr.GetUnit() != nil {
		t.Error("a PR with no unit reads nil")
	}
}
