package runtime

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func automationRenderTestContext() *automationmodel.AutomationRenderContext {
	return &automationmodel.AutomationRenderContext{
		Payload: map[string]any{"nested": map[string]any{"value": "payload"}},
		Record:  map[string]any{"value": "record"},
		Input:   map[string]any{"value": "input"},
		Before:  map[string]any{"value": "before"},
		Actor:   map[string]any{"value": "actor"},
		Event:   map[string]any{"value": "event"},
		Results: map[string]automationmodel.AutomationInstructionResult{
			"created": {ObjectKey: "order", RecordID: "order-1", Data: map[string]any{"status": "ready"}},
		},
	}
}

func TestAutomationRenderAndMergePayloadPaths(t *testing.T) {
	ctx := automationRenderTestContext()
	rendered, err := AutomationRenderStepData(map[string]any{"data": map[string]any{"value": "$input.value"}}, ctx)
	if err != nil || rendered["value"] != "input" {
		t.Fatalf("rendered data=%#v err=%v", rendered, err)
	}
	rendered, err = AutomationRenderStepData(map[string]any{"value": "$record.value"}, ctx)
	if err != nil || rendered["value"] != "record" {
		t.Fatalf("rendered value=%#v err=%v", rendered, err)
	}
	rendered, err = AutomationRenderStepData(map[string]any{"field": "$actor.value"}, ctx)
	if err != nil || rendered["field"] != "actor" {
		t.Fatalf("rendered step=%#v err=%v", rendered, err)
	}
	if _, err := AutomationRenderData("plain", ctx); AutomationRuntimeErrorCode(err) != "backend.action.data_object_required" {
		t.Fatalf("render data error=%v", err)
	}
	if value, err := AutomationRenderOptionalData(nil, ctx); err != nil || value != nil {
		t.Fatalf("optional data=%#v err=%v", value, err)
	}
	if _, err := AutomationRenderOptionalData("plain", ctx); err == nil {
		t.Fatal("invalid optional data accepted")
	}

	object := AutomationRenderObjectValue(map[string]any{
		"record": "$record.value",
		"list":   []any{"$before.value", 7},
	}, ctx).(map[string]any)
	if object["record"] != "record" || !reflect.DeepEqual(object["list"], []any{"before", 7}) ||
		AutomationRenderObjectValue(9, ctx) != 9 || AutomationRenderString(nil, ctx) != "" {
		t.Fatalf("rendered object=%#v", object)
	}

	for source, want := range map[string]any{
		"$payload.nested.value":     "payload",
		"$candidate.nested.value":   "payload",
		"$input.value":              "input",
		"$before.value":             "before",
		"$actor.value":              "actor",
		"$event.value":              "event",
		"$record.value":             "record",
		"$steps.created":            map[string]any{"status": "ready"},
		"$steps.created.record_id":  "order-1",
		"$steps.created.object_key": "order",
		"$steps.created.status":     "ready",
		"$actions.created.status":   "ready",
		"$steps.missing.value":      "",
		"literal":                   "literal",
		"":                          "",
	} {
		if got := renderStringOrValue(source, ctx); !reflect.DeepEqual(got, want) {
			t.Fatalf("render %q=%#v want=%#v", source, got, want)
		}
	}
	if got := renderStringOrValue("$input.value", nil); got != "$input.value" {
		t.Fatalf("nil context render=%#v", got)
	}

	noTarget := automationRenderTestContext()
	if err := AutomationMergeTargetPayload(map[string]any{}, map[string]any{"value": "ignored"}, noTarget); err != nil {
		t.Fatal(err)
	}
	if _, exists := noTarget.Payload["value"]; exists {
		t.Fatalf("unexpected no-target merge=%#v", noTarget.Payload)
	}
	patchContext := &automationmodel.AutomationRenderContext{Input: map[string]any{"value": "patched"}}
	if err := AutomationMergeTargetPayload(
		map[string]any{"target_payload": map[string]any{"": "ignored", "field": "$input.value"}},
		map[string]any{}, patchContext,
	); err != nil || patchContext.Payload["field"] != "patched" {
		t.Fatalf("patch payload=%#v err=%v", patchContext.Payload, err)
	}
	if err := AutomationMergeTargetPayload(map[string]any{"target_payload": "invalid"}, nil, patchContext); err == nil {
		t.Fatal("invalid target payload accepted")
	}
	fieldContext := &automationmodel.AutomationRenderContext{}
	if err := AutomationMergeTargetPayload(map[string]any{"target_field": "result"}, map[string]any{"value": 7}, fieldContext); err != nil || fieldContext.Payload["result"] != 7 {
		t.Fatalf("single field payload=%#v err=%v", fieldContext.Payload, err)
	}
	if err := AutomationMergeTargetPayload(map[string]any{"field_key": "result"}, map[string]any{"value": 1, "other": 2}, fieldContext); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fieldContext.Payload["result"], map[string]any{"value": 1, "other": 2}) {
		t.Fatalf("object field payload=%#v", fieldContext.Payload)
	}
	if err := AutomationMergeTargetPayload(map[string]any{"target_field": "result"}, map[string]any{"other": 3}, fieldContext); err != nil {
		t.Fatal(err)
	}
	mergeContext := &automationmodel.AutomationRenderContext{}
	if err := AutomationMergeTargetPayload(
		map[string]any{"writes_target_payload": "true"},
		map[string]any{"": "ignored", "status": "ready"},
		mergeContext,
	); err != nil || mergeContext.Payload["status"] != "ready" {
		t.Fatalf("merged payload=%#v err=%v", mergeContext.Payload, err)
	}
}

