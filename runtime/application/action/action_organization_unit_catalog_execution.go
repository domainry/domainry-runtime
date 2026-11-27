package action

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func (e *businessActionExecution) ListOrganizationUnits(ctx context.Context, request runtimeext.OrganizationUnitCatalogRequest) (runtimeext.OrganizationUnitCatalogPage, error) {
	if err := e.validateOrganizationUnitCatalogAccess(); err != nil {
		return runtimeext.OrganizationUnitCatalogPage{}, err
	}
	if !request.Valid() {
		return runtimeext.OrganizationUnitCatalogPage{}, apperror.New(apperror.KindBadRequest, "backend.action.organization_unit_catalog_request_invalid", nil, nil)
	}
	pageSize := request.PageSize
	if pageSize == 0 {
		pageSize = e.organizationUnitCatalogGrant.MaxPageSize
		if pageSize > organizationunit.DefaultPageSize {
			pageSize = organizationunit.DefaultPageSize
		}
	}
	if pageSize > e.organizationUnitCatalogGrant.MaxPageSize {
		return runtimeext.OrganizationUnitCatalogPage{}, apperror.New(apperror.KindBadRequest, "backend.action.organization_unit_catalog_page_size_invalid", nil, nil)
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.OrganizationUnitCatalogPage{}, err
	}
	delivery, err := e.bindOrganizationUnitDelivery(txCtx)
	if err != nil {
		return runtimeext.OrganizationUnitCatalogPage{}, err
	}
	page, err := delivery.ListOrganizationUnits(txCtx, organizationunit.ListRequest{
		ContractVersion: organizationunit.DeliveryContractVersionV1,
		AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken), NodeType: organizationunit.NodeType(e.organizationUnitCatalogGrant.NodeType),
		PageSize: pageSize, Cursor: strings.TrimSpace(request.Cursor),
	})
	if err != nil {
		return runtimeext.OrganizationUnitCatalogPage{}, normalizeIdentityCapabilityError(err, "identity.organization_unit_delivery.list_failed")
	}
	if len(page.Items) > pageSize {
		return runtimeext.OrganizationUnitCatalogPage{}, apperror.New(apperror.KindInternal, "backend.action.organization_unit_catalog_result_limit_exceeded", nil, nil)
	}
	result := runtimeext.OrganizationUnitCatalogPage{Items: make([]runtimeext.OrganizationUnitCatalogItem, len(page.Items)), NextCursor: strings.TrimSpace(page.NextCursor)}
	for index, item := range page.Items {
		if strings.TrimSpace(item.OrganizationUnitID) == "" || item.NodeType != organizationunit.NodeType(e.organizationUnitCatalogGrant.NodeType) {
			return runtimeext.OrganizationUnitCatalogPage{}, apperror.New(apperror.KindInternal, "backend.action.organization_unit_catalog_result_invalid", nil, nil)
		}
		issued, issueErr := e.issueOrganizationUnitCatalogItem(item)
		if issueErr != nil {
			return runtimeext.OrganizationUnitCatalogPage{}, issueErr
		}
		result.Items[index] = issued
	}
	return result, nil
}

func (e *businessActionExecution) ResolveOrganizationUnitCatalog(ctx context.Context, organizationID string) (runtimeext.OrganizationUnitCatalogItem, error) {
	if err := e.validateOrganizationUnitCatalogAccess(); err != nil {
		return runtimeext.OrganizationUnitCatalogItem{}, err
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return runtimeext.OrganizationUnitCatalogItem{}, apperror.New(apperror.KindBadRequest, "backend.action.organization_unit_catalog_reference_invalid", nil, nil)
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.OrganizationUnitCatalogItem{}, err
	}
	delivery, err := e.bindOrganizationUnitDelivery(txCtx)
	if err != nil {
		return runtimeext.OrganizationUnitCatalogItem{}, err
	}
	resolved, err := delivery.ResolveOrganizationUnit(txCtx, organizationunit.ResolveRequest{
		ContractVersion: organizationunit.DeliveryContractVersionV1, AccessToken: strings.TrimSpace(e.requestIdentity.AccessToken),
		OrganizationID: organizationID, NodeType: organizationunit.NodeType(e.organizationUnitCatalogGrant.NodeType),
	})
	if err != nil {
		return runtimeext.OrganizationUnitCatalogItem{}, normalizeIdentityCapabilityError(err, "identity.organization_unit_delivery.resolve_failed")
	}
	if strings.TrimSpace(resolved.OrganizationUnitID) != organizationID || resolved.NodeType != organizationunit.NodeType(e.organizationUnitCatalogGrant.NodeType) {
		return runtimeext.OrganizationUnitCatalogItem{}, apperror.New(apperror.KindInternal, "backend.action.organization_unit_catalog_result_invalid", nil, nil)
	}
	return e.issueOrganizationUnitCatalogItem(resolved)
}

func (e *businessActionExecution) validateOrganizationUnitCatalogAccess() error {
	if e == nil || e.unitOfWork == nil || e.organizationUnitCatalogGrant == nil {
		return apperror.New(apperror.KindForbidden, "backend.action.organization_unit_catalog_grant_denied", nil, nil)
	}
	phase := e.Phase()
	if (phase != runtimeext.ExecutionPhasePrewrite && phase != runtimeext.ExecutionPhaseWriting) || !ActionAllowed(e.invocation.Principal, e.action) {
		return apperror.New(apperror.KindForbidden, "backend.action.organization_unit_catalog_phase_or_permission_denied", nil, nil)
	}
	return nil
}

func (e *businessActionExecution) issueOrganizationUnitCatalogItem(item organizationunit.DeliveredOrganizationUnit) (runtimeext.OrganizationUnitCatalogItem, error) {
	projected := runtimeext.OrganizationUnitCatalogItem{
		ID: strings.TrimSpace(item.OrganizationUnitID), Code: strings.TrimSpace(item.Code), Name: strings.TrimSpace(item.Name), NodeType: runtimeext.OrganizationUnitNodeType(item.NodeType),
		Status: strings.TrimSpace(item.Status), ParentOrganizationID: strings.TrimSpace(item.ParentOrganizationID), SortOrder: item.SortOrder, Version: item.Version,
	}
	issued, claim, err := runtimeext.IssueOrganizationUnitCatalogItem(projected)
	if err != nil {
		return runtimeext.OrganizationUnitCatalogItem{}, err
	}
	if e.organizationUnitCatalogClaims == nil {
		e.organizationUnitCatalogClaims = map[string]string{}
	}
	e.organizationUnitCatalogClaims[claim] = projected.ID
	return issued, nil
}
