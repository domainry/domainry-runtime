package validation

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// FieldUpgradeRuleInvalidCode is raised when a field declares an upgrade rule
// that the Runtime cannot honor for existing rows.
const FieldUpgradeRuleInvalidCode = "backend.metadata.field_upgrade_rule_invalid"

// RecordValidateFieldUpgradeRule checks the definition-time contract of a
// field upgrade rule: existing_rows must be backfill or exempt, backfill needs
// a value that normalizes for the field type, and exempt only makes sense for
// a required field because optional fields never reject an empty value.
func RecordValidateFieldUpgradeRule(objectKey string, field definitionmodel.FieldSchema) error {
	if field.Upgrade == nil {
		return nil
	}
	invalid := func(reason string) error {
		return validationError(FieldUpgradeRuleInvalidCode, "object", strings.TrimSpace(objectKey), "field", strings.TrimSpace(field.Key), "reason", reason)
	}
	switch strings.TrimSpace(field.Upgrade.ExistingRows) {
	case definitionmodel.FieldUpgradeBackfill:
		if field.Upgrade.BackfillValue == nil {
			return invalid("backfill_value_required")
		}
		normalized, err := RecordNormalizeFieldValue(field, field.Upgrade.BackfillValue)
		if err != nil {
			return invalid("backfill_value_invalid")
		}
		if RecordIsEmptyValue(normalized) {
			return invalid("backfill_value_empty")
		}
		return nil
	case definitionmodel.FieldUpgradeExempt:
		if !field.Required {
			return invalid("exempt_requires_required")
		}
		if field.Upgrade.BackfillValue != nil {
			return invalid("exempt_rejects_backfill_value")
		}
		return nil
	default:
		return invalid("existing_rows_invalid")
	}
}
