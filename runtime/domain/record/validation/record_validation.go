package validation

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

import (
	"fmt"
	"strings"
)

func RecordValidateData(object definitionmodel.ObjectSchema, data map[string]any, partial bool) error {
	return RecordValidateDataWithPrev(object, data, nil, partial)
}

// RecordValidateDataWithPrev validates data for an update, using prev as the current stored record
// values. This is required for state_machine validations.
func RecordValidateDataWithPrev(object definitionmodel.ObjectSchema, data map[string]any, prev map[string]any, partial bool) error {
	// hasPrev distinguishes an update from a create: exempt upgrade rules only
	// relax the required check for rows that already existed without the field.
	hasPrev := prev != nil
	if data == nil {
		data = map[string]any{}
	}
	if prev == nil {
		prev = map[string]any{}
	}
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[field.Key] = field
	}
	for key, value := range data {
		field, ok := fields[key]
		if !ok {
			return validationError("backend.validation.unknown_field", "field", key, "object", object.Key)
		}
		if err := validateFieldType(field, value); err != nil {
			return err
		}
		if err := validateFieldRules(field, value); err != nil {
			return err
		}
	}
	if partial {
		return nil
	}
	for _, field := range object.Fields {
		if field.Required && RecordIsEmptyValue(data[field.Key]) {
			if hasPrev && field.FieldExemptsExistingRows() && RecordIsEmptyValue(prev[field.Key]) {
				continue
			}
			return validationError("backend.validation.required", "field", field.Key)
		}
	}
	if err := validateObjectRules(object, data, prev); err != nil {
		return err
	}
	return nil
}

