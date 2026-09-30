package rearm

import (
	"strings"
	"testing"
)

// Element grammar 1.2 (task RD4-6): the check preview returns the whole report without a release, and a board's
// families come with the types that define their ids, read on their own so a board read never needs a newer server.
func TestThePreviewSelectsTheWholeReportAndCreatesNothing(t *testing.T) {
	op := AgentCheckPreviewProgrammatic_Operation
	for _, want := range []string{"query AgentCheckPreviewProgrammatic", "$sessionUuid: ID!", "$taskUuid: ID!",
		"$specification: SpecificationType!", "$elements: String!", "$elementsDigest: String",
		"... CheckReportFields", "catalogueVersion", "blocking", "offences {"} {
		if !strings.Contains(op, want) {
			t.Errorf("the preview does not carry %s:\n%s", want, op)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(op), "mutation") {
		t.Error("a preview is a query: it changes nothing")
	}
}

func TestFamilyEntriesAreTheirOwnRead(t *testing.T) {
	for _, want := range []string{"effectiveElementFamilyEntries {", "prefix", "family", "definedIn"} {
		if !strings.Contains(AgentBoardElementFamilyEntries_Operation, want) {
			t.Errorf("entries do not select %s: %s", want, AgentBoardElementFamilyEntries_Operation)
		}
	}
	for name, op := range map[string]string{"show": AgentBoardProgrammatic_Operation, "list": AgentBoardsProgrammatic_Operation} {
		if strings.Contains(op, "effectiveElementFamilyEntries") {
			t.Errorf("the %s board read must not need a grammar 1.2 server", name)
		}
	}
	var e AgentBoardElementFamilyEntriesAgentBoardProgrammaticAgentBoardEffectiveElementFamilyEntriesElementFamilyEntry
	e.DefinedIn = []SpecificationType{}
	if e.GetDefinedIn() == nil {
		t.Error("definedIn is a list of specification types")
	}
}
