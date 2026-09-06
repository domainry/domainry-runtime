package workspaceprovisionmodel

import "errors"

const (
	WorkspaceStatusActive    = "active"
	WorkspaceStatusSuspended = "suspended"
)

var (
	ErrAdministrationForbidden    = errors.New("workspace administration forbidden")
	ErrAdministrationUnavailable  = errors.New("workspace administration unavailable")
	ErrInvalidCursor              = errors.New("workspace catalog cursor invalid")
	ErrRevisionConflict           = errors.New("workspace revision conflict")
	ErrInitialWorkspaceSuspension = errors.New("initial Workspace cannot be suspended")
)

type CatalogQuery struct {
	PageSize int
	Cursor   string
}

type CommercialConfigurationView struct {
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
	Revision              int    `json:"-"`
}

type CatalogEntry struct {
	CanonicalCode           string                      `json:"canonical_code"`
	DisplayName             string                      `json:"display_name"`
	Status                  string                      `json:"status"`
	Revision                int                         `json:"revision"`
	CommercialConfiguration CommercialConfigurationView `json:"commercial_configuration"`
}

type CatalogPage struct {
	Items      []CatalogEntry `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type AdministrationActor struct {
	WorkspaceID           string `json:"-"`
	UserID                string `json:"-"`
	RoleKey               string `json:"-"`
	RequestID             string `json:"-"`
	AuthorizationRevision string `json:"-"`
}

type LifecycleRequest struct {
	ExpectedRevision int `json:"expected_revision"`
}

type LifecycleResult struct {
	Workspace       CatalogEntry `json:"workspace"`
	RevokedSessions int          `json:"revoked_sessions"`
	Replayed        bool         `json:"replayed"`
}

type CommercialConfigurationUpdateRequest struct {
	ExpectedRevision int                     `json:"expected_revision"`
	Configuration    CommercialConfiguration `json:"commercial_configuration"`
}

type CommercialConfigurationUpdateResult struct {
	Workspace CatalogEntry `json:"workspace"`
	Replayed  bool         `json:"replayed"`
}
