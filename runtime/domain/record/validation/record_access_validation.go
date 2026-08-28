package validation

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func RecordValidateImmutableAfterStatusPolicies(object definitionmodel.ObjectSchema, before, next map[string]any, operation string, principal principalmodel.Principal) error {
	if before == nil {
		return nil
	}
	for _, validation := range object.Validations {
		validationType := strings.TrimSpace(validation.Type)
		if validationType != "immutable_after_status" && validationType != "immutable_when_status" && validationType != "locked_fields" {
			continue
		}
		if !validationBlocks(validation) || !policyAppliesToOperation(validation, operation) || !RecordPolicyConditionMatches(validation, next) {
			continue
		}
		statusField := recordValueOrDefault(policyConfigString(validation.Config["status_field"]), "status")
		status := policyConfigString(before[statusField])
		statuses := firstConfiguredStringList(validation.Config, "statuses", "locked_statuses", "when_statuses")
		if len(statuses) == 0 || !policyContainsText(statuses, status) {
			continue
		}
		fields := append([]string(nil), validation.Fields...)
		if len(fields) == 0 {
			fields = firstConfiguredStringList(validation.Config, "fields", "field_keys")
		}
		if len(fields) == 0 && strings.TrimSpace(validation.FieldKey) != "" {
			fields = []string{strings.TrimSpace(validation.FieldKey)}
		}
		for _, fieldKey := range fields {
			fieldKey = strings.TrimSpace(fieldKey)
			if fieldKey != "" && !RecordPolicyValuesEqual(before[fieldKey], next[fieldKey]) {
				return stateMachineError(apperror.KindForbidden, messageCode(validation.Message, "backend.policy.immutable_after_status"), "policy", validation.Key, "field", fieldKey, "status", status)
			}
		}
	}
	return nil
}

func RecordValidateThresholdPermissionPolicies(object definitionmodel.ObjectSchema, before, next map[string]any, operation string, principal principalmodel.Principal) error {
	for _, validation := range object.Validations {
		validationType := strings.TrimSpace(validation.Type)
		if validationType != "threshold_permission" && validationType != "permission_threshold" {
			continue
		}
		if !validationBlocks(validation) || !policyAppliesToOperation(validation, operation) || !RecordPolicyConditionMatches(validation, next) {
			continue
		}
		fieldKey := strings.TrimSpace(validation.FieldKey)
		if fieldKey == "" {
			fieldKey = policyConfigString(validation.Config["field"])
		}
		if fieldKey == "" && len(validation.Fields) > 0 {
			fieldKey = strings.TrimSpace(validation.Fields[0])
		}
		if fieldKey == "" || RecordIsEmptyValue(next[fieldKey]) {
			continue
		}
		if RecordThresholdPermissionChangedOnly(validation) && before != nil && policyConfigString(before[fieldKey]) == policyConfigString(next[fieldKey]) {
			continue
		}
		value, ok := policyNumericAny(next[fieldKey])
		if !ok || !RecordThresholdPermissionExceeded(validation, value, next) {
			continue
		}
		permission := RecordThresholdPermissionKey(validation)
		if permission == "" {
			return stateMachineError(apperror.KindBadRequest, "backend.policy.permission_required", "policy", validation.Key)
		}
		if !principal.HasPermission(permission) {
			return stateMachineError(apperror.KindForbidden, messageCode(validation.Message, "backend.policy.threshold_permission_required"), "permission", permission, "field", fieldKey)
		}
	}
	return nil
}

func policyAppliesToOperation(validation definitionmodel.ValidationSchema, operation string) bool {
	operations := stringListAny(validation.Config["operations"])
	return len(operations) == 0 || policyContainsText(operations, operation)
}

func firstConfiguredStringList(config map[string]any, keys ...string) []string {
	for _, key := range keys {
		if values := stringListAny(config[key]); len(values) > 0 {
			return values
		}
	}
	return nil
}
