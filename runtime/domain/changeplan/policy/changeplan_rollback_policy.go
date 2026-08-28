package policy

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type ChangePlanBusinessRollbackPolicy struct {
	Version   string                                     `json:"version"`
	Resources []ChangePlanBusinessResourceRollbackPolicy `json:"resources"`
}

type ChangePlanBusinessResourceRollbackPolicy struct {
	ResourceTypes        []string `json:"resource_types"`
	Strategy             string   `json:"strategy"`
	PreservesRunEvidence bool     `json:"preserves_run_evidence"`
	SafetyChecks         []string `json:"safety_checks,omitempty"`
	Notes                string   `json:"notes"`
}

func ChangePlanBusinessMaintenanceRollbackPolicy(principal principalmodel.Principal) (ChangePlanBusinessRollbackPolicy, error) {
	if !principal.Known || !principal.HasPermission("workspace.admin") {
		return ChangePlanBusinessRollbackPolicy{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "auth.permission_denied"}
	}
	return ChangePlanBusinessRollbackPolicy{Version: "domain-rollback-policy-v1", Resources: []ChangePlanBusinessResourceRollbackPolicy{
		{ResourceTypes: []string{"object", "field", "validation", "view", "action", "workflow", "automation_rule", "scheduler", "role", "identity_profile_binding", "report", "dictionary", "connector", "surface"}, Strategy: "append_only_version", PreservesRunEvidence: true, SafetyChecks: []string{"reference_impact", "contract_compatibility"}, Notes: "Rollback creates a new metadata version and never rewrites history."},
		{ResourceTypes: []string{"workflow"}, Strategy: "new_published_version", PreservesRunEvidence: true, SafetyChecks: []string{"running_instance_snapshot"}, Notes: "Existing process instances remain bound to their original definition version."},
		{ResourceTypes: []string{"role", "permission", "menu", "data_scope", "field_permission"}, Strategy: "compensating_change_plan", PreservesRunEvidence: true, SafetyChecks: []string{"unique_administrator", "reference_impact"}, Notes: "Governance rollback is a reviewed compensating plan and cannot remove the last active administrator."},
		{ResourceTypes: []string{"scheduler_definition"}, Strategy: "compensating_change_plan", PreservesRunEvidence: true, SafetyChecks: []string{"pending_run_impact", "dead_letter_impact"}, Notes: "Existing runs and dead letters remain immutable execution evidence."},
		{ResourceTypes: []string{"connection_secret", "external_side_effect"}, Strategy: "manual_compensation", PreservesRunEvidence: true, SafetyChecks: []string{"secret_readiness", "external_reconciliation"}, Notes: "Expired secrets and completed external side effects are never restored automatically."},
	}}, nil
}
