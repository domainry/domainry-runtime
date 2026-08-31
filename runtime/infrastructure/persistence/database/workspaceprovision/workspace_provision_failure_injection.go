package workspaceprovision

import (
	"strings"

	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

const (
	FailureAfterWorkspace              = "after_workspace"
	FailureAfterTenantRegistry         = "after_tenant_registry"
	FailureAfterIdentityUser           = "after_identity_user"
	FailureAfterIdentityRole           = "after_identity_role"
	FailureAfterRoleAssignment         = "after_role_assignment"
	FailureAfterCredential             = "after_credential"
	FailureAfterWorkspaceConfiguration = "after_workspace_configuration"
	FailureAfterApplicationProjection  = "after_application_projection:"
	FailureAfterApplicationProjections = "after_application_projections"
	FailureAfterReceipt                = "after_receipt"
)

type FailureInjector interface {
	Inject(string) error
}

type exactAcceptanceFailureInjector struct{ target string }

func NewAcceptanceFailureInjector(target string) FailureInjector {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil
	}
	return exactAcceptanceFailureInjector{target: target}
}

func (injector exactAcceptanceFailureInjector) Inject(point string) error {
	if injector.target == point {
		return workspaceprovisionmodel.ErrAcceptanceFailure
	}
	return nil
}
