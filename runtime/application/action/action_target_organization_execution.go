package action

import (
	"context"
	"errors"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func cloneTargetOrganizationCapability(value *runtimeext.ActionTargetOrganizationCapability) *runtimeext.ActionTargetOrganizationCapability {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneOrganizationUnitDeliveryCapability(value *runtimeext.OrganizationUnitDeliveryCapability) *runtimeext.OrganizationUnitDeliveryCapability {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Operations = append([]runtimeext.OrganizationUnitDeliveryOperation(nil), value.Operations...)
	cloned.NodeTypes = append([]runtimeext.OrganizationUnitNodeType(nil), value.NodeTypes...)
	return &cloned
}

func cloneIdentityHandlerDeliveryCapability(value *runtimeext.IdentityHandlerDeliveryCapability) *runtimeext.IdentityHandlerDeliveryCapability {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Operations = append([]runtimeext.IdentityHandlerOperation(nil), value.Operations...)
	cloned.ProfileBindings = append([]runtimeext.IdentityProfileBindingCapability(nil), value.ProfileBindings...)
	return &cloned
}

func cloneOrganizationUnitCatalogCapability(value *runtimeext.OrganizationUnitCatalogCapability) *runtimeext.OrganizationUnitCatalogCapability {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (e *businessActionExecution) initializeTargetOrganization(ctx context.Context) error {
	if e == nil || e.targetGrant == nil {
		return nil
	}
	switch e.targetGrant.Source {
	case runtimeext.TargetOrganizationSourceExplicit:
		targetID := strings.TrimSpace(e.invocation.TargetOrganizationID)
		if targetID == "" {
			return apperror.New(apperror.KindBadRequest, "backend.action.target_organization_required", nil, nil)
		}
		txCtx, err := e.unitOfWork.beginWriting(ctx)
		if err != nil {
			return err
		}
		delivery, err := e.bindOrganizationUnitDelivery(txCtx)
		if err != nil {
			return err
		}
		organization, err := delivery.ResolveOrganizationUnit(txCtx, organizationunit.ResolveRequest{
			ContractVersion: organizationunit.DeliveryContractVersionV1,
			AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken),
			OrganizationID:  targetID,
			NodeType:        organizationunit.NodeType(e.targetGrant.NodeType),
		})
		if err != nil {
			return normalizeIdentityCapabilityError(err, "identity.organization_unit_delivery.resolve_failed")
		}
		if strings.TrimSpace(organization.OrganizationUnitID) != targetID || organization.NodeType != organizationunit.NodeType(e.targetGrant.NodeType) || !strings.EqualFold(strings.TrimSpace(organization.Status), "active") {
			return apperror.New(apperror.KindInternal, "backend.action.target_organization_identity_mismatch", nil, nil)
		}
		if err := e.authorizeTargetOrganization(targetID); err != nil {
			return err
		}
		e.organizationUnitTargetID = targetID
		e.setTargetOrganization(targetID, false)
		return nil
	case runtimeext.TargetOrganizationSourceExplicitOrSoleAuthorizedOrganization:
		targetID := strings.TrimSpace(e.invocation.TargetOrganizationID)
		txCtx, err := e.unitOfWork.beginWriting(ctx)
		if err != nil {
			return err
		}
		delivery, err := e.bindOrganizationUnitDelivery(txCtx)
		if err != nil {
			return err
		}
		if targetID == "" {
			targetID, err = e.resolveSoleAuthorizedActiveOrganization(txCtx, delivery, e.targetGrant.NodeType)
			if err != nil {
				return err
			}
		} else {
			organization, resolveErr := delivery.ResolveOrganizationUnit(txCtx, organizationunit.ResolveRequest{
				ContractVersion: organizationunit.DeliveryContractVersionV1,
				AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken), OrganizationID: targetID,
				NodeType: organizationunit.NodeType(e.targetGrant.NodeType),
			})
			if resolveErr != nil {
				return normalizeIdentityCapabilityError(resolveErr, "identity.organization_unit_delivery.resolve_failed")
			}
			if strings.TrimSpace(organization.OrganizationUnitID) != targetID || !strings.EqualFold(strings.TrimSpace(organization.Status), "active") {
				return apperror.New(apperror.KindForbidden, "backend.action.target_organization_denied", nil, nil)
			}
		}
		if err := e.authorizeTargetOrganization(targetID); err != nil {
			return err
		}
		e.organizationUnitTargetID = targetID
		e.setTargetOrganization(targetID, false)
		return nil
	case runtimeext.TargetOrganizationSourceRecordOwner:
		if e.dependencies.ResolveRecordTargetOrganization == nil {
			return missingExecutorPort("resolve_record_target_organization")
		}
		txCtx, err := e.unitOfWork.beginRecordOwnerResolution(ctx)
		if err != nil {
			return err
		}
		targetID, err := e.dependencies.ResolveRecordTargetOrganization(txCtx, e.action, e.invocation)
		if err != nil {
			return err
		}
		if strings.TrimSpace(targetID) == "" {
			return apperror.New(apperror.KindForbidden, "backend.action.target_organization_denied", nil, nil)
		}
		e.organizationUnitTargetID = strings.TrimSpace(targetID)
		e.setTargetOrganization(targetID, false)
		return nil
	case runtimeext.TargetOrganizationSourceDeliveredOrganizationUnit:
		return nil
	default:
		return apperror.New(apperror.KindInternal, "backend.action.target_organization_contract_invalid", nil, map[string]string{"action": e.action.Key})
	}
}

func (e *businessActionExecution) resolveSoleAuthorizedActiveOrganization(ctx context.Context, delivery organizationunit.Delivery, nodeType runtimeext.OrganizationUnitNodeType) (string, error) {
	page, err := delivery.ListOrganizationUnits(ctx, organizationunit.ListRequest{
		ContractVersion: organizationunit.DeliveryContractVersionV1,
		AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken), NodeType: organizationunit.NodeType(nodeType), PageSize: 2,
	})
	if err != nil {
		return "", normalizeIdentityCapabilityError(err, "identity.organization_unit_delivery.list_failed")
	}
	// Fail closed unless the complete authorized catalog is exactly one active
	// node. We deliberately do not skip disabled rows or select the first row:
	// either would let a multi-store principal acquire an implicit target.
	if len(page.Items) != 1 || strings.TrimSpace(page.NextCursor) != "" ||
		!strings.EqualFold(strings.TrimSpace(page.Items[0].Status), "active") || strings.TrimSpace(page.Items[0].OrganizationUnitID) == "" {
		return "", apperror.New(apperror.KindBadRequest, "backend.action.target_organization_ambiguous", nil, nil)
	}
	return strings.TrimSpace(page.Items[0].OrganizationUnitID), nil
}

