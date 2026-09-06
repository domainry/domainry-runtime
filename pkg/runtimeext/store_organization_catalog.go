package runtimeext

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

const StoreOrganizationCatalogMaximumPageSize = 100

// StoreOrganizationCatalogCapability is a static Handler grant for the one
// narrow, authorized Identity store-node catalog. It intentionally has no
// Workspace, company, tenant, object, or operation selector.
type StoreOrganizationCatalogCapability struct {
	MaxPageSize int
}

func (capability StoreOrganizationCatalogCapability) Valid() bool {
	return capability.MaxPageSize > 0 && capability.MaxPageSize <= StoreOrganizationCatalogMaximumPageSize
}

// StoreOrganizationCatalogRequest contains only bounded keyset pagination.
// Identity derives Workspace and organization scope from the trusted token.
type StoreOrganizationCatalogRequest struct {
	PageSize int
	Cursor   string
}

func (request StoreOrganizationCatalogRequest) Valid() bool {
	return request.PageSize >= 0 && request.PageSize <= StoreOrganizationCatalogMaximumPageSize && len(strings.TrimSpace(request.Cursor)) <= 2048
}

// StoreOrganizationCatalogItem is the minimum store projection needed to join
// Runtime owner-org records. Identity hierarchy paths and generic Organization
// mutation surfaces never cross this capability boundary.
type StoreOrganizationCatalogItem struct {
	ID                   string
	Code                 string
	Name                 string
	Status               string
	ParentOrganizationID string
	SortOrder            int
	Version              int64
	recordOwnerOrgID     string
	authorizationClaim   string
}

// IssueStoreOrganizationCatalogItem binds one Identity-authorized store
// projection to the current Runtime Action execution. The opaque claim is not
// serialized and cannot be supplied by generated Handler code.
func IssueStoreOrganizationCatalogItem(item StoreOrganizationCatalogItem) (StoreOrganizationCatalogItem, string, error) {
	organizationID := strings.TrimSpace(item.ID)
	if organizationID == "" {
		return StoreOrganizationCatalogItem{}, "", &BusinessError{Code: "backend.action.store_organization_catalog_result_invalid", Message: "Store Organization catalog item identity is required"}
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return StoreOrganizationCatalogItem{}, "", &BusinessError{Code: "backend.action.store_organization_catalog_reference_failed", Message: "Store Organization catalog reference could not be issued", Cause: err}
	}
	claim := hex.EncodeToString(entropy[:])
	item.ID = organizationID
	item.recordOwnerOrgID = organizationID
	item.authorizationClaim = claim
	return item, claim, nil
}

// ResolveStoreOrganizationCatalogItem returns the Runtime-only record owner
// and execution claim carried by an issued item. Visible field tampering fails
// closed instead of changing the referenced Organization.
func ResolveStoreOrganizationCatalogItem(item StoreOrganizationCatalogItem) (string, string, bool) {
	organizationID := strings.TrimSpace(item.recordOwnerOrgID)
	claim := strings.TrimSpace(item.authorizationClaim)
	return organizationID, claim, organizationID != "" && claim != "" && strings.TrimSpace(item.ID) == organizationID
}

type StoreOrganizationCatalogPage struct {
	Items      []StoreOrganizationCatalogItem
	NextCursor string
}

type StoreOrganizationCatalogExecution interface {
	ListStoreOrganizations(context.Context, StoreOrganizationCatalogRequest) (StoreOrganizationCatalogPage, error)
	ResolveStoreOrganization(context.Context, string) (StoreOrganizationCatalogItem, error)
}

func ExecuteStoreOrganizationCatalog(ctx context.Context, execution ActionExecution, request StoreOrganizationCatalogRequest) (StoreOrganizationCatalogPage, error) {
	catalog, ok := execution.(StoreOrganizationCatalogExecution)
	if !ok {
		return StoreOrganizationCatalogPage{}, &BusinessError{Code: "backend.action.store_organization_catalog_unavailable", Message: "Runtime store Organization catalog is unavailable"}
	}
	return catalog.ListStoreOrganizations(ctx, request)
}

func ExecuteStoreOrganizationCatalogResolve(ctx context.Context, execution ActionExecution, organizationID string) (StoreOrganizationCatalogItem, error) {
	catalog, ok := execution.(StoreOrganizationCatalogExecution)
	if !ok {
		return StoreOrganizationCatalogItem{}, &BusinessError{Code: "backend.action.store_organization_catalog_unavailable", Message: "Runtime store Organization catalog is unavailable"}
	}
	return catalog.ResolveStoreOrganization(ctx, organizationID)
}
