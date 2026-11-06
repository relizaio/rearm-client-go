package rearm

import (
	"encoding/json"
	"strings"
	"testing"
)

// The design checkpoint waiver (task TPF-153, backend TPF-152): the architect's sign-off says --no-split, the
// server records it on the sign-off row and as the task's designCheckpointWaiver, and every task read returns it.

// noSplit goes to the server as given, and a sign-off that does not say it sends null: no waiver.
func TestSignOffSendsNoSplit(t *testing.T) {
	op := normalised(AgentTaskSignOffProgrammatic_Operation)
	if !strings.Contains(op, "$noSplit: Boolean") || !strings.Contains(op, "noSplit: $noSplit") {
		t.Fatal("the sign-off does not send noSplit")
	}
	yes := true
	for _, c := range []struct {
		noSplit *bool
		want    string
	}{{nil, `"noSplit":null`}, {&yes, `"noSplit":true`}} {
		b, err := json.Marshal(__AgentTaskSignOffProgrammaticInput{NoSplit: c.noSplit})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), c.want) {
			t.Errorf("noSplit %v marshals as %s, want %s", c.noSplit, b, c.want)
		}
	}
}

// The sign-off reads back the statement on its own sign-off rows, before noCode.
func TestSignOffReadsNoSplit(t *testing.T) {
	op := normalised(AgentTaskSignOffProgrammatic_Operation)
	if !strings.Contains(op, "noSplit noCode overAllowanceMicros noChange answered }") {
		t.Error("the sign-off does not read noSplit on its sign-offs")
	}
	var so AgentTaskSignOffProgrammaticAgentTaskSignOffProgrammaticAgentTaskSignOffsAgentTaskSignOff
	if err := json.Unmarshal([]byte(`{"noSplit":true}`), &so); err != nil {
		t.Fatal(err)
	}
	if so.GetNoSplit() == nil || !*so.GetNoSplit() {
		t.Errorf("read back %+v", so)
	}
}

// The waiver is nullable: a task without one unmarshals to nil, a full record field by field, standing either way.
func TestTheWaiverUnmarshals(t *testing.T) {
	var none AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
	if err := json.Unmarshal([]byte(`{"designCheckpointWaiver":null}`), &none); err != nil {
		t.Fatal(err)
	}
	if none.DesignCheckpointWaiver != nil {
		t.Errorf("a null waiver unmarshals to %+v, want nil", none.DesignCheckpointWaiver)
	}
	for _, standing := range []bool{true, false} {
		var task AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
		body := `{"designCheckpointWaiver":{"bySession":"s-1","byAgent":"ag-1","role":"architect","roleUuid":"r1",` +
			`"at":"2026-11-05T02:59:00Z","designRound":"a1","note":"fits one coder pass","standing":` +
			map[bool]string{true: "true", false: "false"}[standing] + `}}`
		if err := json.Unmarshal([]byte(body), &task); err != nil {
			t.Fatal(err)
		}
		w := task.DesignCheckpointWaiver
		if w == nil {
			t.Fatalf("standing %v: the waiver unmarshals to nil", standing)
		}
		if w.BySession == nil || *w.BySession != "s-1" || w.ByAgent == nil || *w.ByAgent != "ag-1" ||
			w.Role != "architect" || w.RoleUuid == nil || *w.RoleUuid != "r1" || w.At != "2026-11-05T02:59:00Z" ||
			w.DesignRound != "a1" || w.Note == nil || *w.Note != "fits one coder pass" || w.Standing != standing {
			t.Errorf("standing %v: unmarshals to %+v", standing, w)
		}
	}
}

// A blind reader's copy has bySession, byAgent and note null (TPF-152 round 5): they unmarshal to nil and the five
// others stay set. BySession being a pointer is the check that the schema file declares bySession: ID.
func TestABlindReadersWaiverUnmarshals(t *testing.T) {
	var task AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
	body := `{"designCheckpointWaiver":{"bySession":null,"byAgent":null,"role":"architect","roleUuid":"r1",` +
		`"at":"2026-11-05T02:59:00Z","designRound":"a1","note":null,"standing":true}}`
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		t.Fatal(err)
	}
	w := task.DesignCheckpointWaiver
	if w == nil {
		t.Fatal("the waiver unmarshals to nil")
	}
	var bySession, byAgent, note *string = w.BySession, w.ByAgent, w.Note
	if bySession != nil || byAgent != nil || note != nil {
		t.Errorf("the redacted fields unmarshal to %v %v %v, want nil", bySession, byAgent, note)
	}
	if w.Role != "architect" || w.RoleUuid == nil || *w.RoleUuid != "r1" || w.At != "2026-11-05T02:59:00Z" ||
		w.DesignRound != "a1" || !w.Standing {
		t.Errorf("unmarshals to %+v", w)
	}
}
