package rearm

import (
	"encoding/json"
	"strings"
	"testing"
)

// A sign-off says what the hop read (task RD2-34): seenInputs goes to the server as given, nil as null (the
// check is skipped) and an empty list as [] (nothing read, so any newer document refuses it).
func TestSignOffSendsWhatTheHopRead(t *testing.T) {
	if !strings.Contains(normalised(AgentTaskSignOffProgrammatic_Operation), "seenInputs: $seenInputs") {
		t.Fatal("the sign-off does not send seenInputs")
	}
	for _, c := range []struct {
		seen []string
		want string
	}{{nil, `"seenInputs":null`}, {[]string{}, `"seenInputs":[]`}, {[]string{"r1"}, `"seenInputs":["r1"]`}} {
		b, err := json.Marshal(__AgentTaskSignOffProgrammaticInput{SeenInputs: c.seen})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), c.want) {
			t.Errorf("seenInputs %v marshals as %s, want %s", c.seen, b, c.want)
		}
	}
}

// The assign prints the task's documents, so the CLI records what it showed (task RD2-34).
func TestAssignReadsTheTasksDocuments(t *testing.T) {
	if !strings.Contains(normalised(AgentTaskAssignProgrammatic_Operation),
		"documents { uuid version lifecycle document { specification path round task advisory publishedByRole } }") {
		t.Error("the assign does not read the task's documents")
	}
}
