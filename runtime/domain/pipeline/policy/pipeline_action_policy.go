package policy

import (
	"encoding/json"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func PipelineIsRuntimeAction(action definitionmodel.ActionSchema) bool {
	return action.ObjectKey == "pipeline_item" && (action.Key == "pipeline_item.advance" || action.Key == "pipeline_item.reopen")
}

func PipelineValidateExpectedVersion(recordData, data map[string]any) error {
	expectedRaw, ok := firstPresent(data, "expected_version", "version")
	if !ok || recordcontract.RecordIsEmptyValue(expectedRaw) {
		return nil
	}
	expected, ok := PipelineIntValue(expectedRaw)
	if !ok {
		return pipelinePolicyError(apperror.KindBadRequest, "backend.pipeline.invalid_expected_version", nil)
	}
	current, ok := PipelineIntValue(recordData["version"])
	if !ok {
		current = 1
	}
	if expected != current {
		return pipelinePolicyError(apperror.KindConflict, "backend.pipeline.version_conflict", nil)
	}
	return nil
}

func PipelineValidateActionExpectedVersion(action definitionmodel.ActionSchema, object definitionmodel.ObjectSchema, recordData, data map[string]any) error {
	if !PipelineActionRequiresExpectedVersion(action) || !PipelineObjectHasField(object, "version") {
		return nil
	}
	expectedRaw, ok := firstPresent(data, "expected_version", "expectedVersion")
	if !ok || recordcontract.RecordIsEmptyValue(expectedRaw) {
		return pipelinePolicyError(apperror.KindBadRequest, "backend.action.expected_version_required", nil)
	}
	expected, ok := PipelineIntValue(expectedRaw)
	if !ok {
		return pipelinePolicyError(apperror.KindBadRequest, "backend.action.invalid_expected_version", nil)
	}
	current, ok := PipelineIntValue(recordData["version"])
	if !ok {
		current = 1
	}
	if expected != current {
		return pipelinePolicyError(apperror.KindConflict, "backend.action.version_conflict", nil)
	}
	return nil
}

func PipelineActionRequiresExpectedVersion(action definitionmodel.ActionSchema) bool {
	return action.OptimisticConcurrency || strings.TrimSpace(action.ConcurrencyField) != ""
}

func PipelineObjectHasField(object definitionmodel.ObjectSchema, fieldKey string) bool {
	for _, field := range object.Fields {
		if field.Key == fieldKey {
			return true
		}
	}
	return false
}

func firstPresent(data map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func PipelineIntValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func pipelinePolicyError(kind apperror.ErrorKind, code string, err error) error {
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}
