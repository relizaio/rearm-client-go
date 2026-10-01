package rearm

import (
	"strings"
	"testing"
)

// The report reads and the re-run come back as the report's release, carrying the whole report:
// the CLI prints the summary and each failing check's offences from it (elements.md §7).
func TestTheElementCheckOperationsSelectTheWholeReport(t *testing.T) {
	for name, op := range map[string]string{
		"report": AgentElementCheckReportProgrammatic_Operation, "run": AgentElementCheckRunProgrammatic_Operation,
	} {
		for _, want := range []string{"round", "catalogueVersion", "results {", "blocking", "offences {", "elementId", "scope {"} {
			if !strings.Contains(op, want) {
				t.Errorf("%s does not select %s", name, want)
			}
		}
	}
	if !strings.Contains(AgentElementCheckRunProgrammatic_Operation, "$sessionUuid: ID!") {
		t.Error("a run is a session's")
	}
}

// A board's element check policy is read where its element families are, and travels in the exported file.
func TestTheBoardViewsAndTheExportCarryTheElementCheckPolicy(t *testing.T) {
	for name, op := range map[string]string{
		"list": AgentBoardsProgrammatic_Operation, "show": AgentBoardProgrammatic_Operation,
	} {
		if !strings.Contains(op, "elementCheckPolicy {") || !strings.Contains(op, "effectiveElementCheckPolicy {") {
			t.Errorf("%s does not select the element check policy", name)
		}
	}
	if !strings.Contains(ExportBoard_Operation, "elementChecks {") || !strings.Contains(ExportBoard_Operation, "mandatoryFields") {
		t.Error("the export leaves the element check policy behind")
	}
	if SpecificationTypeBoardElementCheckReport != "BOARD_ELEMENT_CHECK_REPORT" {
		t.Error("BOARD_ELEMENT_CHECK_REPORT is a specification type")
	}
}
