package validation

import (
	"regexp"
	"strings"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

var actionPermissionKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+$`)

// ActionPermissionKeyWellFormed reports whether a requires_permission key is
// shaped as at least two dot-separated lower_snake_case segments.
func ActionPermissionKeyWellFormed(permission string) bool {
	return actionPermissionKeyPattern.MatchString(strings.TrimSpace(permission))
}

func actionValidatePermissionPolicy(action definitionmodel.ActionSchema) []metadatamodel.MetadataDefinitionValidationIssue {
	permission := strings.TrimSpace(action.RequiresPermission)
	if permission == "" {
		return nil
	}
	issue := func(code string, params map[string]string) metadatamodel.MetadataDefinitionValidationIssue {
		return actionDefinitionValidationIssue(code, "requires_permission", params)
	}
	if !actionPermissionKeyPattern.MatchString(permission) {
		return []metadatamodel.MetadataDefinitionValidationIssue{issue("backend.action.permission_format_invalid", map[string]string{
			"field": "requires_permission", "actual": permission, "expected": "<object_key>.<permission_name>",
		})}
	}
	objectKey := strings.TrimSpace(action.ObjectKey)
	if objectKey != "" && !strings.HasPrefix(permission, objectKey+".") {
		return []metadatamodel.MetadataDefinitionValidationIssue{issue("backend.action.permission_object_mismatch", map[string]string{
			"field": "requires_permission", "actual": permission, "object_key": objectKey,
		})}
	}
	issues := make([]metadatamodel.MetadataDefinitionValidationIssue, 0, 2)
	declaredRisk := strings.TrimSpace(action.RiskLevel)
	if declaredRisk != "" && actionmodel.ActionRiskLevelRank(declaredRisk) == 0 {
		issues = append(issues, actionDefinitionValidationIssue("backend.action.risk_level_invalid", "risk_level", map[string]string{
			"field": "risk_level", "actual": declaredRisk, "allowed": strings.Join(actionmodel.ActionRiskLevels(), ","),
		}))
	}
	inferredAction := action
	inferredAction.RiskLevel = ""
	inferredRisk := actionmodel.ActionRiskLevel(inferredAction)
	if declaredRisk != "" && actionmodel.ActionRiskLevelRank(declaredRisk) > 0 && actionmodel.ActionRiskLevelRank(declaredRisk) < actionmodel.ActionRiskLevelRank(inferredRisk) {
		issues = append(issues, actionDefinitionValidationIssue("backend.action.risk_level_understated", "risk_level", map[string]string{
			"field": "risk_level", "actual": declaredRisk, "minimum": inferredRisk,
		}))
	}
	riskLevel := actionmodel.ActionRiskLevel(action)
	if riskLevel == "high" || riskLevel == "critical" {
		if actionmodel.ActionAuthorizationStrategy(action) == actionmodel.ActionAuthorizationInheritObjectPermission {
			issues = append(issues, issue("backend.action.high_risk_permission_must_be_dedicated", map[string]string{
				"field": "requires_permission", "actual": permission, "object_key": objectKey, "risk_level": riskLevel,
			}))
		}
	}
	if riskLevel == "high" || riskLevel == "critical" {
		if !actionmodel.ActionHasEnhancedAssurance(action) {
			issues = append(issues, actionDefinitionValidationIssue("backend.action.high_risk_assurance_required", "assurance_policy.required_methods", map[string]string{
				"field": "assurance_policy.required_methods", "action": strings.TrimSpace(action.Key),
			}))
		}
	}
	return issues
}
