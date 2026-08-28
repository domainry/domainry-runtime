package runtime

import (
	"errors"
	"reflect"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestAutomationSimulationSideEffectClassification(t *testing.T) {
	for _, instructionType := range []string{"invoke_business_action", " emit_event ", "start_workflow"} {
		if !AutomationIsSimulationSideEffect(instructionType) {
			t.Fatalf("instruction %q not classified as side effect", instructionType)
		}
	}
	for _, instructionType := range []string{"", "notify", "update_record", "derive_fields", "assert"} {
		if AutomationIsSimulationSideEffect(instructionType) {
			t.Fatalf("instruction %q classified as side effect", instructionType)
		}
	}
}

func TestAutomationInstructionResultRoundTripAndMissingValues(t *testing.T) {
	input := automationmodel.AutomationInstructionResult{
		Key: "step", Type: "emit_event", Status: "completed", ErrorCode: "code", InvocationID: "invocation",
		Data: map[string]any{"record_id": "record"},
	}
	encoded := AutomationInstructionResultMap(input)
	if decoded := AutomationInstructionResult(encoded); !reflect.DeepEqual(decoded, input) {
		t.Fatalf("decoded=%#v want=%#v", decoded, input)
	}
	input.Data = nil
	encoded = AutomationInstructionResultMap(input)
	if data, ok := encoded["data"].(map[string]any); !ok || data == nil || len(data) != 0 {
		t.Fatalf("nil data projection=%#v", encoded["data"])
	}
	missing := AutomationInstructionResult(nil)
	if missing.Key != "" || missing.Type != "" || missing.Status != "" || missing.ErrorCode != "" || missing.InvocationID != "" || missing.Data == nil {
		t.Fatalf("missing projection=%#v", missing)
	}
	malformed := AutomationInstructionResult(map[string]any{"key": " key ", "data": []any{"invalid"}})
	if malformed.Key != "key" || malformed.Data == nil || len(malformed.Data) != 0 {
		t.Fatalf("malformed projection=%#v", malformed)
	}
	var typedNil *int
	if projected := AutomationInstructionResult(map[string]any{"key": typedNil}); projected.Key != "" {
		t.Fatalf("typed nil projection=%#v", projected)
	}
	if got := instructionMapFromAny(map[string]any{"ok": true}); got["ok"] != true {
		t.Fatalf("typed data=%#v", got)
	}
	var nilMap map[string]any
	if got := instructionMapFromAny(nilMap); got == nil || len(got) != 0 {
		t.Fatalf("typed nil data=%#v", got)
	}
}

func TestAutomationErrorBuildsStableParametersAndCodes(t *testing.T) {
	cause := errors.New("failed")
	err := automationError(apperror.KindConflict, "automation.conflict", cause, " rule ", "sync", "", "ignored", "odd")
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindConflict || appErr.Code != "automation.conflict" || !errors.Is(err, cause) {
		t.Fatalf("error=%#v", err)
	}
	if !reflect.DeepEqual(appErr.Params, map[string]string{"rule": "sync"}) || errorCode(err) != "automation.conflict" {
		t.Fatalf("params=%#v code=%q", appErr.Params, errorCode(err))
	}
	withoutParams := automationError(apperror.KindInternal, "automation.internal", nil)
	if !errors.As(withoutParams, &appErr) || appErr.Params != nil {
		t.Fatalf("parameterless error=%#v", withoutParams)
	}
	if errorCode(errors.New("plain")) != "backend.internal" {
		t.Fatal("plain error code did not fall back")
	}
}

func TestAutomationValueAndRecordFallbacks(t *testing.T) {
	if valueOrDefault(" value ", "fallback") != "value" || valueOrDefault(" ", "fallback") != "fallback" {
		t.Fatal("value fallback mismatch")
	}
	if got := automationNonNilMap(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil map=%#v", got)
	}
}
