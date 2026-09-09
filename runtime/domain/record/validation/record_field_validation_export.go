package validation

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

// RecordValidateFieldType exposes the single-field type check used by
// RecordValidateData so Action-owned structured payloads can reuse the exact
// scalar semantics without re-entering object-level validation.
func RecordValidateFieldType(field definitionmodel.FieldSchema, value any) error {
	return validateFieldType(field, value)
}

// RecordValidateFieldRules exposes the single-field rule check (length,
// pattern, options, numeric range) used by RecordValidateData.
func RecordValidateFieldRules(field definitionmodel.FieldSchema, value any) error {
	return validateFieldRules(field, value)
}
