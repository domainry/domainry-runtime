package action

import (
	"errors"
	"strings"

	apperror "github.com/domainry/domainry-foundation/apperror"
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	actionvalidation "github.com/domainry/domainry-runtime/runtime/domain/action/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

// ActionNormalizePayload coordinates Action's payload contract with Record's
// generic normalization and validation policies.
func ActionNormalizePayload(action definitionmodel.ActionSchema, data map[string]any) (map[string]any, error) {
	payload := actionCloneMap(data)
	if payload == nil {
		payload = map[string]any{}
	}
	schema, hasContract := actionvalidation.ActionPayloadObject(action)
	extraKeys := actionPayloadExtraKeys(action)
	actionApplyPayloadDefaults(action, payload, schema, extraKeys, hasContract)
	if !hasContract {
		return payload, nil
	}
	declaredFields := make(map[string]definitionmodel.FieldSchema, len(schema.Fields))
	for _, field := range schema.Fields {
		declaredFields[field.Key] = field
	}
	declaredPayload, extraPayload := map[string]any{}, map[string]any{}
	for key, value := range payload {
		if _, ok := declaredFields[key]; ok {
			declaredPayload[key] = value
			continue
		}
		if extraKeys[key] {
			extraPayload[key] = value
			continue
		}
		return nil, actionPayloadBadRequest("backend.validation.unknown_field", nil, "field", key, "object", action.Key)
	}
	recordpolicy.RecordApplyFieldDefaults(schema, declaredPayload)
	normalized, err := recordvalidation.RecordNormalizeData(schema, declaredPayload, false)
	if err != nil {
		return nil, actionPayloadBadRequestFromError(err)
	}
	if err := recordvalidation.RecordValidateData(schema, normalized, false); err != nil {
		return nil, actionPayloadBadRequestFromError(err)
	}
	for key, value := range extraPayload {
		normalized[key] = value
	}
	return normalized, nil
}

func actionBindInvocationIdempotency(action definitionmodel.ActionSchema, data map[string]any, idempotencyKey string) map[string]any {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return data
	}
	for _, field := range action.PayloadFields {
		if strings.TrimSpace(field.Key) != "idempotency_key" {
			continue
		}
		bound := actionCloneMap(data)
		if bound == nil {
			bound = map[string]any{}
		}
		if _, exists := bound["idempotency_key"]; !exists {
			bound["idempotency_key"] = idempotencyKey
		}
		return bound
	}
	return data
}

func actionPayloadExtraKeys(action definitionmodel.ActionSchema) map[string]bool {
	out := map[string]bool{"idempotency_key": true, "expected_version": true, "expected_updated_at": true, "record_id": true, "request_ref": true, "approved": true, "approval_id": true, "approval_token": true}
	for _, key := range action.IdempotencyKeys {
		if key = strings.TrimSpace(key); key != "" {
			out[key] = true
		}
	}
	return out
}

func actionApplyPayloadDefaults(action definitionmodel.ActionSchema, payload map[string]any, schema definitionmodel.ObjectSchema, extraKeys map[string]bool, hasContract bool) {
	defaults := actionCloneMap(action.Defaults)
	if defaults == nil {
		defaults = map[string]any{}
	}
	declared := map[string]bool{}
	for _, field := range schema.Fields {
		declared[field.Key] = true
	}
	for key, value := range defaults {
		key = strings.TrimSpace(key)
		if key == "" || (hasContract && !declared[key] && !extraKeys[key]) {
			continue
		}
		if actionpolicy.ActionRecordValueEmpty(payload[key]) {
			payload[key] = value
		}
	}
}

func actionPayloadBadRequestFromError(err error) error {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return err
	}
	var coded interface {
		ErrorCode() string
		ErrorParams() map[string]string
	}
	if errors.As(err, &coded) && strings.TrimSpace(coded.ErrorCode()) != "" {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: coded.ErrorCode(), Params: coded.ErrorParams(), Err: err}
	}
	return actionPayloadBadRequest("backend.bad_request", err)
}

func actionPayloadBadRequest(code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code, Params: apperror.SanitizeParams(values), Err: err}
}

func actionCloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}
