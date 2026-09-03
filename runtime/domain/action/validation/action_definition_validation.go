package validation

import (
	"fmt"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func ActionValidateDefinitionIssues(action definitionmodel.ActionSchema) []appschemamodel.ApplicationDefinitionValidationIssue {
	return ActionValidateDefinitionIssuesWithObjects(action, nil)
}

// ActionValidateDefinitionIssuesWithObjects validates only the published Action
// metadata contract. Runtime JSON execution config was retired in favor of
// generated source-owned Business Handlers.
func ActionValidateDefinitionIssuesWithObjects(action definitionmodel.ActionSchema, objects []definitionmodel.ObjectSchema) []appschemamodel.ApplicationDefinitionValidationIssue {
	issues := make([]appschemamodel.ApplicationDefinitionValidationIssue, 0)
	for _, identity := range []struct{ path, value string }{
		{"key", action.Key}, {"object_key", action.ObjectKey},
	} {
		if strings.TrimSpace(identity.value) == "" {
			issues = append(issues, actionDefinitionValidationIssue("backend.action.definition_invalid", identity.path, map[string]string{"field": identity.path}))
		}
	}
	if !actionSupportedKind(action.Kind) {
		issues = append(issues, actionDefinitionValidationIssue("backend.action.kind_invalid", "kind", map[string]string{
			"field": "kind", "allowed": strings.Join(definitionmodel.ActionKindValues(), ","), "actual": action.Kind, "kind": action.Kind,
		}))
	}
	for index, field := range action.PayloadFields {
		if strings.TrimSpace(field.Key) == "idempotency_key" {
			path := fmt.Sprintf("payload_fields[%d].key", index)
			issues = append(issues, actionDefinitionValidationIssue("backend.action.definition_invalid", path, map[string]string{"field": path, "reason": "reserved Runtime invocation metadata"}))
		}
	}
	issues = append(issues, actionValidatePermissionPolicy(action)...)
	issues = append(issues, actionValidateAssurancePolicy(action, objects)...)
	return issues
}

func actionValidateAssurancePolicy(action definitionmodel.ActionSchema, objects []definitionmodel.ObjectSchema) []appschemamodel.ApplicationDefinitionValidationIssue {
	policy := action.AssurancePolicy
	if policy == nil {
		return nil
	}
	issue := func(path string, params map[string]string) appschemamodel.ApplicationDefinitionValidationIssue {
		return actionDefinitionValidationIssue("backend.action.definition_invalid", "assurance_policy."+path, params)
	}
	issues := make([]appschemamodel.ApplicationDefinitionValidationIssue, 0)
	if len(policy.RequiredMethods) == 0 {
		issues = append(issues, issue("required_methods", map[string]string{"field": "assurance_policy.required_methods", "reason": "at least one assurance method is required"}))
	}
	allowed := actionDefinitionStringSet([]string{
		definitionmodel.ActionAssuranceNormalLogin,
		definitionmodel.ActionAssuranceRecentReauth,
		definitionmodel.ActionAssuranceOTP,
		definitionmodel.ActionAssuranceMakerChecker,
		definitionmodel.ActionAssuranceWorkflowApproval,
	})
	methods := map[string]bool{}
	for index, rawMethod := range policy.RequiredMethods {
		method := strings.TrimSpace(rawMethod)
		path := fmt.Sprintf("required_methods[%d]", index)
		if !allowed[method] {
			issues = append(issues, issue(path, map[string]string{"field": "assurance_policy." + path, "actual": rawMethod, "reason": "unknown assurance method"}))
		}
		if methods[method] {
			issues = append(issues, issue(path, map[string]string{"field": "assurance_policy." + path, "actual": rawMethod, "reason": "duplicate assurance method"}))
		}
		methods[method] = true
	}
	if methods[definitionmodel.ActionAssuranceRecentReauth] {
		if policy.RecentReauthMaxAgeSeconds < 1 || policy.RecentReauthMaxAgeSeconds > 86400 {
			issues = append(issues, issue("recent_reauth_max_age_seconds", map[string]string{"field": "assurance_policy.recent_reauth_max_age_seconds", "reason": "must be between 1 and 86400"}))
		}
	} else if policy.RecentReauthMaxAgeSeconds != 0 {
		issues = append(issues, issue("recent_reauth_max_age_seconds", map[string]string{"field": "assurance_policy.recent_reauth_max_age_seconds", "reason": "requires recent_reauth method"}))
	}

	objectFields, objectKnown := actionObjectFields(objects)[strings.TrimSpace(action.ObjectKey)]
	hasField := func(fieldKey string) bool {
		for _, field := range objectFields {
			if strings.TrimSpace(field.Key) == fieldKey {
				return true
			}
		}
		return false
	}
	validateField := func(path, rawField string) {
		fieldKey := strings.TrimSpace(rawField)
		if fieldKey == "" {
			issues = append(issues, issue(path, map[string]string{"field": "assurance_policy." + path, "reason": "field is required"}))
		} else if objectKnown && !hasField(fieldKey) {
			issues = append(issues, issue(path, map[string]string{"field": "assurance_policy." + path, "actual": fieldKey, "reason": "unknown object field"}))
		}
	}
	if methods[definitionmodel.ActionAssuranceWorkflowApproval] {
		validateField("approval_version_field", policy.ApprovalVersionField)
		validateField("approval_hash_field", policy.ApprovalHashField)
	} else if strings.TrimSpace(policy.ApprovalVersionField) != "" || strings.TrimSpace(policy.ApprovalHashField) != "" {
		issues = append(issues, issue("approval_version_field", map[string]string{"field": "assurance_policy.approval_version_field", "reason": "approval fields require workflow_approval method"}))
	}
	if methods[definitionmodel.ActionAssuranceMakerChecker] {
		validateField("maker_field", policy.MakerField)
	} else if strings.TrimSpace(policy.MakerField) != "" {
		issues = append(issues, issue("maker_field", map[string]string{"field": "assurance_policy.maker_field", "reason": "requires maker_checker method"}))
	}
	return issues
}

func actionDefinitionValidationIssue(code, fieldPath string, params map[string]string) appschemamodel.ApplicationDefinitionValidationIssue {
	contract := capabilitycontract.RuntimeAuthoringErrorContract(code, params)
	if strings.TrimSpace(fieldPath) == "" {
		fieldPath = contract.FieldPath
	}
	return appschemamodel.ApplicationDefinitionValidationIssue{FieldPath: fieldPath, ErrorCode: code, MessageKey: code, CapabilityKey: contract.CapabilityKey, ContractVersion: contract.ContractVersion, Params: params}
}

func actionSupportedKind(kind string) bool {
	kind = strings.TrimSpace(kind)
	for _, allowed := range definitionmodel.ActionKindValues() {
		if kind == allowed {
			return true
		}
	}
	return false
}

func actionDefinitionStringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func actionObjectFields(objects []definitionmodel.ObjectSchema) map[string][]definitionmodel.FieldSchema {
	result := make(map[string][]definitionmodel.FieldSchema, len(objects))
	for _, object := range objects {
		result[strings.TrimSpace(object.Key)] = object.Fields
	}
	return result
}

func actionRuleSetValueType(value string) bool {
	switch strings.TrimSpace(value) {
	case "boolean", "date", "datetime", "decimal", "duration", "integer", "text":
		return true
	default:
		return false
	}
}

func actionPreferenceValueType(value string) bool {
	switch strings.TrimSpace(value) {
	case "boolean", "date", "datetime", "decimal", "integer", "json", "number", "text":
		return true
	default:
		return false
	}
}
