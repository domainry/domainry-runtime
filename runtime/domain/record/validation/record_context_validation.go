package validation

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func RecordValidateRelationContextPolicies(object definitionmodel.ObjectSchema, data map[string]any, operation string) error {
	for _, validation := range object.Validations {
		if strings.TrimSpace(validation.Type) != "relation_context" || !RecordValidationBlocks(validation) || !RecordPolicyAppliesToOperation(validation, operation) {
			continue
		}
		fields := append([]string(nil), validation.Fields...)
		if len(fields) == 0 {
			fields = stringListAny(validation.Config["fields"])
		}
		for _, fieldKey := range fields {
			if fieldKey = strings.TrimSpace(fieldKey); fieldKey != "" && !RecordIsEmptyValue(data[fieldKey]) {
				return nil
			}
		}
		return stateMachineError(apperror.KindBadRequest, messageCode(validation.Message, "backend.policy.relation_context_required"), "fields", strings.Join(fields, ", "))
	}
	return nil
}

func RecordRelationField(object definitionmodel.ObjectSchema, fieldKey string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if field.Key == fieldKey && field.Type == "relation" {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func RecordRelationTarget(field definitionmodel.FieldSchema) string {
	target := RecordConfigString(field.Config["object_key"])
	if target == "" {
		target = RecordConfigString(field.Config["target"])
	}
	if target == "" {
		target = strings.TrimSpace(field.Validation.Target)
	}
	return target
}

func RecordValidationBlocks(validation definitionmodel.ValidationSchema) bool {
	return validationBlocks(validation)
}

func RecordPolicyAppliesToOperation(validation definitionmodel.ValidationSchema, operation string) bool {
	return policyAppliesToOperation(validation, operation)
}

func RecordConfigString(value any) string {
	return policyConfigString(value)
}

func RecordContainsText(values []string, want string) bool {
	return policyContainsText(values, want)
}
