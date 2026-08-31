package workspaceprovisionmodel

import "errors"

var (
	ErrInvalid                = errors.New("workspace provisioning request invalid")
	ErrIdempotencyConflict    = errors.New("workspace provisioning idempotency conflict")
	ErrCodeConflict           = errors.New("workspace canonical code conflict")
	ErrWorkspaceNotFound      = errors.New("workspace not found")
	ErrIdentityUnavailable    = errors.New("embedded Identity workspace provisioning is unavailable")
	ErrInitializationRequired = errors.New("initial tenant must be initialized before workspace provisioning")
	ErrAlreadyInitialized     = errors.New("initial tenant is already initialized")
	// ErrAcceptanceFailure is intentionally stable and contains no generated
	// workspace, registry, credential, or projection identity.
	ErrAcceptanceFailure = errors.New("workspace provisioning acceptance failure")
)

type Request struct {
	RequestID          string         `json:"request_id"`
	TenantCode         string         `json:"tenant_code"`
	TenantName         string         `json:"tenant_name"`
	AdminLoginID       string         `json:"admin_login_id"`
	AdminName          string         `json:"admin_name"`
	StoreConfiguration map[string]any `json:"store_configuration"`
	InitialPassword    string         `json:"-"`
}

type Result struct {
	TenantRegistryID   string            `json:"tenant_registry_id"`
	WorkspaceID        string            `json:"workspace_id"`
	CanonicalCode      string            `json:"canonical_code"`
	AdminLoginID       string            `json:"admin_login_id"`
	InitialPassword    string            `json:"initial_password,omitempty"`
	MustChangePassword bool              `json:"must_change_password"`
	Replayed           bool              `json:"replayed"`
	ProjectionIDs      map[string]string `json:"application_projection_ids,omitempty"`
}

type RoleReconciliationResult struct {
	WorkspaceID      string `json:"workspace_id"`
	ProvisionedRoles int    `json:"provisioned_roles"`
}
