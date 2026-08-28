package policy

import (
	"context"
	"reflect"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestWorkflowRecordUpdatedTriggersAndChangedFields(t *testing.T) {
	before := map[string]any{"same": 1, "changed": "old", "removed": true}
	after := map[string]any{"same": 1, "changed": "new", "added": 2}
	wantFields := []string{"added", "changed", "removed"}
	if got := WorkflowChangedRecordFieldKeys(before, after); !reflect.DeepEqual(got, wantFields) {
		t.Fatalf("changed fields = %#v, want %#v", got, wantFields)
	}
	wantTriggers := []string{"record_updated:invoice", "record_updated:invoice.added", "record_updated:invoice.changed", "record_updated:invoice.removed"}
	if got := WorkflowRecordUpdatedTriggers("invoice", before, after); !reflect.DeepEqual(got, wantTriggers) {
		t.Fatalf("triggers = %#v, want %#v", got, wantTriggers)
	}
	if containsText([]string{"a", "b"}, "missing") {
		t.Fatal("containsText matched missing value")
	}
}

func TestWorkflowTriggerContractMatches(t *testing.T) {
	tests := []struct {
		name      string
		contract  definitionmodel.WorkflowTriggerContract
		objectKey string
		trigger   string
		want      bool
	}{
		{name: "object mismatch", contract: definitionmodel.WorkflowTriggerContract{Type: "record_created", ObjectKey: "invoice"}, objectKey: "order", trigger: "record_created:order"},
		{name: "record created", contract: definitionmodel.WorkflowTriggerContract{Type: "record_created"}, objectKey: "invoice", trigger: "record_created:invoice", want: true},
		{name: "record created mismatch", contract: definitionmodel.WorkflowTriggerContract{Type: "record_created"}, objectKey: "invoice", trigger: "record_updated:invoice"},
		{name: "record updated", contract: definitionmodel.WorkflowTriggerContract{Type: "record_updated"}, objectKey: "invoice", trigger: "record_updated:invoice", want: true},
		{name: "field changed", contract: definitionmodel.WorkflowTriggerContract{Type: "field_changed", FieldKey: " status "}, objectKey: "invoice", trigger: "record_updated:invoice.status", want: true},
		{name: "field required", contract: definitionmodel.WorkflowTriggerContract{Type: "field_changed"}, objectKey: "invoice", trigger: "record_updated:invoice."},
		{name: "action completed", contract: definitionmodel.WorkflowTriggerContract{Type: "action_completed", Event: " approve "}, trigger: "action_executed:approve", want: true},
		{name: "action event required", contract: definitionmodel.WorkflowTriggerContract{Type: "action_completed"}, trigger: "action_executed:"},
		{name: "unknown", contract: definitionmodel.WorkflowTriggerContract{Type: "unknown"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := workflowTriggerContractMatches(test.contract, test.objectKey, test.trigger); got != test.want {
				t.Fatalf("match = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWorkflowMatchesRecordEventAndTriggerObjectKeys(t *testing.T) {
	if WorkflowMatchesRecordEvent(t.Context(), definitionmodel.WorkflowSchema{}, "invoice", nil, nil, "record_created:invoice") {
		t.Fatal("workflow without trigger contract matched")
	}
	empty := " "
	workflow := definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: empty}}
	if WorkflowMatchesRecordEvent(t.Context(), workflow, "invoice", nil, nil, "record_created:invoice") {
		t.Fatal("workflow with blank trigger contract matched")
	}
	workflow.TriggerContract = &definitionmodel.WorkflowTriggerContract{Type: "record_created", ObjectKey: "invoice", ObjectKeys: []string{" order ", "invoice", "", "order"}}
	if !WorkflowMatchesRecordEvent(t.Context(), workflow, "invoice", nil, nil, "record_created:invoice") {
		t.Fatal("record event did not match")
	}
	if got := WorkflowTriggerObjectKeys(workflow); !reflect.DeepEqual(got, []string{"invoice", "order"}) {
		t.Fatalf("trigger object keys = %#v", got)
	}
	if got := WorkflowTriggerObjectKeys(definitionmodel.WorkflowSchema{}); len(got) != 0 || got == nil {
		t.Fatalf("nil trigger keys = %#v", got)
	}
}

func TestWorkflowConditionContractMatches(t *testing.T) {
	ctx := context.Background()
	data := map[string]any{"status": "approved", "amount": 12}
	before := map[string]any{"status": "draft", "amount": 12}
	trueCondition := definitionmodel.WorkflowConditionContract{Type: "field_equals", Field: "record.status", Value: "approved"}
	falseCondition := definitionmodel.WorkflowConditionContract{Type: "field_equals", Field: "$status", Value: "draft"}

	tests := []struct {
		name      string
		condition definitionmodel.WorkflowConditionContract
		want      bool
	}{
		{name: "default always", condition: definitionmodel.WorkflowConditionContract{}, want: true},
		{name: "always", condition: definitionmodel.WorkflowConditionContract{Type: "always"}, want: true},
		{name: "field equals", condition: trueCondition, want: true},
		{name: "field equals false", condition: falseCondition},
		{name: "field changed", condition: definitionmodel.WorkflowConditionContract{Type: "field_changed", Field: "after.status"}, want: true},
		{name: "field unchanged", condition: definitionmodel.WorkflowConditionContract{Type: "field_changed", Field: "amount"}},
		{name: "expression", condition: definitionmodel.WorkflowConditionContract{Type: "expression", Expression: "status == approved"}, want: true},
		{name: "all", condition: definitionmodel.WorkflowConditionContract{Type: "all", Conditions: []definitionmodel.WorkflowConditionContract{trueCondition, {Type: "always"}}}, want: true},
		{name: "and false", condition: definitionmodel.WorkflowConditionContract{Type: "and", Conditions: []definitionmodel.WorkflowConditionContract{trueCondition, falseCondition}}},
		{name: "any", condition: definitionmodel.WorkflowConditionContract{Type: "any", Conditions: []definitionmodel.WorkflowConditionContract{falseCondition, trueCondition}}, want: true},
		{name: "or false", condition: definitionmodel.WorkflowConditionContract{Type: "or", Conditions: []definitionmodel.WorkflowConditionContract{falseCondition}}},
		{name: "not", condition: definitionmodel.WorkflowConditionContract{Type: "not", Condition: &falseCondition}, want: true},
		{name: "not missing", condition: definitionmodel.WorkflowConditionContract{Type: "not"}},
		{name: "unknown", condition: definitionmodel.WorkflowConditionContract{Type: "unknown"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := WorkflowConditionContractMatches(ctx, test.condition, data, before); got != test.want {
				t.Fatalf("match = %v, want %v", got, test.want)
			}
		})
	}

	if !WorkflowConditionMatches(ctx, definitionmodel.WorkflowSchema{}, data) {
		t.Fatal("workflow without condition must match")
	}
	blank := definitionmodel.WorkflowConditionContract{Type: " "}
	if !WorkflowConditionMatches(ctx, definitionmodel.WorkflowSchema{ConditionContract: &blank}, data) {
		t.Fatal("workflow with blank condition must match")
	}
	if WorkflowConditionMatches(ctx, definitionmodel.WorkflowSchema{ConditionContract: &falseCondition}, data) {
		t.Fatal("false workflow condition matched")
	}
}

func TestResolvePayloadPath(t *testing.T) {
	data := map[string]any{"status": "approved"}
	for _, field := range []string{"status", "$status", "record.status", "after.status"} {
		if got := resolvePayloadPath(data, field); got != "approved" {
			t.Fatalf("path %q = %#v", field, got)
		}
	}
	if got := resolvePayloadPath(data, " $"); got != nil {
		t.Fatalf("empty path = %#v", got)
	}
	if got := workflowValueOrDefault(" configured ", "fallback"); got != "configured" {
		t.Fatalf("configured value = %q", got)
	}
}

func TestStructuredWorkflowConditionMatches(t *testing.T) {
	data := map[string]any{"status": "Approved"}
	tests := []struct {
		condition map[string]any
		want      bool
	}{
		{condition: nil, want: true},
		{condition: map[string]any{"field": nil}, want: true},
		{condition: map[string]any{"field": "status", "equals": "approved"}, want: true},
		{condition: map[string]any{"field": "status", "equals": "draft"}},
		{condition: map[string]any{"field": "status", "not_equals": "draft"}, want: true},
		{condition: map[string]any{"field": "status", "not_equals": "approved"}},
		{condition: map[string]any{"field": "status", "in": []string{"draft", "approved"}}, want: true},
		{condition: map[string]any{"field": "status", "values": []any{"draft", "closed"}}},
		{condition: map[string]any{"field": "status"}, want: true},
	}
	for _, test := range tests {
		if got := structuredWorkflowConditionMatches(test.condition, data); got != test.want {
			t.Fatalf("condition %#v = %v, want %v", test.condition, got, test.want)
		}
	}
}

func TestWorkflowConditionExpressionMatches(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	date := func(offset int) string { return today.AddDate(0, 0, offset).Format("2006-01-02") }
	now := time.Now().UTC()
	data := map[string]any{
		"status": "approved", "empty": " ", "present": "value", "amount": 12.0, "discount": 2,
		"past": date(-3), "today": date(0), "future": date(3), "invalid_date": "bad",
		"past_time":   now.Add(-time.Hour).Format(time.RFC3339Nano),
		"future_time": now.Add(time.Hour).Format(time.RFC3339Nano),
	}
	tests := []struct {
		expression string
		want       bool
	}{
		{expression: "status == approved and amount >= 10", want: true},
		{expression: "status == draft and amount >= 10"},
		{expression: "status == draft or amount > 10", want: true},
		{expression: "status == draft or amount < 10"},
		{expression: "past <= days_ago:2", want: true},
		{expression: "past < days_ago:2", want: true},
		{expression: "invalid_date <= days_ago:2"},
		{expression: "past <= days_ago:bad"},
		{expression: "future <= days_ahead:3", want: true},
		{expression: "past_time <= now", want: true},
		{expression: "past_time < now", want: true},
		{expression: "future_time >= now", want: true},
		{expression: "future_time > now", want: true},
		{expression: "future_time <= now"},
		{expression: "invalid_date <= now"},
		{expression: "future < days_ahead:4", want: true},
		{expression: "future <= days_ahead:-1"},
		{expression: "today <= today", want: true},
		{expression: "past < today", want: true},
		{expression: "invalid_date < today"},
		{expression: "empty is empty", want: true},
		{expression: "present is present", want: true},
		{expression: "empty is present"},
		{expression: "status == APPROVED", want: true},
		{expression: "status != draft", want: true},
		{expression: "status not in draft,closed", want: true},
		{expression: "status in draft,approved", want: true},
		{expression: "amount - discount >= 10", want: true},
		{expression: "amount > 11", want: true},
		{expression: "discount <= 2", want: true},
		{expression: "discount < amount", want: true},
		{expression: "missing >= 1"},
		{expression: "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.expression, func(t *testing.T) {
			if got := WorkflowConditionExpressionMatches(t.Context(), test.expression, data); got != test.want {
				t.Fatalf("match = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWorkflowConditionValueHelpers(t *testing.T) {
	if !stringInCSV("Approved", "draft, approved") || stringInCSV("missing", "draft, approved") {
		t.Fatal("CSV membership mismatch")
	}
	if _, ok := daysAgoCutoff(t.Context(), "bad"); ok {
		t.Fatal("invalid days-ago cutoff accepted")
	}
	if _, ok := daysAheadCutoff(t.Context(), "-1"); ok {
		t.Fatal("negative days-ahead cutoff accepted")
	}
	if value, ok := numericExpressionAny("10 - 3 - 2", nil); !ok || value != 5 {
		t.Fatalf("numeric expression = %v %v", value, ok)
	}
	for _, expression := range []string{"", "10 - ", "unknown"} {
		if _, ok := numericExpressionAny(expression, nil); ok {
			t.Fatalf("invalid numeric expression %q accepted", expression)
		}
	}
	if got := stringListFromAny([]string{"a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("string list = %#v", got)
	}
	if got := stringListFromAny([]any{"a", 2}); !reflect.DeepEqual(got, []string{"a", "2"}) {
		t.Fatalf("any list = %#v", got)
	}
	if got := stringListFromAny("unsupported"); len(got) != 0 {
		t.Fatalf("unsupported list = %#v", got)
	}

	numericCases := []struct {
		value any
		want  float64
		ok    bool
	}{{float64(1.5), 1.5, true}, {float32(2.5), 2.5, true}, {3, 3, true}, {int64(4), 4, true}, {"5.5", 5.5, true}, {"bad", 0, false}, {true, 0, false}}
	for _, test := range numericCases {
		got, ok := numericAny(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("numericAny(%#v) = %v,%v want %v,%v", test.value, got, ok, test.want, test.ok)
		}
	}

	boolCases := []struct {
		value any
		want  bool
		ok    bool
	}{{true, true, true}, {false, false, true}, {" yes ", true, true}, {"0", false, true}, {"maybe", false, false}, {1, false, false}}
	for _, test := range boolCases {
		got, ok := boolAny(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("boolAny(%#v) = %v,%v want %v,%v", test.value, got, ok, test.want, test.ok)
		}
	}

	for _, value := range []any{nil, "", "bad"} {
		if _, ok := workflowRecordDateOnly(value); ok {
			t.Fatalf("invalid date %#v accepted", value)
		}
	}
	if parsed, ok := workflowRecordDateOnly("2026-07-19T12:00:00Z"); !ok || parsed.Format("2006-01-02") != "2026-07-19" {
		t.Fatalf("parsed date = %v,%v", parsed, ok)
	}
	if !workflowRecordIsEmptyValue(nil) || !workflowRecordIsEmptyValue(" ") || workflowRecordIsEmptyValue("value") || workflowRecordIsEmptyValue(0) {
		t.Fatal("empty value classification mismatch")
	}
}
