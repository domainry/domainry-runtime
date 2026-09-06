package workspaceprovisionmodel

import "errors"

var (
	ErrInvalid                    = errors.New("workspace provisioning request invalid")
	ErrIdempotencyConflict        = errors.New("workspace provisioning idempotency conflict")
	ErrCodeConflict               = errors.New("workspace canonical code conflict")
	ErrWorkspaceNotFound          = errors.New("workspace not found")
	ErrIdentityUnavailable        = errors.New("embedded Identity workspace bootstrap is unavailable")
	ErrInitializationRequired     = errors.New("initial Workspace must be initialized before workspace provisioning")
	ErrAlreadyInitialized         = errors.New("initial Workspace is already initialized")
	ErrLegacyAdjudicationRequired = errors.New("retired workspace provisioning receipt requires Identity graph adjudication")
	// ErrAcceptanceFailure is intentionally stable and contains no generated
	// Workspace, credential, organization, or application-record identity.
	ErrAcceptanceFailure = errors.New("workspace provisioning acceptance failure")
)

// CommercialConfiguration is Runtime's typed commercial contract. It is not
// an application configuration bag: callers cannot use it to choose database
// objects, fields, ownership, SQL, or projections.
type CommercialConfiguration struct {
	Plan                  string `json:"plan"`
	IncludedUserLimit     int    `json:"included_user_limit"`
	MaxUserLimit          int    `json:"max_user_limit"`
	IncludedCustomerLimit int    `json:"included_customer_limit"`
	MaxCustomerLimit      int    `json:"max_customer_limit"`
	IncludedStoreLimit    int    `json:"included_store_limit"`
	MaxStores             int    `json:"max_stores"`
	ContractDate          string `json:"contract_date"`
	BillingDay            int    `json:"billing_day"`
	BillingContactName    string `json:"billing_contact_name"`
	BillingContactPhone   string `json:"billing_contact_phone"`
	BillingContactEmail   string `json:"billing_contact_email"`
	BillingContactAddress string `json:"billing_contact_address"`
	BillingContactNotes   string `json:"billing_contact_notes"`
}

type Request struct {
	RequestID               string                  `json:"request_id"`
	WorkspaceCode           string                  `json:"workspace_code"`
	WorkspaceName           string                  `json:"workspace_name"`
	FirstStoreCode          string                  `json:"first_store_code"`
	FirstStoreName          string                  `json:"first_store_name"`
	AdminLoginID            string                  `json:"admin_login_id"`
	AdminName               string                  `json:"admin_name"`
	CommercialConfiguration CommercialConfiguration `json:"commercial_configuration"`
	ApplicationBootstrap    map[string]any          `json:"application_bootstrap,omitempty"`
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
	FirstStoreID       string `json:"-"`
	InitialAdminUserID string `json:"-"`
}
