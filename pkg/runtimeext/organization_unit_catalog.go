package runtimeext

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

const OrganizationUnitCatalogMaximumPageSize = 100

// OrganizationUnitCatalogCapability is a static Handler grant for one node
// type in the authorized Identity organization tree.
type OrganizationUnitCatalogCapability struct {
	NodeType    OrganizationUnitNodeType
	MaxPageSize int
}

func (capability OrganizationUnitCatalogCapability) Valid() bool {
	return capability.NodeType.Valid() && capability.MaxPageSize > 0 && capability.MaxPageSize <= OrganizationUnitCatalogMaximumPageSize
}

type OrganizationUnitCatalogRequest struct {
	PageSize int
	Cursor   string
}

func (request OrganizationUnitCatalogRequest) Valid() bool {
	return request.PageSize >= 0 && request.PageSize <= OrganizationUnitCatalogMaximumPageSize && len(strings.TrimSpace(request.Cursor)) <= 2048
}

type OrganizationUnitCatalogItem struct {
	ID                   string
	Code                 string
	Name                 string
	NodeType             OrganizationUnitNodeType
	Status               string
	ParentOrganizationID string
	SortOrder            int
	Version              int64
	recordOwnerOrgID     string
	authorizationClaim   string
}

func IssueOrganizationUnitCatalogItem(item OrganizationUnitCatalogItem) (OrganizationUnitCatalogItem, string, error) {
	organizationID := strings.TrimSpace(item.ID)
	if organizationID == "" || !item.NodeType.Valid() {
		return OrganizationUnitCatalogItem{}, "", &BusinessError{Class: BusinessErrorInternal, Code: "backend.action.organization_unit_catalog_result_invalid", Message: "Organization Unit catalog item identity and node type are required"}
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return OrganizationUnitCatalogItem{}, "", &BusinessError{Class: BusinessErrorInternal, Code: "backend.action.organization_unit_catalog_reference_failed", Message: "Organization Unit catalog reference could not be issued", Cause: err}
	}
	claim := hex.EncodeToString(entropy[:])
	item.ID = organizationID
	item.recordOwnerOrgID = organizationID
	item.authorizationClaim = claim
	return item, claim, nil
}

func ResolveOrganizationUnitCatalogItem(item OrganizationUnitCatalogItem) (string, string, bool) {
	organizationID := strings.TrimSpace(item.recordOwnerOrgID)
	claim := strings.TrimSpace(item.authorizationClaim)
	return organizationID, claim, organizationID != "" && claim != "" && item.NodeType.Valid() && strings.TrimSpace(item.ID) == organizationID
}

type OrganizationUnitCatalogPage struct {
	Items      []OrganizationUnitCatalogItem
	NextCursor string
}

type OrganizationUnitCatalogExecution interface {
	ListOrganizationUnits(context.Context, OrganizationUnitCatalogRequest) (OrganizationUnitCatalogPage, error)
	ResolveOrganizationUnitCatalog(context.Context, string) (OrganizationUnitCatalogItem, error)
}

func ExecuteOrganizationUnitCatalog(ctx context.Context, execution ActionExecution, request OrganizationUnitCatalogRequest) (OrganizationUnitCatalogPage, error) {
	catalog, ok := execution.(OrganizationUnitCatalogExecution)
	if !ok {
		return OrganizationUnitCatalogPage{}, &BusinessError{Class: BusinessErrorInternal, Code: "backend.action.organization_unit_catalog_unavailable", Message: "Runtime Organization Unit catalog is unavailable"}
	}
	return catalog.ListOrganizationUnits(ctx, request)
}

func ExecuteOrganizationUnitCatalogResolve(ctx context.Context, execution ActionExecution, organizationID string) (OrganizationUnitCatalogItem, error) {
	catalog, ok := execution.(OrganizationUnitCatalogExecution)
	if !ok {
		return OrganizationUnitCatalogItem{}, &BusinessError{Class: BusinessErrorInternal, Code: "backend.action.organization_unit_catalog_unavailable", Message: "Runtime Organization Unit catalog is unavailable"}
	}
	return catalog.ResolveOrganizationUnitCatalog(ctx, organizationID)
}
