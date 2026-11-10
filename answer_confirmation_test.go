package rearm

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// genqlient writes the spread as "... ActorFields" in the generated text.
const answerConfirmationsSelection = "answerConfirmations { answeredAt role roleUuid session agent askingRole" +
	" questionsRelease rounds { release specification round version path previous previousRound previousVersion" +
	" previousPath } state settledBy { ... ActorFields } settledAt note }"

// TPF-173 (TPF-171 7.3): the seat's and the person's verdict on an answer signed off --no-change take the
// verdict as the schema's enum and call their mutation; both read the task back with its confirmations.
func TestTheConfirmAnswerOperations(t *testing.T) {
	for name, c := range map[string]struct {
		op   string
		want []string
	}{
		"seat": {AgentTaskConfirmAnswerProgrammatic_Operation, []string{
			"$taskUuid: ID!", "$sessionUuid: ID!", "$verdict: AgentTaskAnswerVerdict!", "$note: String",
			"agentTaskConfirmAnswerProgrammatic(taskUuid: $taskUuid, sessionUuid: $sessionUuid, verdict: $verdict, note: $note)",
		}},
		"person": {AgentTaskConfirmAnswer_Operation, []string{
			"$taskUuid: ID!", "$verdict: AgentTaskAnswerVerdict!", "$note: String",
			"agentTaskConfirmAnswer(taskUuid: $taskUuid, verdict: $verdict, note: $note)",
		}},
	} {
		for _, w := range c.want {
			if !strings.Contains(c.op, w) {
				t.Errorf("%s lacks %q", name, w)
			}
		}
		n := normalised(c.op)
		if !strings.Contains(n, answerConfirmationsSelection) {
			t.Errorf("%s does not read the confirmations back", name)
		}
		if !strings.Contains(n, "statusHistory { from to at trigger actor { ... ActorFields } note }") {
			t.Errorf("%s does not read the status history back (the verdict's row)", name)
		}
	}
	if strings.Contains(AgentTaskConfirmAnswer_Operation, "sessionUuid") {
		t.Error("the person's verdict names a session")
	}
	if AgentTaskAnswerVerdictConfirmed != "CONFIRMED" || AgentTaskAnswerVerdictRefused != "REFUSED" {
		t.Error("the verdicts")
	}
	if AgentStatusTriggerAnswerConfirmed != "ANSWER_CONFIRMED" || AgentStatusTriggerAnswerRefused != "ANSWER_REFUSED" {
		t.Error("the verdict's status triggers")
	}
}

// TPF-173 (TPF-171 7.3): the two reads task show prints from select the confirmations whole.
func TestShowReadsSelectTheAnswerConfirmations(t *testing.T) {
	for name, op := range map[string]string{
		"one": AgentTaskProgrammatic_Operation, "by uuid": AgentTasksByUuidProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), answerConfirmationsSelection) {
			t.Errorf("%s does not select answerConfirmations", name)
		}
	}
}

// The generated task type carries the record: a PENDING entry with no settler and a CONFIRMED one with its
// settler, oldest first as the server sends them.
func TestTheAnswerConfirmationsUnmarshal(t *testing.T) {
	body := `{"answerConfirmations":[` +
		`{"answeredAt":"2026-11-10T15:15:00Z","role":"architect","roleUuid":"ro-1","session":"s-1","agent":"a-1",` +
		`"askingRole":"ro-4","questionsRelease":"q-1","rounds":[{"release":"r-3","specification":"ARCHITECTURE",` +
		`"round":3,"version":"26.11.336","path":"design/OOL-5/architecture-3.md","previous":"r-2","previousRound":2,` +
		`"previousVersion":"26.11.333","previousPath":"design/OOL-5/architecture-2.md"}],"state":"CONFIRMED",` +
		`"settledBy":{"kind":"SESSION","uuid":"seat","name":"seat"},"settledAt":"2026-11-10T15:18:00Z","note":"wording only"},` +
		`{"answeredAt":"2026-11-10T16:02:00Z","role":"architect","roleUuid":null,"session":null,"agent":null,` +
		`"askingRole":null,"questionsRelease":null,"rounds":[{"release":"r-4","specification":"ARCHITECTURE",` +
		`"round":null,"version":null,"path":null,"previous":null,"previousRound":null,"previousVersion":null,` +
		`"previousPath":null}],"state":"PENDING","settledBy":null,"settledAt":null,"note":null}]}`
	var task AgentTasksByUuidProgrammaticAgentTasksByUuidProgrammaticAgentTask
	if err := json.Unmarshal([]byte(body), &task); err != nil {
		t.Fatal(err)
	}
	cs := task.GetAnswerConfirmations()
	if len(cs) != 2 {
		t.Fatalf("%d entries, want 2", len(cs))
	}
	done, open := cs[0], cs[1]
	if done.GetState() != AgentTaskAnswerConfirmationStateConfirmed || done.GetRole() != "architect" ||
		done.GetSettledBy() == nil || done.GetSettledBy().GetName() == nil || *done.GetSettledBy().GetName() != "seat" ||
		done.GetNote() == nil || *done.GetNote() != "wording only" || done.GetSession() == nil {
		t.Errorf("the confirmed entry unmarshals to %+v", done)
	}
	r := done.GetRounds()[0]
	if r.GetRelease() != "r-3" || r.GetSpecification() != "ARCHITECTURE" || r.GetRound() == nil || *r.GetRound() != 3 ||
		r.GetPrevious() == nil || *r.GetPrevious() != "r-2" || r.GetPreviousRound() == nil || *r.GetPreviousRound() != 2 ||
		r.GetPreviousPath() == nil || r.GetVersion() == nil {
		t.Errorf("the round unmarshals to %+v", r)
	}
	if open.GetState() != AgentTaskAnswerConfirmationStatePending || open.GetSettledBy() != nil ||
		open.GetSettledAt() != nil || open.GetSession() != nil || open.GetAgent() != nil {
		t.Errorf("the pending entry unmarshals to %+v", open)
	}
	if open.GetRounds()[0].GetRound() != nil || open.GetRounds()[0].GetPrevious() != nil {
		t.Errorf("null rounds unmarshal to %+v", open.GetRounds()[0])
	}
}

// The committed schema is the server's bundle, refreshed whole from the TPF-172 tree: the two mutations, the
// record, its enums and the two status triggers, as the server declares them.
func TestTheSchemaCarriesTheAnswerConfirmation(t *testing.T) {
	b, err := os.ReadFile("schema/programmatic.graphqls")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"  agentTaskConfirmAnswerProgrammatic(\n  taskUuid: ID!\n  sessionUuid: ID!\n  verdict: AgentTaskAnswerVerdict!\n  note: String): AgentTask\n",
		"  agentTaskConfirmAnswer(\n  taskUuid: ID!\n  verdict: AgentTaskAnswerVerdict!\n  note: String): AgentTask\n",
		"  answerConfirmations: [AgentTaskAnswerConfirmation!]!\n",
		"type AgentTaskAnswerConfirmation {",
		"type AgentTaskAnswerRound {",
		"enum AgentTaskAnswerVerdict {\n  CONFIRMED\n  REFUSED\n}",
		"enum AgentTaskAnswerConfirmationState {\n  PENDING\n  CONFIRMED\n  REFUSED\n  LAPSED\n}",
		"  ANSWER_CONFIRMED\n",
		"  ANSWER_REFUSED\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the schema has no %q", want)
		}
	}
}
