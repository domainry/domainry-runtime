package deployment

import (
	"context"
	"sort"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	deploymentvalidation "github.com/domainry/domainry-runtime/runtime/domain/deployment/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	apperror "github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func (s *DeploymentFrontendCapabilityApplicationService) ValidateManifest(ctx context.Context, manifest deploymentmodel.FrontendCapabilityManifest, principal principalmodel.Principal) (deploymentmodel.FrontendCapabilityManifestValidationResult, error) {
	if err := deploymentAuthorizeQuery(principal); err != nil {
		return deploymentmodel.FrontendCapabilityManifestValidationResult{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return deploymentmodel.FrontendCapabilityManifestValidationResult{}, applicationError(apperror.KindForbidden, "auth.permission_denied", nil)
	}
	validation := s.validateFrontendCapabilityManifestUsage(manifest)
	s.validateBusinessBindings(ctx, &validation)
	return validation, nil
}

func (s *DeploymentFrontendCapabilityApplicationService) validateFrontendCapabilityManifestUsage(manifest deploymentmodel.FrontendCapabilityManifest) deploymentmodel.FrontendCapabilityManifestValidationResult {
	return deploymentvalidation.ValidateFrontendCapabilityManifest(manifest, s.contractVersion, s.capabilities)
}

func (s *DeploymentFrontendCapabilityApplicationService) validateBusinessBindings(ctx context.Context, validation *deploymentmodel.FrontendCapabilityManifestValidationResult) {
	if s == nil || s.businessBindings == nil || validation == nil {
		return
	}
	bindings := s.businessBindings(ctx)
	known := map[string]map[string]bool{
		"business_objects": bindings.Objects, "view_keys": bindings.Views,
		"implemented_actions": bindings.Actions, "report_keys": bindings.Reports, "field_keys": bindings.Fields,
	}
	for entryIndex, entry := range validation.NormalizedManifest.Entries {
		bindings := map[string][]string{
			"business_objects": entry.BusinessObjects, "view_keys": entry.ViewKeys,
			"implemented_actions": entry.ImplementedActions, "report_keys": entry.ReportKeys, "field_keys": entry.FieldKeys,
		}
		for kind, values := range bindings {
			for valueIndex, value := range values {
				if known[kind][value] {
					continue
				}
				validation.Valid = false
				validation.Issues = append(validation.Issues, deploymentmodel.FrontendCapabilityUsageValidationIssue{
					Severity: "error", EntrySupportKey: entry.SupportKey,
					FieldPath: "entries[" + stringIndex(entryIndex) + "]." + kind + "[" + stringIndex(valueIndex) + "]",
					ErrorCode: "backend.frontend.business_binding_unknown", MessageKey: "backend.frontend.business_binding_unknown",
					ContractVersion: s.contractVersion, Params: map[string]string{"kind": kind, "actual": value},
				})
			}
		}
	}
	sort.Slice(validation.Issues, func(i, j int) bool {
		return validation.Issues[i].FieldPath+"\x00"+validation.Issues[i].ErrorCode < validation.Issues[j].FieldPath+"\x00"+validation.Issues[j].ErrorCode
	})
}
