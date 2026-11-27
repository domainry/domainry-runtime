package workspaceprovisionmodel

import "errors"

var (
	ErrInvalid                = errors.New("workspace provisioning request invalid")
	ErrIdempotencyConflict    = errors.New("workspace provisioning idempotency conflict")
	ErrCodeConflict           = errors.New("workspace canonical code conflict")
	ErrWorkspaceNotFound      = errors.New("workspace not found")
	ErrIdentityUnavailable    = errors.New("embedded Identity workspace bootstrap is unavailable")
	ErrInitializationRequired = errors.New("initial Workspace must be initialized before workspace provisioning")
	ErrAlreadyInitialized     = errors.New("initial Workspace is already initialized")
	// ErrAcceptanceFailure is intentionally stable and contains no generated
	// Workspace, credential, organization, or application-record identity.
	ErrAcceptanceFailure = errors.New("workspace provisioning acceptance failure")
)

type Request struct {
	RequestID                      string         `json:"request_id"`
	WorkspaceCode                  string         `json:"workspace_code"`
	WorkspaceName                  string         `json:"workspace_name"`
	AdminLoginID                   string         `json:"admin_login_id"`
	AdminName                      string         `json:"admin_name"`
	InitialAdminPassword           string         `json:"-"`
	InitialAdminMustChangePassword bool           `json:"-"`
	ApplicationBootstrap           map[string]any `json:"application_bootstrap,omitempty"`
}

type CredentialDeliveryStatus string

const (
	CredentialDelivered                CredentialDeliveryStatus = "delivered"
	CredentialUnavailableResetRequired CredentialDeliveryStatus = "unavailable_reset_required"
)

// Result deliberately omits Runtime's physical Workspace ID and all Identity
// organization/user IDs from JSON. Those values are available only to the
// trusted host during initial binding.
type Result struct {
	CanonicalCode      string                   `json:"canonical_code"`
	AdminLoginID       string                   `json:"admin_login_id"`
	InitialPassword    string                   `json:"initial_password,omitempty"`
	MustChangePassword bool                     `json:"must_change_password"`
	CredentialDelivery CredentialDeliveryStatus `json:"credential_delivery_status"`
	Replayed           bool                     `json:"replayed"`

	WorkspaceID        string `json:"-"`
	CompanyID          string `json:"-"`
	InitialAdminUserID string `json:"-"`
}
