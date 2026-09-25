package rearm

import (
	"strings"
	"testing"
)

// The board's event log (task 1c5442d2): read since a point, with the cursor to read on from.
func TestTheEventLogRead(t *testing.T) {
	op := strings.Join(strings.Fields(AgentBoardEventsProgrammatic_Operation), " ")
	for _, want := range []string{"$after: Long", "$since: DateTime", "$limit: Int", "seq", "nextAfter", "hasMore"} {
		if !strings.Contains(op, want) {
			t.Errorf("the event log read lacks %q", want)
		}
	}
}

// Retention (task 04dedcc5): the page says where the board's window starts and whether this read
// lost events to it; the boards and the export carry the setting.
func TestTheEventLogRetention(t *testing.T) {
	op := strings.Join(strings.Fields(AgentBoardEventsProgrammatic_Operation), " ")
	for _, want := range []string{"truncatedBefore", "gap"} {
		if !strings.Contains(op, want) {
			t.Errorf("the event log read lacks %q", want)
		}
	}
	for name, doc := range map[string]string{
		"AgentBoardsProgrammatic": AgentBoardsProgrammatic_Operation,
		"AgentBoardProgrammatic":  AgentBoardProgrammatic_Operation,
	} {
		d := strings.Join(strings.Fields(doc), " ")
		if !strings.Contains(d, "eventRetentionDays effectiveEventRetentionDays") {
			t.Errorf("%s lacks the retention setting", name)
		}
	}
	if !strings.Contains(ExportBoard_Operation, "eventRetentionDays") {
		t.Error("the board export lacks the retention setting")
	}
	var page AgentBoardEventsProgrammaticAgentBoardEventsProgrammaticAgentBoardEventPage
	_ = page.GetTruncatedBefore()
	_ = page.GetGap()
}

// The merge procedure (task 71a3dd22): the board reads, the export and the seat carry it.
func TestTheMergeProcedureIsRead(t *testing.T) {
	merge := "merge { by method atTestedHead requireAttestation order }"
	for name, doc := range map[string]string{
		"AgentBoardsProgrammatic": AgentBoardsProgrammatic_Operation,
		"AgentBoardProgrammatic":  AgentBoardProgrammatic_Operation,
		"ExportBoard":             ExportBoard_Operation,
	} {
		if !strings.Contains(strings.Join(strings.Fields(doc), " "), merge) {
			t.Errorf("%s lacks the merge procedure", name)
		}
	}
	if !strings.Contains(AgentBoardCoordinateProgrammatic_Operation, "servedCoordinatorPrompt") {
		t.Error("taking the seat reads the served coordinator prompt")
	}
}
