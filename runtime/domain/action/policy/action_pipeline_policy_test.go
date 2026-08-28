package policy

import (
	"encoding/json"
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestActionPipelineNamesAndLookup(t *testing.T) {
	if got := ActionName(definitionmodel.ActionSchema{Key: "order.submit"}); got != "submit" {
		t.Fatalf("name from key = %q", got)
	}
	if got := ActionName(definitionmodel.ActionSchema{Key: "ignored", RequiresPermission: "sales.order.approve"}); got != "approve" {
		t.Fatalf("name from permission = %q", got)
	}
	for _, test := range []struct {
		input      string
		wantObject string
		wantAction string
	}{{" sales.order.approve ", "order", "approve"}, {"submit", "", "submit"}, {"", "", ""}, {"object. action ", "object", "action"}} {
		object, action := ActionSplitPermission(test.input)
		if object != test.wantObject || action != test.wantAction {
			t.Fatalf("split %q = %q/%q, want %q/%q", test.input, object, action, test.wantObject, test.wantAction)
		}
	}

	data := map[string]any{"empty": "  ", "count": 2, "nil": nil}
	if value, ok := ActionFirstPresent(data, "missing", "count"); !ok || value != 2 {
		t.Fatalf("first present = %#v, %v", value, ok)
	}
	if value, ok := ActionFirstPresent(nil, "count"); ok || value != nil {
		t.Fatalf("nil lookup = %#v, %v", value, ok)
	}
	if value, ok := ActionFirstPresent(data, "missing"); ok || value != nil {
		t.Fatalf("missing lookup = %#v, %v", value, ok)
	}
	if got := ActionFirstString(data, "empty", "count"); got != "" {
		t.Fatalf("first empty string = %q", got)
	}
	if got := ActionFirstString(data, "missing", "count"); got != "2" {
		t.Fatalf("first string = %q", got)
	}
	if got := ActionFirstString(data, "missing"); got != "" {
		t.Fatalf("missing first string = %q", got)
	}
}

func TestActionPipelineNumericConversions(t *testing.T) {
	intTests := []struct {
		value any
		want  int
		ok    bool
	}{{1, 1, true}, {int64(2), 2, true}, {2.9, 2, true}, {json.Number("3"), 3, true}, {json.Number("bad"), 0, false}, {" 4 ", 4, true}, {"bad", 0, false}, {true, 0, false}}
	for _, test := range intTests {
		got, ok := ActionPipelineInt(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("int(%#v) = %d/%v, want %d/%v", test.value, got, ok, test.want, test.ok)
		}
	}
	floatTests := []struct {
		value any
		want  float64
		ok    bool
	}{{1, 1, true}, {int64(2), 2, true}, {2.5, 2.5, true}, {json.Number("3.5"), 3.5, true}, {json.Number("bad"), 0, false}, {" 4.5 ", 4.5, true}, {"bad", 0, false}, {true, 0, false}}
	for _, test := range floatTests {
		got, ok := ActionPipelineFloat(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("float(%#v) = %v/%v, want %v/%v", test.value, got, ok, test.want, test.ok)
		}
	}
}

func TestActionApplyPipelineCompletionFields(t *testing.T) {
	for name, initial := range map[string]map[string]any{
		"missing status":  {},
		"empty status":    {"status": "  "},
		"terminal status": {"status": "failed", "completed_at": "old", "failure_reason": "old"},
		"open status":     {"status": "open"},
	} {
		t.Run("reopen "+name, func(t *testing.T) {
			ActionApplyPipelineCompletionFields(initial, recordmodel.Record{}, nil, "now", true)
			if initial["status"] != "open" || initial["completed_at"] != "" || initial["failure_reason"] != "" {
				t.Fatalf("reopened item = %#v", initial)
			}
		})
	}
	inProgress := map[string]any{"status": "open", "completed_at": "old", "failure_reason": "old"}
	ActionApplyPipelineCompletionFields(inProgress, recordmodel.Record{Data: map[string]any{"stage_type": "in_progress"}}, nil, "now", false)
	if !reflect.DeepEqual(inProgress, map[string]any{"status": "in_progress", "completed_at": "", "failure_reason": ""}) {
		t.Fatalf("in-progress item = %#v", inProgress)
	}
	preservedTerminal := map[string]any{"status": "won", "completed_at": "done", "failure_reason": ""}
	ActionApplyPipelineCompletionFields(preservedTerminal, recordmodel.Record{Data: map[string]any{"stage_type": "review"}}, nil, "now", false)
	if preservedTerminal["completed_at"] != "done" || preservedTerminal["status"] != "won" {
		t.Fatalf("terminal item changed = %#v", preservedTerminal)
	}
	defaultStage := map[string]any{}
	ActionApplyPipelineCompletionFields(defaultStage, recordmodel.Record{Data: map[string]any{"stage_type": nil}}, nil, "now", false)
	if defaultStage["status"] != "open" {
		t.Fatalf("default stage = %#v", defaultStage)
	}
	blankStage := map[string]any{"status": "", "completed_at": ""}
	ActionApplyPipelineCompletionFields(blankStage, recordmodel.Record{Data: map[string]any{"stage_type": ""}}, nil, "now", false)
	if blankStage["status"] != "open" {
		t.Fatalf("blank stage = %#v", blankStage)
	}
	completed := map[string]any{"status": "open", "completed_at": nil}
	ActionApplyPipelineCompletionFields(completed, recordmodel.Record{Data: map[string]any{"stage_type": "done"}}, nil, "2026-01-01T00:00:00Z", false)
	if completed["status"] != "done" || completed["completed_at"] != "2026-01-01T00:00:00Z" {
		t.Fatalf("completed item = %#v", completed)
	}
	blankCompleted := map[string]any{"status": "open", "completed_at": ""}
	ActionApplyPipelineCompletionFields(blankCompleted, recordmodel.Record{Data: map[string]any{"stage_type": "done"}}, nil, "now", false)
	if blankCompleted["completed_at"] != "now" {
		t.Fatalf("blank completion = %#v", blankCompleted)
	}
	alreadyCompleted := map[string]any{"status": "open", "completed_at": "existing"}
	ActionApplyPipelineCompletionFields(alreadyCompleted, recordmodel.Record{Data: map[string]any{"stage_type": "done"}}, nil, "now", false)
	if alreadyCompleted["completed_at"] != "existing" {
		t.Fatalf("existing completion changed = %#v", alreadyCompleted)
	}
	failed := map[string]any{"status": "open"}
	ActionApplyPipelineCompletionFields(failed, recordmodel.Record{Data: map[string]any{"stage_type": "lost"}}, map[string]any{"reason": " customer declined "}, "now", false)
	if failed["failure_reason"] != "customer declined" || failed["completed_at"] != "now" {
		t.Fatalf("failed item = %#v", failed)
	}
	fallbackReason := map[string]any{}
	ActionApplyPipelineCompletionFields(fallbackReason, recordmodel.Record{Data: map[string]any{"stage_type": "failed"}}, nil, "now", false)
	if fallbackReason["failure_reason"] != "failed" {
		t.Fatalf("fallback failure = %#v", fallbackReason)
	}
}

func TestActionPipelineStatusCSVAndBooleanPolicy(t *testing.T) {
	for _, status := range []string{"won", "lost", "done", "cancelled", "failed"} {
		if !ActionPipelineTerminalStatus(" " + status + " ") {
			t.Fatalf("%q should be terminal", status)
		}
	}
	if ActionPipelineTerminalStatus("open") {
		t.Fatal("open must not be terminal")
	}
	for _, status := range []string{"lost", "cancelled", "failed"} {
		if !ActionPipelineFailedStatus(status) {
			t.Fatalf("%q should be failed", status)
		}
	}
	if ActionPipelineFailedStatus("done") {
		t.Fatal("done must not be failed")
	}
	if !ActionPipelineStageTerminal(recordmodel.Record{Data: map[string]any{"is_terminal": true}}) || !ActionPipelineStageTerminal(recordmodel.Record{Data: map[string]any{"stage_type": "done"}}) || ActionPipelineStageTerminal(recordmodel.Record{Data: map[string]any{"is_terminal": false, "stage_type": "open"}}) {
		t.Fatal("stage terminal policy mismatch")
	}
	if !ActionPipelineStageFailed(recordmodel.Record{Data: map[string]any{"is_failed": "true"}}) || !ActionPipelineStageFailed(recordmodel.Record{Data: map[string]any{"stage_type": "lost"}}) || ActionPipelineStageFailed(recordmodel.Record{Data: map[string]any{"is_failed": false, "stage_type": "done"}}) {
		t.Fatal("stage failed policy mismatch")
	}
	if got := ActionSplitCSV(" a, ,b,a "); !reflect.DeepEqual(got, []string{"a", "b", "a"}) {
		t.Fatalf("CSV = %#v", got)
	}
	for _, value := range []string{"", "  ", "<nil>"} {
		if got := ActionSplitCSV(value); got != nil {
			t.Fatalf("empty CSV %q = %#v", value, got)
		}
	}
	for _, test := range []struct {
		value any
		want  bool
		ok    bool
	}{{true, true, true}, {false, false, true}, {" true ", true, true}, {"bad", false, false}, {1, false, false}} {
		got, ok := pipelineBool(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("pipelineBool(%#v) = %v/%v", test.value, got, ok)
		}
	}
}
