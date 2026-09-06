package action

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func (e *businessActionExecution) ListStoreOrganizations(ctx context.Context, request runtimeext.StoreOrganizationCatalogRequest) (runtimeext.StoreOrganizationCatalogPage, error) {
	if e == nil || e.unitOfWork == nil || e.storeCatalogGrant == nil {
		return runtimeext.StoreOrganizationCatalogPage{}, apperror.New(apperror.KindForbidden, "backend.action.store_organization_catalog_grant_denied", nil, nil)
	}
	phase := e.Phase()
	if (phase != runtimeext.ExecutionPhasePrewrite && phase != runtimeext.ExecutionPhaseWriting) || !ActionAllowed(e.invocation.Principal, e.action) {
		return runtimeext.StoreOrganizationCatalogPage{}, apperror.New(apperror.KindForbidden, "backend.action.store_organization_catalog_phase_or_permission_denied", nil, nil)
	}
	if !request.Valid() {
		return runtimeext.StoreOrganizationCatalogPage{}, apperror.New(apperror.KindBadRequest, "backend.action.store_organization_catalog_request_invalid", nil, nil)
	}
	pageSize := request.PageSize
	if pageSize == 0 {
		pageSize = e.storeCatalogGrant.MaxPageSize
		if pageSize > identitysdk.StoreOrganizationDefaultPageSize {
			pageSize = identitysdk.StoreOrganizationDefaultPageSize
		}
	}
	if pageSize > e.storeCatalogGrant.MaxPageSize {
		return runtimeext.StoreOrganizationCatalogPage{}, apperror.New(apperror.KindBadRequest, "backend.action.store_organization_catalog_page_size_invalid", nil, nil)
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.StoreOrganizationCatalogPage{}, err
	}
	delivery, err := e.bindStoreOrganizationDelivery(txCtx)
	if err != nil {
		return runtimeext.StoreOrganizationCatalogPage{}, err
	}
	page, err := delivery.ListStoreOrganizations(txCtx, identitysdk.StoreOrganizationListRequest{
		ContractVersion: identitysdk.StoreOrganizationDeliveryContractVersionV1,
		AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken),
		PageSize:        pageSize,
		Cursor:          strings.TrimSpace(request.Cursor),
	})
	if err != nil {
		return runtimeext.StoreOrganizationCatalogPage{}, normalizeIdentityCapabilityError(err, "identity.store_organization_delivery.list_failed")
	}
	if len(page.Items) > pageSize {
		return runtimeext.StoreOrganizationCatalogPage{}, apperror.New(apperror.KindInternal, "backend.action.store_organization_catalog_result_limit_exceeded", nil, nil)
	}
	result := runtimeext.StoreOrganizationCatalogPage{Items: make([]runtimeext.StoreOrganizationCatalogItem, len(page.Items)), NextCursor: strings.TrimSpace(page.NextCursor)}
	for index, item := range page.Items {
		if strings.TrimSpace(item.ID) == "" {
			return runtimeext.StoreOrganizationCatalogPage{}, apperror.New(apperror.KindInternal, "backend.action.store_organization_catalog_result_invalid", nil, nil)
		}
		projected := runtimeext.StoreOrganizationCatalogItem{
			ID: strings.TrimSpace(item.ID), Code: strings.TrimSpace(item.Code), Name: strings.TrimSpace(item.Name), Status: strings.TrimSpace(item.Status),
			ParentOrganizationID: strings.TrimSpace(item.ParentOrganizationID), SortOrder: item.SortOrder, Version: item.Version,
		}
		issued, claim, issueErr := runtimeext.IssueStoreOrganizationCatalogItem(projected)
		if issueErr != nil {
			return runtimeext.StoreOrganizationCatalogPage{}, issueErr
		}
		if e.storeCatalogClaims == nil {
			e.storeCatalogClaims = map[string]string{}
		}
		e.storeCatalogClaims[claim] = projected.ID
		result.Items[index] = issued
	}
	return result, nil
}
