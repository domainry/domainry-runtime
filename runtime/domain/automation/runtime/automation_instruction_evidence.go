package runtime

import (
	"errors"
	"fmt"
	"strings"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

func AutomationIsSimulationSideEffect(instructionType string) bool {
	switch strings.TrimSpace(instructionType) {
	case "invoke_business_action", "emit_event", "start_workflow":
		return true
	default:
		return false
	}
}

func AutomationInstructionIdempotencyKey(rule automationmodel.AutomationRuleSchema, instruction automationmodel.AutomationInstructionSchema, record *recordmodel.Record) string {
	return automationExecutionFingerprint("automation.instruction.execute", rule.Key+"/"+instruction.Key, rule, record)
}

func AutomationRuleIdempotencyKey(rule automationmodel.AutomationRuleSchema, record *recordmodel.Record) string {
	return automationExecutionFingerprint("automation.rule.execute", rule.Key, rule, record)
}

func automationExecutionFingerprint(useCase, target string, rule automationmodel.AutomationRuleSchema, record *recordmodel.Record) string {
	recordID := "unknown"
	if record != nil {
		recordID = valueOrDefault(strings.TrimSpace(record.ID), "unknown")
	}
	key, _ := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: useCase, ResourceType: "automation", TargetID: target,
		Payload: map[string]any{"rule_key": rule.Key, "object_key": rule.ObjectKey, "record_id": recordID, "record_version": AutomationRecordVersion(record), "operation": rule.Trigger.Operation},
	})
	return key
}

func AutomationRecordVersion(record *recordmodel.Record) string {
	if record == nil {
		return "unknown"
	}
	return valueOrDefault(strings.TrimSpace(record.UpdatedAt), "unknown")
}

func AutomationInstructionResultMap(result automationmodel.AutomationInstructionResult) map[string]any {
	return map[string]any{
		"key": result.Key, "type": result.Type, "status": result.Status,
		"error_code": result.ErrorCode, "invocation_id": result.InvocationID,
		"data": automationNonNilMap(result.Data),
	}
}

func AutomationInstructionResult(value map[string]any) automationmodel.AutomationInstructionResult {
	return automationmodel.AutomationInstructionResult{
		Key: automationNormalizedValue(value["key"]), Type: automationNormalizedValue(value["type"]),
		Status: automationNormalizedValue(value["status"]), ErrorCode: automationNormalizedValue(value["error_code"]),
		InvocationID: automationNormalizedValue(value["invocation_id"]), Data: instructionMapFromAny(value["data"]),
	}
}

func automationNormalizedValue(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func automationNonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func instructionMapFromAny(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok && typed != nil {
		return typed
	}
	return map[string]any{}
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func automationError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: err}
}

func errorCode(err error) string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode()
	}
	return "backend.internal"
}