func (e *businessActionExecution) TargetOrganization() (runtimeext.TargetOrganization, bool) {
	if e == nil || !e.targetResolved || strings.TrimSpace(e.targetOrganization.ID) == "" {
		return runtimeext.TargetOrganization{}, false
	}
	return e.targetOrganization, true
}

func (e *businessActionExecution) setTargetOrganization(targetID string, provisioned bool) {
	targetID = strings.TrimSpace(targetID)
	e.targetOrganization = runtimeext.TargetOrganization{ID: targetID}
	e.targetResolved = targetID != ""
	if !provisioned || e.invocation.Principal.AccessBundle == nil || targetID == "" {
		return
	}
	principal := e.invocation.Principal
	bundle := *principal.AccessBundle
	resource, operation := definitionmodel.ActionPermissionSubject(e.action)
	policyKey := "runtime.delivered_organization_unit." + e.identity.ExecutionID
	bundle.DataPolicies = append(append([]identitysdk.DataPolicy(nil), bundle.DataPolicies...), identitysdk.DataPolicy{
		Key:      policyKey,
		Resource: identitysdk.ResourceType(resource),
		Action:   identitysdk.Action(operation),
		Effect:   identitysdk.EffectAllow,
		Predicate: identitysdk.Predicate{
			Fact: "owner_org_id", Operator: identitysdk.OperatorEqual, Value: targetID,
		},
	})
	principal.AccessBundle = &bundle
	e.mutationPrincipal = &principal
}

func (e *businessActionExecution) authorizeTargetOrganization(targetID string) error {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" || e.invocation.Principal.AccessBundle == nil {
		return apperror.New(apperror.KindForbidden, "backend.action.target_organization_denied", nil, nil)
	}
	resource, operation := definitionmodel.ActionPermissionSubject(e.action)
	decision, err := identityevaluator.Evaluate(*e.invocation.Principal.AccessBundle, identitysdk.AccessRequest{
		ObjectKey: resource,
		Action:    operation,
	}, identitysdk.ResourceFacts{"owner_org_id": targetID}, e.invocation.Principal.AuthorizationEvaluationTime())
	if err != nil {
		return apperror.New(apperror.KindForbidden, "backend.action.target_organization_denied", err, nil)
	}
	if !decision.Allowed {
		return apperror.New(apperror.KindForbidden, "backend.action.target_organization_denied", nil, map[string]string{"decision": decision.Code})
	}
	return nil
}

func (e *businessActionExecution) actionMutationPrincipal() principalmodel.Principal {
	if e != nil && e.mutationPrincipal != nil {
		return *e.mutationPrincipal
	}
	return e.invocation.Principal
}

func (e *businessActionExecution) validateMutationTarget(plans []transactionmodel.MutationPlan) error {
	if e == nil || e.targetGrant == nil {
		return nil
	}
	if !e.targetResolved || strings.TrimSpace(e.targetOrganization.ID) == "" {
		return apperror.New(apperror.KindBadRequest, "backend.action.target_organization_unresolved", nil, nil)
	}
	for _, plan := range plans {
		commit := plan.CanonicalCommit()
		ownerID := strings.TrimSpace(commit.Record.OwnerOrgID)
		if ownerID == "" || ownerID != e.targetOrganization.ID {
			return apperror.New(apperror.KindForbidden, "backend.action.mixed_target_organization", nil, map[string]string{
				"object": commit.Object.Key, "record_id": firstNonempty(commit.Record.RecordID, commit.RecordID),
			})
		}
	}
	return nil
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func normalizeIdentityCapabilityError(err error, fallback string) error {
	if err == nil {
		return nil
	}
	code := strings.TrimSpace(fallback)
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) && apperror.IsI18nCode(coded.ErrorCode()) {
		code = strings.TrimSpace(coded.ErrorCode())
	}
	return apperror.New(apperror.KindOf(err), code, err, apperror.ParamsOf(err))
}
