package actionmodel

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func ActionAssuranceMethods(action definitionmodel.ActionSchema) []string {
	if action.AssurancePolicy == nil {
		return nil
	}
	return action.AssurancePolicy.RequiredMethods
}

func ActionApprovalRequired(action definitionmodel.ActionSchema) bool {
	for _, method := range ActionAssuranceMethods(action) {
		if method == definitionmodel.ActionAssuranceMakerChecker || method == definitionmodel.ActionAssuranceWorkflowApproval {
			return true
		}
	}
	return false
}

func ActionRiskLevel(action definitionmodel.ActionSchema) string {
	inferred := actionInferredRiskLevel(action)
	declared := strings.TrimSpace(action.RiskLevel)
	if ActionRiskLevelRank(declared) > ActionRiskLevelRank(inferred) {
		return declared
	}
	return inferred
}

func ActionRiskLevels() []string {
	return []string{"low", "medium", "high", "critical"}
}

func ActionRiskLevelRank(value string) int {
	switch strings.TrimSpace(value) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "critical":
		return 4
	default:
		return 0
	}
}

func actionInferredRiskLevel(action definitionmodel.ActionSchema) string {
	for _, method := range ActionAssuranceMethods(action) {
		if method == definitionmodel.ActionAssuranceMakerChecker || method == definitionmodel.ActionAssuranceWorkflowApproval {
			return "critical"
		}
	}
	for _, method := range ActionAssuranceMethods(action) {
		if method == definitionmodel.ActionAssuranceOTP || method == definitionmodel.ActionAssuranceRecentReauth {
			return "high"
		}
	}
	if ActionHasHighRiskEffect(action) {
		return "high"
	}
	if action.Kind == "record_update" || action.Kind == "record_operation" || action.Kind == "bulk_operation" {
		return "medium"
	}
	return "low"
}

func ActionHasHighRiskEffect(action definitionmodel.ActionSchema) bool {
	if action.Kind == "record_delete" {
		return true
	}
	return false
}

func ActionHasEnhancedAssurance(action definitionmodel.ActionSchema) bool {
	if ActionApprovalRequired(action) {
		return true
	}
	for _, method := range ActionAssuranceMethods(action) {
		switch method {
		case definitionmodel.ActionAssuranceRecentReauth, definitionmodel.ActionAssuranceOTP, definitionmodel.ActionAssuranceMakerChecker, definitionmodel.ActionAssuranceWorkflowApproval:
			return true
		}
	}
	return false
}
