package rearm

import (
	"strings"
	"testing"
)

// Task keys (task 3d1f9dd7): task reads carry the key and number, board reads the prefix and its
// history, the export the prefix, and both by-key reads exist for the CLI to resolve a key.
func TestTaskReadsCarryTheKeyAndTheByKeyReadsExist(t *testing.T) {
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task list": AgentTasksProgrammatic_Operation,
		"task show (several)": AgentTasksByUuidProgrammatic_Operation, "person task list": AgentTasksOfBoard_Operation,
		"snapshot": AgentBoardSnapshotProgrammatic_Operation,
	} {
		if !strings.Contains(normalised(op), "key number uuid") {
			t.Errorf("%s does not read the key and number", name)
		}
	}
	for name, op := range map[string]string{"board list": AgentBoardsProgrammatic_Operation, "board show": AgentBoardProgrammatic_Operation} {
		if !strings.Contains(normalised(op), "taskPrefix taskPrefixHistory") {
			t.Errorf("%s does not read the task prefix", name)
		}
	}
	if !strings.Contains(normalised(ExportBoard_Operation), "taskPrefix") {
		t.Error("the board export does not carry the task prefix")
	}
	if !strings.Contains(normalised(AgentTaskByKeyProgrammatic_Operation), "agentTaskByKeyProgrammatic(key: $key) { uuid key board }") {
		t.Error("the agent's by-key read is missing")
	}
	if !strings.Contains(normalised(AgentTaskByKey_Operation), "agentTaskByKey(orgUuid: $orgUuid, key: $key) { uuid key board }") {
		t.Error("the person's by-key read is missing")
	}
}
