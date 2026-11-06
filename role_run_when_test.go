package rearm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TPF-156 T-100: the role reads, the seat's role-config set and both board-file fragments select the role's
// runWhen condition (backend TPF-155); the role part of a brief does not.
func TestRoleReadsSelectRunWhen(t *testing.T) {
	const want = "runWhen { taggedAny taggedNone }"
	for name, op := range map[string]string{
		"AgentTaskRoleConfigsProgrammatic":   AgentTaskRoleConfigsProgrammatic_Operation,
		"AgentTaskRoleConfigSetProgrammatic": AgentTaskRoleConfigSetProgrammatic_Operation,
		"ExportBoard":                        ExportBoard_Operation,
		"ExportRolePresets":                  ExportRolePresets_Operation,
	} {
		if !strings.Contains(normalised(op), want) {
			t.Errorf("%s does not select %q", name, want)
		}
	}
	if strings.Contains(AgentRoleBriefProgrammatic_Operation, "runWhen") {
		t.Error("the role brief selects runWhen; the condition reaches roles through the routing rules, not the brief")
	}
}

// T-100: the input carries presence. Left out (nil) is absent, so a set never touches a stored condition; an
// empty struct clears; the lists go as given.
func TestRunWhenInputCarriesPresence(t *testing.T) {
	marshal := func(in AgentTaskRoleConfigInput) string {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := marshal(AgentTaskRoleConfigInput{Name: "x"}); strings.Contains(got, "runWhen") {
		t.Errorf("an input without RunWhen marshals %s, want no runWhen key", got)
	}
	if got := marshal(AgentTaskRoleConfigInput{Name: "x", RunWhen: &AgentRoleRunWhenInput{}}); !strings.Contains(got,
		`"runWhen":{"taggedAny":null,"taggedNone":null}`) {
		t.Errorf("an empty RunWhen marshals %s, want the clearing object", got)
	}
	got := marshal(AgentTaskRoleConfigInput{Name: "x",
		RunWhen: &AgentRoleRunWhenInput{TaggedAny: []string{"mutation"}, TaggedNone: []string{}}})
	if !strings.Contains(got, `"runWhen":{"taggedAny":["mutation"],"taggedNone":[]}`) {
		t.Errorf("a condition marshals %s", got)
	}
}

// T-100: the read type takes null as nil and a condition as its two lists, and the generated condition type
// has exactly the two lists (no empty key, TPF-155 round 2 F-7).
func TestRunWhenUnmarshals(t *testing.T) {
	var none AgentTaskRoleConfigsProgrammaticAgentTaskRoleConfigsProgrammaticAgentTaskRoleConfig
	if err := json.Unmarshal([]byte(`{"runWhen":null}`), &none); err != nil {
		t.Fatal(err)
	}
	if none.GetRunWhen() != nil {
		t.Errorf("a null condition unmarshals to %+v, want nil", none.GetRunWhen())
	}
	var rc AgentTaskRoleConfigsProgrammaticAgentTaskRoleConfigsProgrammaticAgentTaskRoleConfig
	if err := json.Unmarshal([]byte(`{"runWhen":{"taggedAny":["mutation"],"taggedNone":[]}}`), &rc); err != nil {
		t.Fatal(err)
	}
	rw := rc.GetRunWhen()
	if rw == nil || !reflect.DeepEqual(rw.TaggedAny, []string{"mutation"}) || rw.TaggedNone == nil || len(rw.TaggedNone) != 0 {
		t.Errorf("the condition unmarshals to %+v", rw)
	}
	for name, typ := range map[string]reflect.Type{
		"role configs":    reflect.TypeOf(AgentTaskRoleConfigsProgrammaticAgentTaskRoleConfigsProgrammaticAgentTaskRoleConfigRunWhenAgentRoleRunWhen{}),
		"role config set": reflect.TypeOf(AgentTaskRoleConfigSetProgrammaticAgentTaskRoleConfigSetProgrammaticAgentTaskRoleConfigRunWhenAgentRoleRunWhen{}),
		"board file":      reflect.TypeOf(BoardRoleSpecFieldsRunWhenAgentRoleRunWhen{}),
	} {
		if typ.NumField() != 2 {
			t.Errorf("the %s condition type has %d fields, want taggedAny and taggedNone only", name, typ.NumField())
		}
	}

	// The export writes runWhen: null on every role without a condition; it reads as nil and writes back as null.
	var role ExportBoardExportBoardProgrammaticBoardSpecRolesBoardRoleSpec
	if err := json.Unmarshal([]byte(`{"name":"coder","runWhen":null}`), &role); err != nil {
		t.Fatal(err)
	}
	if role.GetRunWhen() != nil {
		t.Errorf("the export's null condition unmarshals to %+v, want nil", role.GetRunWhen())
	}
	b, err := json.Marshal(&role)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"runWhen":null`) {
		t.Errorf("the export role marshals %s, want runWhen null", b)
	}
	var cond ExportBoardExportBoardProgrammaticBoardSpecRolesBoardRoleSpec
	if err := json.Unmarshal([]byte(`{"name":"mutant_coder","runWhen":{"taggedAny":["mutation"],"taggedNone":[]}}`), &cond); err != nil {
		t.Fatal(err)
	}
	if cond.GetRunWhen() == nil || !reflect.DeepEqual(cond.GetRunWhen().TaggedAny, []string{"mutation"}) {
		t.Errorf("the export's condition unmarshals to %+v", cond.GetRunWhen())
	}
}

// T-102: the skip record on a task read. An empty list and null both read as no entry; a full entry reads field
// by field with live a plain bool; roleUuid and by are nullable.
func TestTheConditionSkipsUnmarshal(t *testing.T) {
	var empty, null AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
	if err := json.Unmarshal([]byte(`{"rolesSkippedByCondition":[]}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.RolesSkippedByCondition == nil || len(empty.GetRolesSkippedByCondition()) != 0 {
		t.Errorf("an empty list unmarshals to %#v, want an empty non-nil slice", empty.RolesSkippedByCondition)
	}
	if err := json.Unmarshal([]byte(`{"rolesSkippedByCondition":null}`), &null); err != nil {
		t.Fatal(err)
	}
	if null.RolesSkippedByCondition != nil || len(null.GetRolesSkippedByCondition()) != 0 {
		t.Errorf("a null list unmarshals to %#v, want nil", null.RolesSkippedByCondition)
	}

	for _, live := range []bool{true, false} {
		var task AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
		body := `{"rolesSkippedByCondition":[{"role":"mutant_coder","roleUuid":"r1","reason":"runWhen not met: no mutation tag",` +
			`"at":"2026-11-05T13:00:00Z","by":{"kind":"SYSTEM","uuid":null,"name":"routing"},"waives":["BOARD_TEST_REPORT"],` +
			`"live":` + map[bool]string{true: "true", false: "false"}[live] + `}]}`
		if err := json.Unmarshal([]byte(body), &task); err != nil {
			t.Fatal(err)
		}
		if len(task.GetRolesSkippedByCondition()) != 1 {
			t.Fatalf("%d entries, want 1", len(task.GetRolesSkippedByCondition()))
		}
		e := task.GetRolesSkippedByCondition()[0]
		if e.GetRole() != "mutant_coder" || e.GetRoleUuid() == nil || *e.GetRoleUuid() != "r1" ||
			e.GetReason() != "runWhen not met: no mutation tag" || e.GetAt() != "2026-11-05T13:00:00Z" {
			t.Errorf("the entry reads %+v", e)
		}
		if len(e.GetWaives()) != 1 || e.GetWaives()[0] != SpecificationType("BOARD_TEST_REPORT") {
			t.Errorf("waives reads %v", e.GetWaives())
		}
		if e.GetBy() == nil || e.GetBy().GetName() == nil || *e.GetBy().GetName() != "routing" {
			t.Errorf("by reads %+v, want routing", e.GetBy())
		}
		var l bool = e.GetLive() // a plain bool: the schema's live is non-null
		if l != live {
			t.Errorf("live reads %v, want %v", l, live)
		}
	}

	var bare AgentTaskProgrammaticAgentTaskProgrammaticAgentTask
	if err := json.Unmarshal([]byte(`{"rolesSkippedByCondition":[{"role":"mutant_coder","roleUuid":null,`+
		`"reason":"runWhen not met: tagged hotfix","at":"2026-11-05T13:00:00Z","by":null,"waives":[],"live":true}]}`), &bare); err != nil {
		t.Fatal(err)
	}
	e := bare.GetRolesSkippedByCondition()[0]
	var roleUuid *string = e.RoleUuid // nullable in the schema: roleUuid: ID
	if roleUuid != nil || e.By != nil || len(e.Waives) != 0 || !e.Live {
		t.Errorf("the bare entry reads %+v", e)
	}
}
