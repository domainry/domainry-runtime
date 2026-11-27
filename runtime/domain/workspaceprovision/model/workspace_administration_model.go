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

type CatalogEntry struct {
	CanonicalCode string `json:"canonical_code"`
	DisplayName   string `json:"display_name"`
	Status        string `json:"status"`
	Revision      int    `json:"revision"`
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
	CausationID           string `json:"-"`
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