func validateObjectRules(object definitionmodel.ObjectSchema, data map[string]any, prev map[string]any) error {
	for _, validation := range object.Validations {
		requiredCode := validationCode(validation.Message, "backend.validation.required")
		invalidCode := validationCode(validation.Message, "backend.validation.invalid")
		validationType := strings.TrimSpace(validation.Type)
		// conditional_required owns its when_field/value contract below. Applying
		// the generic gate first duplicates the same predicate and makes its
		// fallback branches unreachable.
		if validationType != "conditional_required" && !validationConditionApplies(validation, data) {
			continue
		}
		switch validationType {
		case "required_fields":
			if !isBlockingValidation(validation) {
				continue
			}
			for _, requiredField := range validation.Fields {
				requiredField = strings.TrimSpace(requiredField)
				if requiredField != "" && RecordIsEmptyValue(data[requiredField]) {
					return validationError(requiredCode, "validation", validation.Key, "field", requiredField)
				}
			}
		case "conditional_required":
			if !isBlockingValidation(validation) {
				continue
			}
			if triggerField, ok := stringConfig(validation.Config, "when_field"); ok {
				requiredFields := stringListAny(validation.Config["required_fields"])
				if len(requiredFields) == 0 {
					requiredFields = validation.Fields
				}
				triggerValue := strings.TrimSpace(fmt.Sprint(data[triggerField]))
				triggerValues := stringListAny(validation.Config["when_in"])
				if len(triggerValues) == 0 {
					triggerValues = stringListAny(validation.Config["values"])
				}
				if len(triggerValues) == 0 {
					if expected, ok := stringConfig(validation.Config, "value"); ok {
						triggerValues = []string{expected}
					}
				}
				if len(triggerValues) == 0 || containsString(triggerValues, triggerValue) {
					for _, requiredField := range requiredFields {
						requiredField = strings.TrimSpace(requiredField)
						if requiredField != "" && RecordIsEmptyValue(data[requiredField]) {
							return validationError(requiredCode, "validation", validation.Key, "field", requiredField)
						}
					}
				}
				continue
			}
			if len(validation.Fields) < 2 {
				continue
			}
			triggerField := strings.TrimSpace(validation.Fields[0])
			requiredField := strings.TrimSpace(validation.Fields[1])
			if triggerField == "" || requiredField == "" {
				continue
			}
			triggerValue := strings.ToLower(strings.TrimSpace(fmt.Sprint(data[triggerField])))
			if triggerValue == "delivery" && RecordIsEmptyValue(data[requiredField]) {
				return validationError(requiredCode, "validation", validation.Key, "field", requiredField)
			}
		case "required_when":
			if !isBlockingValidation(validation) {
				continue
			}
			fieldKey := strings.TrimSpace(validation.FieldKey)
			if fieldKey == "" {
				continue
			}
			if !validationValueMatches(data[fieldKey], validation.Config) {
				continue
			}
			for _, requiredField := range validation.Fields {
				requiredField = strings.TrimSpace(requiredField)
				if requiredField != "" && RecordIsEmptyValue(data[requiredField]) {
					return validationError(requiredCode, "validation", validation.Key, "field", requiredField)
				}
			}
		case "cross_field":
			if !isBlockingValidation(validation) {
				continue
			}
			if len(validation.Fields) < 2 {
				continue
			}
			left, leftOK := numericAny(data[validation.Fields[0]])
			right, rightOK := numericAny(data[validation.Fields[1]])
			if leftOK && rightOK && right > left {
				return validationError(invalidCode, "validation", validation.Key, "field", validation.Fields[1])
			}
		case "fields_not_equal", "not_equal":
			if !isBlockingValidation(validation) {
				continue
			}
			if len(validation.Fields) < 2 {
				continue
			}
			leftField := strings.TrimSpace(validation.Fields[0])
			rightField := strings.TrimSpace(validation.Fields[1])
			if leftField == "" || rightField == "" || RecordIsEmptyValue(data[leftField]) || RecordIsEmptyValue(data[rightField]) {
				continue
			}
			if strings.TrimSpace(fmt.Sprint(data[leftField])) == strings.TrimSpace(fmt.Sprint(data[rightField])) {
				return validationError(invalidCode, "validation", validation.Key, "field", rightField)
			}
		case "numeric_min":
			if !isBlockingValidation(validation) {
				continue
			}
			fieldKey := strings.TrimSpace(validation.FieldKey)
			if fieldKey == "" {
				continue
			}
			minimum := 0.0
			if value, ok := numericAny(validation.Config["min"]); ok {
				minimum = value
			}
			if value, ok := numericAny(data[fieldKey]); ok && value < minimum {
				return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "min", fmt.Sprint(minimum))
			}
		case "range":
			if !isBlockingValidation(validation) {
				continue
			}
			fieldKey := strings.TrimSpace(validation.FieldKey)
			if fieldKey == "" {
				continue
			}
			rawValue := data[fieldKey]
			value, numericOK := numericAny(rawValue)
			actualDate, dateOK := validationDateOnly(rawValue)
			if !numericOK && !dateOK {
				continue
			}
			if minimum, ok := numericAny(validation.Config["exclusive_min"]); numericOK && ok && value <= minimum {
				return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "min", fmt.Sprint(minimum))
			}
			if minimum, ok := numericAny(validation.Config["min"]); numericOK && ok && value < minimum {
				return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "min", fmt.Sprint(minimum))
			}
			if minField, ok := stringConfig(validation.Config, "min_field"); ok {
				if minimum, ok := numericAny(data[minField]); numericOK && ok && value < minimum {
					return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "min_field", minField)
				}
				if minimumDate, minimumOK := validationDateOnly(data[minField]); dateOK && minimumOK && actualDate.Before(minimumDate) {
					return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "min_field", minField)
				}
			}
			if maximum, ok := numericAny(validation.Config["max"]); numericOK && ok && value > maximum {
				return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "max", fmt.Sprint(maximum))
			}
			if maxField, ok := stringConfig(validation.Config, "exclusive_max_field"); ok {
				if maximum, ok := numericAny(data[maxField]); numericOK && ok && value >= maximum {
					return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "max_field", maxField)
				}
				if maximumDate, maximumOK := validationDateOnly(data[maxField]); dateOK && maximumOK && !actualDate.Before(maximumDate) {
					return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "max_field", maxField)
				}
			}
			if maxField, ok := stringConfig(validation.Config, "max_field"); ok {
				if maximum, ok := numericAny(data[maxField]); numericOK && ok && value > maximum {
					return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "max_field", maxField)
				}
				if maximumDate, maximumOK := validationDateOnly(data[maxField]); dateOK && maximumOK && actualDate.After(maximumDate) {
					return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "max_field", maxField)
				}
			}
		case "boolean_true", "truthy":
			if !isBlockingValidation(validation) {
				continue
			}
			fieldKey := strings.TrimSpace(validation.FieldKey)
			if fieldKey == "" {
				continue
			}
			if value, ok := boolAny(data[fieldKey]); !ok || !value {
				return validationError(invalidCode, "validation", validation.Key, "field", fieldKey)
			}
		case "enum_array":
			if !isBlockingValidation(validation) {
				continue
			}
			fieldKey := strings.TrimSpace(validation.FieldKey)
			if fieldKey == "" {
				continue
			}
			field, ok := findFieldSchema(object, fieldKey)
			if !ok {
				continue
			}
			values, err := jsonStringArray(data[fieldKey])
			if err != nil {
				return err
			}
			if minItems, ok := numericAny(validation.Config["min_items"]); ok && len(values) < int(minItems) {
				return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "min_items", fmt.Sprint(int(minItems)))
			}
			allowed := stringListAny(validation.Config["options"])
			if len(allowed) == 0 {
				allowed = append(allowed, field.Validation.Options...)
			}
			if len(allowed) == 0 {
				allowed = stringListConfig(field.Config, "options")
			}
			if len(allowed) > 0 {
				for _, value := range values {
					if !containsOption(allowed, value) {
						return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "value", value)
					}
				}
			}
		case "business_hours_segments":
			if !isBlockingValidation(validation) {
				continue
			}
			fieldKey := strings.TrimSpace(validation.FieldKey)
			if fieldKey == "" && len(validation.Fields) > 0 {
				fieldKey = strings.TrimSpace(validation.Fields[0])
			}
			if fieldKey == "" {
				continue
			}
			if err := validateBusinessHourSegmentsValue(data[fieldKey]); err != nil {
				if validation.Message != "" {
					return validationError(invalidCode, "validation", validation.Key, "field", fieldKey)
				}
				return err
			}
		case "special_dates_schedule":
			if !isBlockingValidation(validation) {
				continue
			}
			fieldKey := strings.TrimSpace(validation.FieldKey)
			if fieldKey == "" && len(validation.Fields) > 0 {
				fieldKey = strings.TrimSpace(validation.Fields[0])
			}
			if fieldKey == "" {
				continue
			}
			if err := validateSpecialDatesScheduleValue(data[fieldKey]); err != nil {
				if validation.Message != "" {
					return validationError(invalidCode, "validation", validation.Key, "field", fieldKey)
				}
				return err
			}
		case "state_transition":
			// Validates that a field transition is allowed by the state machine definition.
			if !isBlockingValidation(validation) {
				continue
			}
			fieldKey := strings.TrimSpace(validation.FieldKey)
			if fieldKey == "" {
				continue
			}
			allowedFrom := stringListAny(validation.Config["allowed_from"])
			if len(allowedFrom) == 0 {
				continue
			}
			current := strings.TrimSpace(fmt.Sprint(data[fieldKey]))
			if current != "" && !containsString(allowedFrom, current) {
				return validationError(invalidCode, "validation", validation.Key, "field", fieldKey, "state", current)
			}
		case "relation_exists":
			// Validates that a referenced relation record key field is non-empty.
			if !isBlockingValidation(validation) {
				continue
			}
			fieldKey := strings.TrimSpace(validation.FieldKey)
			if fieldKey == "" {
				continue
			}
			if RecordIsEmptyValue(data[fieldKey]) {
				return validationError(requiredCode, "validation", validation.Key, "field", fieldKey)
			}
		case "capability_available":
			// Validates that the capability (e.g. promotion_active, inventory_sufficient) is met.
			// Evaluated as a runtime check by the service layer via ValidateCapabilityRule;
			// here we confirm the config is well-formed.
			_, ok := stringConfig(validation.Config, "capability")
			if !ok {
				return validationError("backend.validation.capability_missing_key", "validation", validation.Key)
			}
			// Actual availability check is deferred to service layer.
		case "state_machine":
			// Validates that a state transition is allowed by the configured state machine.
			// Config: { state_field: string, transitions: { from: string, to: []string }[] }
			if len(validation.Config) == 0 {
				continue
			}
			stateField, ok := stringConfig(validation.Config, "state_field")
			if !ok {
				stateField = strings.TrimSpace(validation.FieldKey)
			}
			if stateField == "" {
				return validationError("backend.validation.state_machine_missing_field", "validation", validation.Key)
			}
			currentState := stateMachineValue(data, stateField)
			newState := stateMachineValue(prev, stateField)
			// Only validate when the state field is changing.
			if currentState == "" || newState == "" || currentState == newState {
				continue
			}
			transitionsRaw, _ := validation.Config["transitions"]
			if !stateMachineAllowsTransition(transitionsRaw, newState, currentState) {
				code := validationCode(validation.Message, "backend.transition.invalid_transition")
				return validationError(code, "field", stateField, "from", newState, "to", currentState)
			}
		case "composite_unique":
			// Unique combination across multiple fields is validated at the service layer.
			// Domain layer only ensures the fields listed are non-empty.
			if !isBlockingValidation(validation) {
				continue
			}
			for _, fieldKey := range validation.Fields {
				fieldKey = strings.TrimSpace(fieldKey)
				if fieldKey != "" && RecordIsEmptyValue(data[fieldKey]) {
					return validationError(requiredCode, "validation", validation.Key, "field", fieldKey)
				}
			}
		case ConditionalUniqueValidationType:
			if !isBlockingValidation(validation) {
				continue
			}
			policies, err := RecordConditionalUniquePolicies(definitionmodel.ObjectSchema{
				Key: object.Key, Fields: object.Fields, Validations: []definitionmodel.ValidationSchema{validation},
			})
			if err != nil {
				return validationError("backend.validation.conditional_unique_invalid", "validation", validation.Key)
			}
			// The parser receives exactly this conditional_unique validation; a
			// successful parse therefore yields exactly one policy.
			if RecordConditionalUniqueApplies(policies[0], data) {
				for _, fieldKey := range policies[0].Fields {
					if RecordIsEmptyValue(data[fieldKey]) {
						return validationError(requiredCode, "validation", validation.Key, "field", fieldKey)
					}
				}
			}
		}
	}
	return nil
}
