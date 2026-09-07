package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	organizationunit "github.com/domainry/domainry-identity/organizationunit"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func (e *businessActionExecution) bindOrganizationUnitDelivery(ctx context.Context) (organizationunit.Delivery, error) {
	if e.dependencies.BindOrganizationUnitDelivery == nil {
		return nil, apperror.New(apperror.KindInternal, "identity.organization_unit_delivery_transaction_required", nil, nil)
	}
	if strings.TrimSpace(e.requestIdentity.AccessToken) == "" {
		return nil, apperror.New(apperror.KindForbidden, "identity.organization_unit_delivery_request_identity_required", nil, nil)
	}
	delivery, err := e.dependencies.BindOrganizationUnitDelivery(ctx)
	if err != nil {
		return nil, normalizeIdentityCapabilityError(err, "identity.organization_unit_delivery_transaction_required")
	}
	if delivery == nil {
		return nil, apperror.New(apperror.KindInternal, "identity.organization_unit_delivery_transaction_required", nil, nil)
	}
	return delivery, nil
}

func (e *businessActionExecution) CreateOrganizationUnit(ctx context.Context, request runtimeext.OrganizationUnitDeliveryRequest) (runtimeext.OrganizationUnitDeliveryResult, error) {
	if e == nil || e.organizationUnitGrant == nil || !e.organizationUnitGrant.Allows(runtimeext.OrganizationUnitDeliveryCreate, request.NodeType) {
		return runtimeext.OrganizationUnitDeliveryResult{}, apperror.New(apperror.KindForbidden, "backend.action.organization_unit_delivery_grant_denied", nil, nil)
	}
	request.Code, request.Name = strings.TrimSpace(request.Code), strings.TrimSpace(request.Name)
	if !request.Valid() {
		return runtimeext.OrganizationUnitDeliveryResult{}, apperror.New(apperror.KindBadRequest, "backend.action.organization_unit_delivery_invalid", nil, nil)
	}
	if e.organizationUnitRequest != nil {
		if reflect.DeepEqual(*e.organizationUnitRequest, request) {
			return e.organizationUnitResult, nil
		}
		return runtimeext.OrganizationUnitDeliveryResult{}, apperror.New(apperror.KindConflict, "backend.idempotency_key_reused", nil, nil)
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.OrganizationUnitDeliveryResult{}, err
	}
	delivery, err := e.bindOrganizationUnitDelivery(txCtx)
	if err != nil {
		return runtimeext.OrganizationUnitDeliveryResult{}, err
	}
	parentID, err := e.organizationUnitParentID(txCtx)
	if err != nil {
		return runtimeext.OrganizationUnitDeliveryResult{}, err
	}
	organizationID := stableOrganizationUnitID(e.workspace.ID, e.identity.ExecutionID)
	delivered, err := delivery.CreateOrganizationUnit(txCtx, organizationunit.DeliveryRequest{
		ContractVersion: organizationunit.DeliveryContractVersionV1,
		AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken),
		IdempotencyKey:  e.identity.ExecutionID + ":organization_unit_create",
		Organization: organizationunit.CreateCandidate{
			OrganizationID: organizationID, Code: request.Code, Name: request.Name,
			NodeType: organizationunit.NodeType(request.NodeType), ParentOrganizationID: parentID,
			SortOrder: request.SortOrder, ExpectedVersion: 0,
		},
	})
	if err != nil {
		return runtimeext.OrganizationUnitDeliveryResult{}, normalizeIdentityCapabilityError(err, "identity.organization_unit_delivery.create_failed")
	}
	organization := delivered.Organization
	if strings.TrimSpace(delivered.DeliveryID) == "" || strings.TrimSpace(organization.ID) != organizationID || strings.TrimSpace(organization.ParentOrganizationID) != parentID ||
		strings.TrimSpace(string(organization.NodeType)) != string(request.NodeType) || strings.TrimSpace(organization.Code) != request.Code ||
		strings.TrimSpace(organization.Name) != request.Name || organization.SortOrder != request.SortOrder ||
		!strings.EqualFold(strings.TrimSpace(organization.Status), "active") || organization.Version != 1 {
		return runtimeext.OrganizationUnitDeliveryResult{}, apperror.New(apperror.KindInternal, "backend.action.organization_unit_delivery_identity_mismatch", nil, nil)
	}
	result := runtimeext.OrganizationUnitDeliveryResult{
		Organization: runtimeext.OrganizationUnit{ID: organizationID, NodeType: request.NodeType},
		Version:      organization.Version,
		Replayed:     delivered.Replayed,
	}
	if e.targetGrant != nil && e.targetGrant.Source == runtimeext.TargetOrganizationSourceDeliveredOrganizationUnit {
		e.setTargetOrganization(organizationID, true)
	}
	e.organizationUnitRequest, e.organizationUnitResult = &request, result
	return result, nil
}

