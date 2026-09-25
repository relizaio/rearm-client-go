package rearm

import (
	"strings"
	"testing"
)

// The report reads and the re-run come back as the report's release, carrying the whole report:
// the CLI prints the summary and each failing check's offences from it (elements.md §7).
func TestTheCheckOperationsSelectTheWholeReport(t *testing.T) {
	for name, op := range map[string]string{
		"report": AgentCheckReportProgrammatic_Operation, "run": AgentCheckRunProgrammatic_Operation,
	} {
		for _, want := range []string{"round", "catalogueVersion", "results {", "blocking", "offences {", "elementId", "scope {"} {
			if !strings.Contains(op, want) {
				t.Errorf("%s does not select %s", name, want)
			}
		}
	}
	if !strings.Contains(AgentCheckRunProgrammatic_Operation, "$sessionUuid: ID!") {
		t.Error("a run is a session's")
	}
}

// A board's check policy is read where its element families are, and travels in the exported file.
func TestTheBoardViewsAndTheExportCarryTheCheckPolicy(t *testing.T) {
	for name, op := range map[string]string{
		"list": AgentBoardsProgrammatic_Operation, "show": AgentBoardProgrammatic_Operation,
	} {
		if !strings.Contains(op, "checkPolicy {") || !strings.Contains(op, "effectiveCheckPolicy {") {
			t.Errorf("%s does not select the check policy", name)
		}
	}
	if !strings.Contains(ExportBoard_Operation, "checks {") || !strings.Contains(ExportBoard_Operation, "mandatoryFields") {
		t.Error("the export leaves the check policy behind")
	}
	if SpecificationTypeCheckReport != "CHECK_REPORT" {
		t.Error("CHECK_REPORT is a specification type")
	}
}
