package validation

import (
	"github.com/domainry/domainry-foundation/apperror"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	"encoding/json"
	"fmt"

	definitioncontract "github.com/domainry/domainry-runtime/runtime/domain/definition/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"regexp"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

// ApplicationSchemaNormalizeFieldMutation owns pure field-definition normalization and
// evaluates compatibility against a caller-provided record snapshot.
func ApplicationSchemaNormalizeFieldMutation(request appschemamodel.ApplicationDefinitionUpsertRequest, objects []definitionmodel.ObjectSchema, allowedFieldTypes []string, existingRecords []recordmodel.Record, existingRecordCount int) (appschemamodel.ApplicationDefinitionUpsertRequest, error) {
	var field definitionmodel.FieldSchema
	if err := decodeClosedAuthoringJSON(request.Payload, &field); err != nil {
		return request, badRequest("backend.metadata.field_definition_invalid")
	}
	if !containsFieldType(allowedFieldTypes, field.Type) {
		return request, badRequest("backend.metadata.field_type_invalid", "field", field.Key, "type", field.Type, "allowed", strings.Join(allowedFieldTypes, ","))
	}
	if err := recordmodel.RecordValidateLocalizedFieldContract(definitionmodel.ObjectSchema{Key: request.ObjectKey, Fields: []definitionmodel.FieldSchema{field}}); err != nil {
		return request, badRequest("backend.metadata.localized_field_invalid", "field", field.Key, "detail", err.Error())
	}
	if err := recordvalidation.RecordValidateFieldUpgradeRule(request.ObjectKey, field); err != nil {
		return request, apperror.FromError(apperror.KindBadRequest, err)
	}
	if strings.TrimSpace(field.Type) == "currency" {
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return request, badRequest(metadataDecimalErrorCode(err), "field", field.Key)
		}
		if field.Config == nil {
			field.Config = map[string]any{}
		}
		field.Config["precision"] = config.Precision
		field.Config["scale"] = config.Scale
		field.Config["rounding_mode"] = config.RoundingMode
		field.Config["currency_code"] = config.CurrencyCode
		payload, _ := json.Marshal(field)
		request.Payload = payload
	}
	objectKey := strings.TrimSpace(request.ObjectKey)
	if objectKey == "" && field.Config != nil {
		objectKey = cleanFieldValue(field.Config["_definition_object_key"])
	}
	objectMap := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, object := range objects {
		objectMap[strings.TrimSpace(object.Key)] = object
	}
	object, exists := objectMap[objectKey]
	if !exists {
		return request, badRequest("backend.metadata.field_object_not_found", "object", objectKey)
	}
	if strings.TrimSpace(field.Type) == "relation" {
		if field.Config == nil {
			field.Config = map[string]any{}
		}
		target := fieldRelationTarget(field)
		if target == "" {
			return request, badRequest("backend.metadata.relation_target_required", "object", objectKey, "field", field.Key)
		}
		if _, found := objectMap[target]; !found && !definitioncontract.IsFoundationObjectKey(target) {
			return request, badRequest("backend.metadata.relation_target_not_found", "object", objectKey, "field", field.Key, "target", target)
		}
		cardinality := defaultFieldValue(field.Config["cardinality"], "many_to_one")
		if cardinality != "many_to_one" && cardinality != "one_to_one" {
			return request, badRequest("backend.metadata.relation_cardinality_invalid", "object", objectKey, "field", field.Key, "cardinality", cardinality)
		}
		onDelete := defaultFieldValue(field.Config["on_delete"], "restrict")
		switch onDelete {
		case "restrict", "set_null", "cascade":
		default:
			return request, badRequest("backend.metadata.relation_on_delete_invalid", "object", objectKey, "field", field.Key, "on_delete", onDelete)
		}
		if onDelete == "set_null" && field.Required {
			return request, badRequest("backend.metadata.relation_set_null_required", "object", objectKey, "field", field.Key)
		}
		inverseName := cleanFieldValue(field.Config["inverse_name"])
		if inverseName != "" && !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(inverseName) {
			return request, badRequest("backend.metadata.relation_inverse_name_invalid", "object", objectKey, "field", field.Key, "inverse_name", inverseName)
		}
		field.Validation.Target = target
		field.Config["target"] = target
		field.Config["cardinality"] = cardinality
		field.Config["on_delete"] = onDelete
		field.Config["inverse_name"] = inverseName
		if _, present := field.Config["indexed"]; !present {
			field.Config["indexed"] = true
		}
		field.Unique = cardinality == "one_to_one"
		if field.Unique {
			field.Config["indexed"] = true
		}
		// field was decoded from JSON above, so its interface values are JSON-safe.
		payload, _ := json.Marshal(field)
		request.Payload = payload
	}
	var existing *definitionmodel.FieldSchema
	for _, candidate := range object.Fields {
		if strings.TrimSpace(candidate.Key) == strings.TrimSpace(field.Key) {
			copy := candidate
			existing = &copy
			break
		}
	}
	if !field.Required || field.Default != nil || field.DefaultValue != nil || (existing != nil && existing.Required) || existingRecords == nil {
		return request, nil
	}
	for _, record := range existingRecords {
		if existing == nil || recordcontract.RecordIsEmptyValue(record.Data[field.Key]) {
			return request, badRequest("backend.metadata.required_field_default_required", "object", objectKey, "field", field.Key, "records", fmt.Sprint(existingRecordCount))
		}
	}
	return request, nil
}

func metadataDecimalErrorCode(err error) string {
	if decimalError, ok := err.(*recordmodel.RecordDecimalError); ok {
		return decimalError.Code
	}
	return "backend.decimal.value_invalid"
}

func containsFieldType(allowed []string, fieldType string) bool {
	fieldType = strings.TrimSpace(fieldType)
	for _, candidate := range allowed {
		if strings.TrimSpace(candidate) == fieldType {
			return true
		}
	}
	return false
}

func fieldRelationTarget(field definitionmodel.FieldSchema) string {
	if target := strings.TrimSpace(field.Validation.Target); target != "" {
		return target
	}
	for _, key := range []string{"target", "object_key", "target_object"} {
		if target := cleanFieldValue(field.Config[key]); target != "" {
			return target
		}
	}
	return ""
}

func defaultFieldValue(value any, fallback string) string {
	if result := cleanFieldValue(value); result != "" {
		return result
	}
	return fallback
}

func cleanFieldValue(value any) string {
	result := strings.TrimSpace(fmt.Sprint(value))
	if result == "<nil>" {
		return ""
	}
	return result
}
