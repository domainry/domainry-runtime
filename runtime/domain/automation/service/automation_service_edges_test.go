package service

import (
	"errors"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestProtocolValueMatchesTypeMatrix(t *testing.T) {
	if intFromAny("not-a-number") != 0 {
		t.Fatal("unsupported integer source did not fall back to zero")
	}
	if got := stringSliceFromAny([]any{" one ", " ", 2}); len(got) != 2 || got[0] != "one" || got[1] != "2" {
		t.Fatalf("string slice=%#v", got)
	}
	for _, test := range []struct {
		value     any
		fieldType string
		want      bool
	}{
		{value: nil, fieldType: "unknown", want: true},
		{value: "text", fieldType: " text ", want: true},
		{value: 1, fieldType: "text", want: false},
		{value: int32(2), fieldType: "integer", want: true},
		{value: float64(2), fieldType: "integer", want: true},
		{value: 2.5, fieldType: "integer", want: false},
		{value: "2", fieldType: "integer", want: false},
		{value: float32(2.5), fieldType: "decimal", want: true},
		{value: "2.5", fieldType: "decimal", want: false},
		{value: true, fieldType: "boolean", want: true},
		{value: "true", fieldType: "boolean", want: false},
		{value: func() {}, fieldType: "json", want: true},
		{value: "value", fieldType: "unsupported", want: false},
	} {
		if got := ProtocolValueMatchesType(test.value, test.fieldType); got != test.want {
			t.Fatalf("value=%#v type=%q got=%t want=%t", test.value, test.fieldType, got, test.want)
		}
	}
}

func TestValidateIntegrationOutputBoundaries(t *testing.T) {
	action := automationmodel.AutomationInstructionSchema{ConnectorKey: "crm", Operation: "create"}
	operation := connectormodel.ConnectorOperationSchema{Key: "create", Output: []definitionmodel.FieldSchema{
		{Key: "id", Type: "text", Required: true},
		{Key: "count", Type: "integer"},
	}}
	connector := connectormodel.ConnectorSchema{Key: "crm", Operations: []connectormodel.ConnectorOperationSchema{operation}}
	for _, test := range []struct {
		name       string
		connectors []connectormodel.ConnectorSchema
		output     map[string]any
		wantCode   string
	}{
		{name: "connector missing", wantCode: "backend.automation.connector_not_found"},
		{name: "operation missing", connectors: []connectormodel.ConnectorSchema{{Key: "crm", Operations: []connectormodel.ConnectorOperationSchema{{Key: "update"}}}}, wantCode: "backend.automation.connector_operation_not_found"},
		{name: "required missing", connectors: []connectormodel.ConnectorSchema{connector}, output: map[string]any{}, wantCode: "backend.automation.operation_output_required"},
		{name: "required empty", connectors: []connectormodel.ConnectorSchema{connector}, output: map[string]any{"id": " "}, wantCode: "backend.automation.operation_output_required"},
		{name: "wrong type", connectors: []connectormodel.ConnectorSchema{connector}, output: map[string]any{"id": "1", "count": 1.5}, wantCode: "backend.automation.operation_output_type_invalid"},
		{name: "valid", connectors: []connectormodel.ConnectorSchema{{Key: "other"}, connector}, output: map[string]any{"id": "1", "count": 2}},
		{name: "legacy connector", connectors: []connectormodel.ConnectorSchema{{Key: "crm"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateIntegrationOutput(test.connectors, action, test.output)
			if test.wantCode == "" && err != nil || test.wantCode != "" && apperror.CodeOf(err) != test.wantCode {
				t.Fatalf("error=%v code=%q want=%q", err, apperror.CodeOf(err), test.wantCode)
			}
		})
	}
}

func TestMatchingRulesChangedFieldsAndStableOrder(t *testing.T) {
	base := automationmodel.AutomationRuleSchema{Enabled: true, ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"}}
	rules := []automationmodel.AutomationRuleSchema{
		base,
		base,
		base,
		{Enabled: false, ObjectKey: "order", Trigger: base.Trigger},
		{Enabled: true, ObjectKey: "other", Trigger: base.Trigger},
		{Enabled: true, ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "create"}},
		{Enabled: true, ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update", ChangedFields: []string{"unchanged"}}},
	}
	rules[0].Key, rules[0].Priority, rules[0].Trigger.ChangedFields = "z", 20, []string{"status"}
	rules[1].Key, rules[1].Priority, rules[1].Trigger.ChangedFields = "a", 10, nil
	rules[2].Key, rules[2].Priority, rules[2].Trigger.ChangedFields = "b", 10, nil
	matched := MatchingRules(rules, "order", "after", "update", map[string]any{"status": "new", "unchanged": true}, map[string]any{"status": "paid", "unchanged": true})
	if len(matched) != 3 || matched[0].Key != "a" || matched[1].Key != "b" || matched[2].Key != "z" {
		t.Fatalf("matched=%#v", matched)
	}
	if ChangedFieldsMatch([]string{"status"}, map[string]any{"status": "paid"}, map[string]any{"status": "paid"}) {
		t.Fatal("unchanged field matched")
	}
	if !ChangedFieldsMatch(nil, nil, nil) {
		t.Fatal("empty changed-field requirement rejected")
	}
}

func TestMapConversionMutationVersionAndTraceFallback(t *testing.T) {
	if got := mapFromAny(nil); len(got) != 0 {
		t.Fatalf("nil map=%#v", got)
	}
	original := map[string]any{"nested": map[string]any{"value": "before"}}
	cloned := mapFromAny(original)
	cloned["added"] = true
	if _, found := original["added"]; found {
		t.Fatalf("map was not cloned: %#v", original)
	}
	if got := mapFromAny(struct {
		Value string `json:"value"`
	}{Value: "ok"}); got["value"] != "ok" {
		t.Fatalf("struct map=%#v", got)
	}
	if got := mapFromAny(func() {}); len(got) != 0 {
		t.Fatalf("marshal failure map=%#v", got)
	}
	if got := mapFromAny("scalar"); len(got) != 0 {
		t.Fatalf("non-object map=%#v", got)
	}

	unknown := MutationVersion(nil, recordmodel.Record{Data: map[string]any{"status": "new"}})
	known := MutationVersion(nil, recordmodel.Record{Data: map[string]any{"status": "new"}, UpdatedAt: "2026-07-19T00:00:00Z"})
	if !strings.HasPrefix(unknown, "unknown-") || !strings.HasPrefix(known, "2026-07-19T00:00:00Z-") || unknown == known {
		t.Fatalf("unknown=%q known=%q", unknown, known)
	}

	trace := automationprojection.AutomationRuleTrace{ExecutionID: "exec-1", RuleKey: "rule-1", Status: "failed", NodeTraces: []automationprojection.AutomationNodeTrace{{Input: map[string]any{"unsupported": func() {}}}}}
	fallback := TraceMap(trace)
	if fallback["execution_id"] != "exec-1" || fallback["rule_key"] != "rule-1" || fallback["status"] != "failed" || len(fallback) != 3 {
		t.Fatalf("fallback=%#v", fallback)
	}
	encoded := TraceMap(automationprojection.AutomationRuleTrace{ExecutionID: "exec-2", RuleKey: "rule-2", Status: "succeeded"})
	if encoded["execution_id"] != "exec-2" || encoded["rule_key"] != "rule-2" || encoded["status"] != "succeeded" {
		t.Fatalf("encoded=%#v", encoded)
	}
}

func TestAutomationErrorDetailsAndParams(t *testing.T) {
	code, params := automationErrorDetails(errors.New("plain"))
	if code != "backend.internal" || params != nil {
		t.Fatalf("code=%q params=%#v", code, params)
	}
	err := automationError(apperror.KindConflict, "backend.automation.conflict", errors.New("cause"), " operation ", "create", "", "ignored", "odd")
	if apperror.CodeOf(err) != "backend.automation.conflict" {
		t.Fatalf("error=%v", err)
	}
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Params["operation"] != "create" || len(appErr.Params) != 1 {
		t.Fatalf("app error=%#v", appErr)
	}
	withoutParams := automationError(apperror.KindBadRequest, "backend.automation.invalid", nil)
	if !errors.As(withoutParams, &appErr) || appErr.Params != nil {
		t.Fatalf("params=%#v", appErr.Params)
	}
}
