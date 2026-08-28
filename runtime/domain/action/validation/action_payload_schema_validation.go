package validation

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// ActionPayloadObject projects an Action-owned payload contract into the
// generic definition value object. Cross-owner Record validation is performed
// by the Application use case.
func ActionPayloadObject(action definitionmodel.ActionSchema) (definitionmodel.ObjectSchema, bool) {
	fields := []definitionmodel.FieldSchema{}
	hasContract := action.PayloadFields != nil
	for _, field := range action.PayloadFields {
		if payloadField := ActionTypedPayloadField(field); payloadField.Key != "" {
			fields = append(fields, payloadField)
		}
	}
	return definitionmodel.ObjectSchema{Key: action.Key + ":payload", Name: action.Label + " Payload", Fields: fields}, hasContract
}

func ActionTypedPayloadField(field definitionmodel.ActionPayloadField) definitionmodel.FieldSchema {
	key := strings.TrimSpace(field.Key)
	if key == "" {
		return definitionmodel.FieldSchema{}
	}
	fieldType := strings.TrimSpace(field.Type)
	if fieldType == "" {
		fieldType = "text"
	}
	name := strings.TrimSpace(field.Name)
	if name == "" {
		name = key
	}
	return definitionmodel.FieldSchema{Key: key, Name: name, Type: fieldType, Validation: definitionmodel.FieldValidation{Options: append([]string(nil), field.Options...)}, Required: field.Required, DefaultValue: field.DefaultValue}
}
