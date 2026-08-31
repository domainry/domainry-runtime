package workspaceprovision

const (
	FailureAfterWorkspace              = "after_workspace"
	FailureAfterTenantRegistry         = "after_tenant_registry"
	FailureAfterIdentity               = "after_identity"
	FailureAfterWorkspaceConfiguration = "after_workspace_configuration"
	FailureAfterApplicationProjections = "after_application_projections"
	FailureAfterReceipt                = "after_receipt"
)

type FailureInjector interface {
	Inject(string) error
}
