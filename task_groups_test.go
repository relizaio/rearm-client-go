package rearm

import (
	"strings"
	"testing"
)

// Task groups and tags (RD2-29): every task read carries the group, the tags and what it waits on;
// the lists filter by group and tag; the group and tag verbs exist for the seat and for people.
func TestTaskReadsCarryGroupsAndTags(t *testing.T) {
	sel := "group { uuid key name } tags { key value removable } waitingOnGroups"
	for name, op := range map[string]string{
		"task show": AgentTaskProgrammatic_Operation, "task list": AgentTasksProgrammatic_Operation,
		"tasks by uuid": AgentTasksByUuidProgrammatic_Operation, "boards tasks": AgentTasksOfBoard_Operation,
	} {
		if !strings.Contains(normalised(op), sel) {
			t.Errorf("%s does not read the group, tags and waitingOnGroups", name)
		}
	}
	if !strings.Contains(normalised(AgentBoardSnapshotProgrammatic_Operation), "group { key } tags { key } waitingOnGroups") {
		t.Error("the snapshot's task does not read its group and what it waits on")
	}
	if !strings.Contains(normalised(AgentTasksProgrammatic_Operation), "group: $group, tag: $tag") ||
		!strings.Contains(normalised(AgentTasksOfBoard_Operation), "group: $group, tag: $tag") {
		t.Error("the task lists do not filter by group and tag")
	}
	if !strings.Contains(normalised(AgentBoardProgrammatic_Operation), "groups { uuid key name description order dependsOn defaultLevel status createdAt progress") {
		t.Error("board show does not read the groups")
	}
	if !strings.Contains(normalised(AgentBoardGroupsProgrammatic_Operation), "spentMicros") {
		t.Error("the groups read does not carry what they spent")
	}
	for name, op := range map[string]string{
		"seat group set": AgentBoardGroupSetProgrammatic_Operation, "seat setgroup": AgentTaskSetGroupProgrammatic_Operation,
		"seat tags": AgentTaskSetTagsProgrammatic_Operation, "person group set": AgentBoardGroupSet_Operation,
		"person group delete": AgentBoardGroupDelete_Operation, "person setgroup": AgentTaskSetGroup_Operation,
		"person tags": AgentTaskSetTags_Operation,
	} {
		if op == "" {
			t.Errorf("no %s operation", name)
		}
	}
}
