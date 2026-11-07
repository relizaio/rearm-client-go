package rearm

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TPF-163 7.3 T-1: every task read selects the carried design (backend TPF-162) with the server-resolved live and
// standing, and every proposed child its designFromParent, so the verb that printed the task can say what the
// child builds from. A dropped selection fails by the operation's name.
func TestTaskSelectionsReadTheCarriedDesign(t *testing.T) {
	// genqlient writes the spread as "... ActorFields" in the generated text.
	const carried = "carriedDesign { parentTask parentKey rounds { specification release round path }" +
		" roles { role roleUuid signedOffAt session reviewedBy { ... ActorFields } decidedOver live }" +
		" at by { ... ActorFields } standing }"
	const proposed = "producesComponent workLevel group tags { key value } designFromParent }"
	ops := taskReadOperations()
	if len(ops) != 26 {
		t.Fatalf("%d operations listed, want the 26 task reads", len(ops))
	}
	for name, op := range ops {
		n := normalised(op)
		if !strings.Contains(n, carried) {
			t.Errorf("%s does not select the carried design", name)
		}
		if !strings.Contains(n, proposed) {
			t.Errorf("%s does not select designFromParent on the proposed children", name)
		}
	}
}

// TPF-163 7.3 T-1: the generated task types carry the record with its server-resolved fields; a task that is not
// a carried child unmarshals to nil.
func TestTheCarriedDesignUnmarshals(t *testing.T) {
	var none AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
	if err := json.Unmarshal([]byte(`{"carriedDesign":null}`), &none); err != nil {
		t.Fatal(err)
	}
	if none.CarriedDesign != nil {
		t.Errorf("a null record unmarshals to %+v, want nil", none.CarriedDesign)
	}
	body := `{"carriedDesign":{"parentTask":"p-1","parentKey":"TPF-12",` +
		`"rounds":[{"specification":"ARCHITECTURE","release":"r-1","round":1,"path":"design/TPF-12/architecture-1.md"}],` +
		`"roles":[{"role":"architect","roleUuid":"ro-1","signedOffAt":"2026-11-06T17:38:00Z","session":"s-1",` +
		`"reviewedBy":null,"decidedOver":false,"live":true},` +
		`{"role":"functional_reviewer","roleUuid":"ro-2","signedOffAt":"2026-11-06T17:47:00Z","session":null,` +
		`"reviewedBy":{"kind":"USER","uuid":"u-1","name":"Pat"},"decidedOver":true,"live":false}],` +
		`"at":"2026-11-06T18:00:00Z","by":{"kind":"SESSION","uuid":"seat","name":null},"standing":false},` +
		`"splitRequest":{"proposedChildren":[{"title":"a","designFromParent":true},{"title":"b"}]}}`
	var task AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		t.Fatal(err)
	}
	c := task.CarriedDesign
	if c == nil {
		t.Fatal("the record unmarshals to nil")
	}
	if c.GetParentKey() != "TPF-12" || c.GetStanding() || len(c.GetRounds()) != 1 || len(c.GetRoles()) != 2 {
		t.Errorf("unmarshals to %+v", c)
	}
	r := c.GetRounds()[0]
	if r.GetSpecification() != SpecificationTypeArchitecture || r.GetRelease() != "r-1" || r.GetRound() == nil ||
		*r.GetRound() != 1 || r.GetPath() == nil {
		t.Errorf("round unmarshals to %+v", r)
	}
	arch, fr := c.GetRoles()[0], c.GetRoles()[1]
	if !arch.GetLive() || arch.GetDecidedOver() || arch.GetSignedOffAt() == nil || arch.GetSession() == nil ||
		arch.GetReviewedBy() != nil {
		t.Errorf("architect unmarshals to %+v", arch)
	}
	if fr.GetLive() || !fr.GetDecidedOver() || fr.GetSession() != nil || fr.GetReviewedBy() == nil ||
		fr.GetReviewedBy().GetName() == nil || *fr.GetReviewedBy().GetName() != "Pat" {
		t.Errorf("functional_reviewer unmarshals to %+v", fr)
	}
	kids := task.SplitRequest.GetProposedChildren()
	if len(kids) != 2 || kids[0].GetDesignFromParent() == nil || !*kids[0].GetDesignFromParent() ||
		kids[1].GetDesignFromParent() != nil {
		t.Errorf("proposed children unmarshal to %+v", kids)
	}
}

// An unflagged typed child omits designFromParent, so a typed split or return that never sets it sends what it
// sent before the field existed; a flagged one sends true.
func TestDesignFromParentIsOmittedWhenNil(t *testing.T) {
	plain, err := json.Marshal(AgentTaskSplitChildInput{Title: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "designFromParent") {
		t.Errorf("an unflagged child sends %s", plain)
	}
	yes := true
	flagged, err := json.Marshal(AgentTaskSplitChildInput{Title: "a", DesignFromParent: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(flagged), `"designFromParent":true`) {
		t.Errorf("a flagged child sends %s", flagged)
	}
}

// TPF-163 7.3 T-2: the committed schema is the server's bundle, refreshed whole, so it carries the new input
// field and types exactly as the server declares them.
func TestTheSchemaCarriesTheCarriedDesign(t *testing.T) {
	b, err := os.ReadFile("schema/programmatic.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"carriedDesign: AgentTaskCarriedDesign\n",
		"type AgentTaskCarriedDesign {",
		"type AgentTaskCarriedRound {",
		"type AgentTaskCarriedRole {",
		"  standing: Boolean!\n",
		"  decidedOver: Boolean!\n",
		"  live: Boolean!\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the schema has no %q", want)
		}
	}
	for _, typ := range []string{"input AgentTaskSplitChildInput {", "type AgentTaskSplitChild {"} {
		i := strings.Index(s, typ)
		if i < 0 {
			t.Fatalf("the schema has no %s", typ)
		}
		end := strings.Index(s[i:], "\n}")
		if !strings.Contains(s[i:i+end+1], "  designFromParent: Boolean\n") {
			t.Errorf("%s has no designFromParent: Boolean", typ)
		}
	}
}
