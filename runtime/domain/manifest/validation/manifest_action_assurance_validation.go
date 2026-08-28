package validation

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (state *validationState) validateActionAssurancePolicy(path string, action definitionmodel.ActionSchema) {
	policy := action.AssurancePolicy
	if policy == nil {
		return
	}
	if len(policy.RequiredMethods) == 0 {
		state.add(path+".assurance_policy.required_methods", "must contain at least one method")
	}
	seen := map[string]bool{}
	methods := map[string]bool{}
	for index, method := range policy.RequiredMethods {
		method = strings.TrimSpace(method)
		valid := method == definitionmodel.ActionAssuranceNormalLogin || method == definitionmodel.ActionAssuranceRecentReauth || method == definitionmodel.ActionAssuranceOTP || method == definitionmodel.ActionAssuranceMakerChecker || method == definitionmodel.ActionAssuranceWorkflowApproval
		if !valid {
			state.add(path+".assurance_policy.required_methods", "unknown method at index %d: %q", index, method)
		}
		if seen[method] {
			state.add(path+".assurance_policy.required_methods", "duplicate method %q", method)
		}
		seen[method], methods[method] = true, true
	}
	if methods[definitionmodel.ActionAssuranceRecentReauth] {
		if policy.RecentReauthMaxAgeSeconds < 1 || policy.RecentReauthMaxAgeSeconds > 86400 {
			state.add(path+".assurance_policy.recent_reauth_max_age_seconds", "must be between 1 and 86400")
		}
	} else if policy.RecentReauthMaxAgeSeconds != 0 {
		state.add(path+".assurance_policy.recent_reauth_max_age_seconds", "requires recent_reauth method")
	}
	if methods[definitionmodel.ActionAssuranceWorkflowApproval] {
		objectFields := state.fields[strings.TrimSpace(action.ObjectKey)]
		for fieldPath, fieldKey := range map[string]string{"approval_version_field": policy.ApprovalVersionField, "approval_hash_field": policy.ApprovalHashField} {
			fieldKey = strings.TrimSpace(fieldKey)
			if fieldKey == "" {
				state.add(path+".assurance_policy."+fieldPath, "is required")
			} else if objectFields[fieldKey].Key == "" {
				state.add(path+".assurance_policy."+fieldPath, "unknown field %q", fieldKey)
			}
		}
	} else if strings.TrimSpace(policy.ApprovalVersionField) != "" || strings.TrimSpace(policy.ApprovalHashField) != "" {
		state.add(path+".assurance_policy", "approval fields require workflow_approval method")
	}
	if methods[definitionmodel.ActionAssuranceMakerChecker] {
		fieldKey := strings.TrimSpace(policy.MakerField)
		if fieldKey == "" {
			state.add(path+".assurance_policy.maker_field", "is required")
		} else if state.fields[strings.TrimSpace(action.ObjectKey)][fieldKey].Key == "" {
			state.add(path+".assurance_policy.maker_field", "unknown field %q", fieldKey)
		}
	} else if strings.TrimSpace(policy.MakerField) != "" {
		state.add(path+".assurance_policy.maker_field", "requires maker_checker method")
	}
}
