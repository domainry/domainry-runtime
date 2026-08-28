package action

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
)

// ProjectBusinessHandlerOutput is the public-field boundary after trusted
// Handler execution. Handlers may read unmasked source values to enforce
// business rules; source-backed result fields are re-authorized for the caller
// before the receipt, audit event, or HTTP response is committed.
func ProjectBusinessHandlerOutput(_ context.Context, principal principalmodel.Principal, action definitionmodel.ActionSchema, output map[string]any, objectForKey func(string) (definitionmodel.ObjectSchema, bool)) (map[string]any, error) {
	projected := make(map[string]any, len(output))
	for key, value := range output {
		projected[key] = value
	}
	for _, resultField := range action.OutputFields {
		objectKey, fieldKey := strings.TrimSpace(resultField.SourceObjectKey), strings.TrimSpace(resultField.SourceFieldKey)
		if objectKey == "" && fieldKey == "" {
			continue
		}
		if objectKey == "" || fieldKey == "" || objectForKey == nil {
			return nil, apperror.New(apperror.KindInternal, "backend.action.output_field_contract_invalid", nil, map[string]string{"action": action.Key, "field": resultField.Key})
		}
		object, ok := objectForKey(objectKey)
		if !ok {
			return nil, apperror.New(apperror.KindInternal, "backend.action.output_field_contract_invalid", nil, map[string]string{"action": action.Key, "field": resultField.Key, "object": objectKey})
		}
		field, ok := actionOutputObjectField(object, fieldKey)
		if !ok {
			return nil, apperror.New(apperror.KindInternal, "backend.action.output_field_contract_invalid", nil, map[string]string{"action": action.Key, "field": resultField.Key, "object": objectKey, "source_field": fieldKey})
		}
		value, exists := projected[resultField.Key]
		if !exists {
			continue
		}
		if !recordpolicy.RecordCanReadObjectFieldForPrincipal(principal, object, field) {
			delete(projected, resultField.Key)
			continue
		}
		if recordpolicy.RecordFieldReadMaskedForPrincipal(principal, objectKey, fieldKey) {
			projected[resultField.Key] = actionMaskOutputValue(field, value, resultField.Repeated)
		}
	}
	return projected, nil
}

func actionOutputObjectField(object definitionmodel.ObjectSchema, fieldKey string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == fieldKey && strings.TrimSpace(field.DisabledAt) == "" {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func actionMaskOutputValue(field definitionmodel.FieldSchema, value any, repeated bool) any {
	if !repeated {
		return recordpolicy.RecordMaskFieldValue(field, value)
	}
	values, ok := value.([]any)
	if !ok {
		return recordpolicy.RecordMaskFieldValue(field, value)
	}
	masked := make([]any, len(values))
	for index := range values {
		masked[index] = recordpolicy.RecordMaskFieldValue(field, values[index])
	}
	return masked
}
