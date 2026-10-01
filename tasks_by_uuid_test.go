package rearm

import (
	"strings"
	"testing"
)

// A named set of tasks in one read (task cc14f4cb) selects what task show selects, so a CLI that
// shows several tasks shows each as it shows one.
func TestTasksByUuidSelectsWhatTaskShowSelects(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	one := norm(AgentTaskProgrammatic_Operation)
	many := norm(AgentTasksByUuidProgrammatic_Operation)
	if !strings.Contains(many, "$taskUuids: [ID!]!") {
		t.Error("the read takes a list of uuids")
	}
	body := func(op string) string { return op[strings.Index(op, "{ uuid"):] }
	if body(one) != body(many) {
		t.Errorf("the selections differ:\none:  %s\nmany: %s", body(one), body(many))
	}
}