func TestAutomationCalculateNumberOperatorsAndErrors(t *testing.T) {
	tests := []struct {
		operator string
		left     any
		right    any
		want     float64
		code     string
	}{
		{operator: "add", left: 5, right: 2, want: 7},
		{operator: "-", left: 5, right: 2, want: 3},
		{operator: "*", left: 5, right: 2, want: 10},
		{operator: "/", left: 5, right: 2, want: 2.5},
		{operator: "ceil_to_multiple", left: 5, right: 2, want: 6},
		{operator: "/", left: 5, right: 0, code: "backend.action.calculate_divide_by_zero"},
		{operator: "ceil_to_multiple", left: 5, right: 0, code: "backend.action.calculate_multiple_invalid"},
		{operator: "unknown", left: 5, right: 2, code: "backend.action.calculate_operator_invalid"},
		{operator: "add", left: "invalid", right: 2, code: "backend.action.calculate_number_invalid"},
		{operator: "add", left: 2, right: "invalid", code: "backend.action.calculate_number_invalid"},
	}
	for _, test := range tests {
		t.Run(fmt.Sprint(test.operator, test.left, test.right), func(t *testing.T) {
			result, err := AutomationCalculateNumber(map[string]any{
				"operator": test.operator, "left": test.left, "right": test.right,
			}, nil)
			if test.code != "" {
				if AutomationRuntimeErrorCode(err) != test.code {
					t.Fatalf("error=%v code=%q", err, AutomationRuntimeErrorCode(err))
				}
				return
			}
			if err != nil || result["value"] != test.want {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
	if result, err := AutomationCalculateNumber(map[string]any{"operator": "+", "source": 1, "value": 2}, nil); err != nil || result["value"] != float64(3) {
		t.Fatalf("fallback operands=%#v err=%v", result, err)
	}
}

func TestAutomationCalculateDecimalOperatorsAndErrors(t *testing.T) {
	for _, operator := range []string{"add", "-", "*", "/", "percentage"} {
		result, err := AutomationCalculateDecimal(map[string]any{
			"operator": operator, "left": "10", "right": "2", "scale": 2,
		}, nil)
		if err != nil || result["value"] == "" || fmt.Sprint(result["scale"]) != "2" {
			t.Fatalf("operator=%s result=%#v err=%v", operator, result, err)
		}
	}
	if _, err := AutomationCalculateDecimal(map[string]any{"operator": "unknown", "left": "1", "right": "2"}, nil); AutomationRuntimeErrorCode(err) != "backend.decimal.operator_invalid" {
		t.Fatalf("operator error=%v", err)
	}
	if _, err := AutomationCalculateDecimal(map[string]any{"operator": "add", "left": "1", "right": "2", "precision": "invalid"}, nil); AutomationRuntimeErrorCode(err) != "backend.decimal.precision_invalid" {
		t.Fatalf("config error=%v", err)
	}
	if _, err := AutomationCalculateDecimal(map[string]any{"operator": "add", "left": "invalid", "right": "2"}, nil); err == nil {
		t.Fatal("invalid decimal accepted")
	}
	if AutomationRuntimeErrorCode(actionDecimalRuntimeError(errors.New("plain"))) != "backend.decimal.value_invalid" {
		t.Fatal("plain decimal error was not normalized")
	}
	if AutomationRuntimeErrorCode(actionDecimalRuntimeError(&recordmodel.RecordDecimalError{Code: "decimal.code"})) != "decimal.code" {
		t.Fatal("typed decimal error was not preserved")
	}
}

func TestAutomationCompareAssertAndValueHelpers(t *testing.T) {
	for _, test := range []struct {
		operator string
		want     bool
	}{
		{operator: "gt", want: true},
		{operator: "gte", want: true},
		{operator: "lt", want: false},
		{operator: "lte", want: false},
	} {
		got, err := AutomationCompareNumbers(2, 1, test.operator)
		if err != nil || got != test.want {
			t.Fatalf("compare %s=%v err=%v", test.operator, got, err)
		}
	}
	for _, input := range []struct {
		actual, expected any
		operator         string
	}{
		{"invalid", 1, "gt"},
		{1, "invalid", "gt"},
		{1, 1, "unknown"},
	} {
		if _, err := AutomationCompareNumbers(input.actual, input.expected, input.operator); err == nil {
			t.Fatalf("invalid comparison accepted: %#v", input)
		}
	}

	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	successes := []map[string]any{
		{"source": "same", "value": "same"},
		{"source": "a", "value": "b", "operator": "ne"},
		{"source": "a", "value": []any{"b", "a"}, "operator": "in"},
		{"source": tomorrow, "operator": "future_date"},
		{"source": "value", "operator": "not_empty"},
		{"source": " ", "operator": "empty"},
		{"source": 2, "value": 1, "operator": "gt"},
	}
	for _, step := range successes {
		if err := AutomationAssert(step, nil); err != nil {
			t.Fatalf("assert step=%#v err=%v", step, err)
		}
	}
	failures := []struct {
		step map[string]any
		code string
	}{
		{step: map[string]any{"source": "a", "value": []string{"b"}, "operator": "in"}, code: "backend.action.assert_failed"},
		{step: map[string]any{"source": "invalid", "operator": "future_date"}, code: "backend.action.assert_date_invalid"},
		{step: map[string]any{"source": nil, "operator": "not_empty", "error_code": "custom.failure"}, code: "custom.failure"},
		{step: map[string]any{"source": "invalid", "value": 1, "operator": "gt"}, code: "backend.action.calculate_number_invalid"},
		{step: map[string]any{"operator": "unknown"}, code: "backend.action.assert_operator_unsupported"},
	}
	for _, failure := range failures {
		if code := AutomationRuntimeErrorCode(AutomationAssert(failure.step, nil)); code != failure.code {
			t.Fatalf("assert step=%#v code=%q want=%q", failure.step, code, failure.code)
		}
	}

	if AutomationFirstNonNil(nil, "value") != "value" || AutomationFirstNonNil(nil) != nil {
		t.Fatal("first non-nil mismatch")
	}
	if value, ok := AutomationFirstPresent(map[string]any{"second": 2}, "first", "second"); !ok || value != 2 {
		t.Fatalf("first present=%#v %v", value, ok)
	}
	if _, ok := AutomationFirstPresent(nil, "missing"); ok {
		t.Fatal("missing value reported present")
	}
	if nestedValue(nil, "value") != "" || nestedValue(map[string]any{}, " ") != "" ||
		nestedValue(map[string]any{"value": "plain"}, "value.child") != "" ||
		nestedValue(map[string]any{}, "missing") != "" ||
		nestedValue(map[string]any{"nested": map[string]any{"value": "found"}}, "nested.value") != "found" {
		t.Fatal("nested value outcomes mismatch")
	}
	if AutomationCleanRuntimeValue(nil) != "" || AutomationCleanRuntimeValue(" value ") != "value" {
		t.Fatal("clean value mismatch")
	}
	if !AutomationRuntimeBool(true) || !AutomationRuntimeBool(" true ") || AutomationRuntimeBool(7) {
		t.Fatal("runtime bool mismatch")
	}
	if !actionRuntimeIsEmptyValue(nil) || !actionRuntimeIsEmptyValue(" ") || actionRuntimeIsEmptyValue(0) {
		t.Fatal("empty value mismatch")
	}
	if got := AutomationRuntimeStringList([]string{" a ", "a", ""}); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("string list=%#v", got)
	}
	if got := AutomationRuntimeStringList([]any{"a", 2}); !reflect.DeepEqual(got, []string{"a", "2"}) {
		t.Fatalf("any list=%#v", got)
	}
	if got := AutomationRuntimeStringList("a, b"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("csv list=%#v", got)
	}
	if got := AutomationRuntimeStringList(7); len(got) != 0 {
		t.Fatalf("unsupported list=%#v", got)
	}
}

func TestAutomationRuntimeErrorsPreserveCodesAndParams(t *testing.T) {
	err := AutomationRuntimeError("runtime.error", "field", "status", "orphan")
	if AutomationRuntimeErrorCode(err) != "runtime.error" {
		t.Fatalf("runtime error=%v", err)
	}
	if AutomationRuntimeErrorCode(errors.New("plain")) != "backend.internal" {
		t.Fatal("plain error did not normalize to internal")
	}
	if AutomationRuntimeErrorCode(nil) != "backend.internal" {
		t.Fatal("nil error did not normalize to internal")
	}
	if code := AutomationRuntimeErrorCode(AutomationRuntimeError("")); code != "backend.internal" {
		t.Fatalf("blank code=%q", code)
	}
}
