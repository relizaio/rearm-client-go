package rearm

import (
	"encoding/json"
	"strings"
	"testing"
)

// A sign-off can say its round changed no code (task RD4-2): noCode goes to the server as given, and a sign-off
// that does not say it sends null, which the server records as nothing said.
func TestSignOffSendsNoCode(t *testing.T) {
	op := normalised(AgentTaskSignOffProgrammatic_Operation)
	if !strings.Contains(op, "$noCode: Boolean") || !strings.Contains(op, "noCode: $noCode") {
		t.Fatal("the sign-off does not send noCode")
	}
	yes := true
	for _, c := range []struct {
		noCode *bool
		want   string
	}{{nil, `"noCode":null`}, {&yes, `"noCode":true`}} {
		b, err := json.Marshal(__AgentTaskSignOffProgrammaticInput{NoCode: c.noCode})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), c.want) {
			t.Errorf("noCode %v marshals as %s, want %s", c.noCode, b, c.want)
		}
	}
	if !strings.Contains(op, "noCode overAllowanceMicros noChange answered }") {
		t.Error("the sign-off does not read noCode back on its sign-offs")
	}
}

// Every task read that lists the PRs reads how far each PR's base moved since the round (task RD4-2), and
// null (unknown) stays apart from zero.
func TestTaskReadsSelectBaseMovedBy(t *testing.T) {
	for name, op := range map[string]string{
		"signoff":  AgentTaskSignOffProgrammatic_Operation,
		"show":     AgentTaskProgrammatic_Operation,
		"byUuid":   AgentTasksByUuidProgrammatic_Operation,
		"linkpr":   AgentTaskLinkPrProgrammatic_Operation,
		"complete": AgentTaskCompleteProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "mergedDate baseMovedBy registered head") {
			t.Errorf("%s does not select baseMovedBy on its PRs", name)
		}
	}
	var unknown, zero, moved AgentTaskProgrammaticAgentTaskProgrammaticAgentTaskPullRequestsAgentTaskPullRequest
	for _, c := range []struct {
		raw string
		v   *AgentTaskProgrammaticAgentTaskProgrammaticAgentTaskPullRequestsAgentTaskPullRequest
	}{{`{"baseMovedBy":null}`, &unknown}, {`{"baseMovedBy":0}`, &zero}, {`{"baseMovedBy":3}`, &moved}} {
		if err := json.Unmarshal([]byte(c.raw), c.v); err != nil {
			t.Fatal(err)
		}
	}
	if unknown.GetBaseMovedBy() != nil || zero.GetBaseMovedBy() == nil || *zero.GetBaseMovedBy() != 0 ||
		moved.GetBaseMovedBy() == nil || *moved.GetBaseMovedBy() != 3 {
		t.Errorf("read back %v %v %v", unknown.GetBaseMovedBy(), zero.GetBaseMovedBy(), moved.GetBaseMovedBy())
	}
}