func (e *businessActionExecution) ResolveOrganizationUnit(ctx context.Context, request runtimeext.OrganizationUnitResolveRequest) (runtimeext.OrganizationUnitDeliveryResult, error) {
	if e == nil || e.organizationUnitGrant == nil || !request.Valid() || !e.organizationUnitGrant.Allows(runtimeext.OrganizationUnitDeliveryResolve, request.NodeType) {
		return runtimeext.OrganizationUnitDeliveryResult{}, apperror.New(apperror.KindForbidden, "backend.action.organization_unit_delivery_grant_denied", nil, nil)
	}
	if e.organizationUnitResolve != nil {
		if reflect.DeepEqual(*e.organizationUnitResolve, request) {
			return e.organizationUnitResult, nil
		}
		return runtimeext.OrganizationUnitDeliveryResult{}, apperror.New(apperror.KindConflict, "backend.idempotency_key_reused", nil, nil)
	}
	targetID := strings.TrimSpace(e.organizationUnitTargetID)
	if targetID == "" {
		return runtimeext.OrganizationUnitDeliveryResult{}, apperror.New(apperror.KindForbidden, "backend.action.target_organization_denied", nil, nil)
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.OrganizationUnitDeliveryResult{}, err
	}
	delivery, err := e.bindOrganizationUnitDelivery(txCtx)
	if err != nil {
		return runtimeext.OrganizationUnitDeliveryResult{}, err
	}
	organization, err := delivery.ResolveOrganizationUnit(txCtx, organizationunit.ResolveRequest{
		ContractVersion: organizationunit.DeliveryContractVersionV1,
		AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken),
		OrganizationID:  targetID,
		NodeType:        organizationunit.NodeType(request.NodeType),
	})
	if err != nil {
		return runtimeext.OrganizationUnitDeliveryResult{}, normalizeIdentityCapabilityError(err, "identity.organization_unit_delivery.resolve_failed")
	}
	if strings.TrimSpace(organization.ID) != targetID || strings.TrimSpace(string(organization.NodeType)) != string(request.NodeType) ||
		strings.TrimSpace(organization.ParentOrganizationID) == "" || !strings.EqualFold(strings.TrimSpace(organization.Status), "active") || organization.Version != 1 {
		return runtimeext.OrganizationUnitDeliveryResult{}, apperror.New(apperror.KindInternal, "backend.action.organization_unit_delivery_identity_mismatch", nil, nil)
	}
	result := runtimeext.OrganizationUnitDeliveryResult{
		Organization: runtimeext.OrganizationUnit{ID: targetID, NodeType: request.NodeType},
		Version:      organization.Version,
	}
	e.setTargetOrganization(targetID, false)
	e.organizationUnitResolve, e.organizationUnitResult = &request, result
	return result, nil
}

func (e *businessActionExecution) organizationUnitParentID(ctx context.Context) (string, error) {
	if e == nil || e.organizationUnitGrant == nil {
		return "", apperror.New(apperror.KindInternal, "backend.action.organization_unit_delivery_contract_invalid", nil, nil)
	}
	switch e.organizationUnitGrant.ParentSource {
	case runtimeext.OrganizationUnitParentSourceWorkspaceCompany:
		if e.dependencies.WorkspaceCommercialConfiguration == nil {
			return "", missingExecutorPort("workspace_commercial_configuration")
		}
		commercial, err := e.dependencies.WorkspaceCommercialConfiguration.LockWorkspaceCommercialConfiguration(ctx, e.workspace.ID)
		if err != nil {
			return "", apperror.New(apperror.KindConflict, "backend.action.workspace_company_authority_unavailable", err, nil)
		}
		parentID := strings.TrimSpace(commercial.CompanyOrganizationID)
		if parentID == "" {
			return "", apperror.New(apperror.KindConflict, "backend.action.workspace_company_authority_unavailable", nil, nil)
		}
		return parentID, nil
	case runtimeext.OrganizationUnitParentSourceTargetOrganization:
		parentID := strings.TrimSpace(e.organizationUnitTargetID)
		if parentID == "" || !e.targetResolved || strings.TrimSpace(e.targetOrganization.ID) != parentID {
			return "", apperror.New(apperror.KindForbidden, "backend.action.target_organization_denied", nil, nil)
		}
		return parentID, nil
	default:
		return "", apperror.New(apperror.KindInternal, "backend.action.organization_unit_delivery_contract_invalid", nil, nil)
	}
}

func stableOrganizationUnitID(workspaceID, executionID string) string {
	digest := sha256.Sum256([]byte("runtime-action-organization-unit-v1\x00" + strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(executionID)))
	return "org_" + hex.EncodeToString(digest[:16])
}
