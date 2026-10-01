package rearm

import (
	"strings"
	"testing"
)

// The CLI parses documents against the board's effective element families and publishes the
// index with its digest (gaps §2.A, task e200cb32).
func TestTheBoardViewsSelectTheElementFamilies(t *testing.T) {
	for name, op := range map[string]string{
		"list": AgentBoardsProgrammatic_Operation, "show": AgentBoardProgrammatic_Operation,
	} {
		for _, want := range []string{"elementFamilies", "effectiveElementFamilies"} {
			if !strings.Contains(op, want) {
				t.Errorf("%s does not select %s", name, want)
			}
		}
	}
}

func TestThePublishInputCarriesTheElementIndex(t *testing.T) {
	var in AgentDocumentPublishInput
	s, d := `{"grammarVersion":"1","elements":[],"warnings":[]}`, "abc"
	in.Elements, in.ElementsDigest = &s, &d
	if *in.Elements == "" || *in.ElementsDigest == "" {
		t.Fatal("elements and elementsDigest are fields of the publish input")
	}
}
