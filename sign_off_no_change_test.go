package rearm

import (
	"encoding/json"
	"strings"
	"testing"
)

// A sign-off can say its round changes nothing to build (task RD4-13): noChange goes to the server as given,
// and a sign-off that does not say it sends null, which the server records as nothing said.
func TestSignOffSendsNoChange(t *testing.T) {
	op := normalised(AgentTaskSignOffProgrammatic_Operation)
	if !strings.Contains(op, "$noChange: Boolean") || !strings.Contains(op, "noChange: $noChange") {
		t.Fatal("the sign-off does not send noChange")
	}
	yes := true
	for _, c := range []struct {
		noChange *bool
		want     string
	}{{nil, `"noChange":null`}, {&yes, `"noChange":true`}} {
		b, err := json.Marshal(__AgentTaskSignOffProgrammaticInput{NoChange: c.noChange})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), c.want) {
			t.Errorf("noChange %v marshals as %s, want %s", c.noChange, b, c.want)
		}
	}
}

// The sign-off reads back what it recorded: the statement, and the findings round the hop answered.
func TestSignOffReadsNoChangeAndAnswered(t *testing.T) {
	op := normalised(AgentTaskSignOffProgrammatic_Operation)
	if !strings.Contains(op, "overAllowanceMicros noChange answered }") {
		t.Error("the sign-off does not read noChange and answered on its sign-offs")
	}
	var so AgentTaskSignOffProgrammaticAgentTaskSignOffProgrammaticAgentTaskSignOffsAgentTaskSignOff
	if err := json.Unmarshal([]byte(`{"noChange":true,"answered":"r1"}`), &so); err != nil {
		t.Fatal(err)
	}
	if so.GetNoChange() == nil || !*so.GetNoChange() || so.GetAnswered() == nil || *so.GetAnswered() != "r1" {
		t.Errorf("read back %+v", so)
	}
}
