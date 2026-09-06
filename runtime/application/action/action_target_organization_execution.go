package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
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

func cloneIdentityHandlerDeliveryCapability(value *runtimeext.IdentityHandlerDeliveryCapability) *runtimeext.IdentityHandlerDeliveryCapability {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Operations = append([]runtimeext.IdentityHandlerOperation(nil), value.Operations...)
	cloned.ProfileBindings = append([]runtimeext.IdentityProfileBindingCapability(nil), value.ProfileBindings...)
	return &cloned
}

func cloneStoreOrganizationCatalogCapability(value *runtimeext.StoreOrganizationCatalogCapability) *runtimeext.StoreOrganizationCatalogCapability {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneStoreOrganizationMutationCapability(value *runtimeext.ActionStoreOrganizationMutationCapability) *runtimeext.ActionStoreOrganizationMutationCapability {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Operations = append([]runtimeext.StoreOrganizationMutationOperation(nil), value.Operations...)
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
		delivery, err := e.bindStoreOrganizationDelivery(txCtx)
		if err != nil {
			return err
		}
		organization, err := delivery.ResolveStoreOrganization(txCtx, identitysdk.StoreOrganizationResolveRequest{
			ContractVersion: identitysdk.StoreOrganizationDeliveryContractVersionV1,
			AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken),
			OrganizationID:  targetID,
		})
		if err != nil {
			return normalizeIdentityCapabilityError(err, "identity.store_organization_delivery.resolve_failed")
		}
		if strings.TrimSpace(organization.ID) != targetID {
			return apperror.New(apperror.KindInternal, "backend.action.target_organization_identity_mismatch", nil, nil)
		}
		if err := e.authorizeTargetOrganization(targetID); err != nil {
			return err
		}
		e.setTargetOrganization(targetID, false)
		return nil
	case runtimeext.TargetOrganizationSourceExplicitOrSoleAuthorizedStore:
		targetID := strings.TrimSpace(e.invocation.TargetOrganizationID)
		txCtx, err := e.unitOfWork.beginWriting(ctx)
		if err != nil {
			return err
		}
		delivery, err := e.bindStoreOrganizationDelivery(txCtx)
		if err != nil {
			return err
		}
		if targetID == "" {
			targetID, err = e.resolveSoleAuthorizedActiveStore(txCtx, delivery)
			if err != nil {
				return err
			}
		} else {
			organization, resolveErr := delivery.ResolveStoreOrganization(txCtx, identitysdk.StoreOrganizationResolveRequest{
				ContractVersion: identitysdk.StoreOrganizationDeliveryContractVersionV1,
				AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken), OrganizationID: targetID,
			})
			if resolveErr != nil {
				return normalizeIdentityCapabilityError(resolveErr, "identity.store_organization_delivery.resolve_failed")
			}
			if strings.TrimSpace(organization.ID) != targetID || !strings.EqualFold(strings.TrimSpace(organization.Status), "active") {
				return apperror.New(apperror.KindForbidden, "backend.action.target_organization_denied", nil, nil)
			}
		}
		if err := e.authorizeTargetOrganization(targetID); err != nil {
			return err
		}
		e.setTargetOrganization(targetID, false)
		return nil
	case runtimeext.TargetOrganizationSourceRecordOwner:
		if e.dependencies.ResolveRecordTargetOrganization == nil {
			return missingExecutorPort("resolve_record_target_organization")
		}
		txCtx, err := e.unitOfWork.beginWriting(ctx)
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
		e.setTargetOrganization(targetID, false)
		return nil
	case runtimeext.TargetOrganizationSourceProvisionedStore:
		return nil
	default:
		return apperror.New(apperror.KindInternal, "backend.action.target_organization_contract_invalid", nil, map[string]string{"action": e.action.Key})
	}
}

func (e *businessActionExecution) resolveSoleAuthorizedActiveStore(ctx context.Context, delivery identitysdk.StoreOrganizationDelivery) (string, error) {
	page, err := delivery.ListStoreOrganizations(ctx, identitysdk.StoreOrganizationListRequest{
		ContractVersion: identitysdk.StoreOrganizationDeliveryContractVersionV1,
		AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken), PageSize: 2,
	})
	if err != nil {
		return "", normalizeIdentityCapabilityError(err, "identity.store_organization_delivery.list_failed")
	}
	// Fail closed unless the complete authorized catalog is exactly one active
	// store. We deliberately do not skip disabled rows or select the first row:
	// either would let a multi-store principal acquire an implicit target.
	if len(page.Items) != 1 || strings.TrimSpace(page.NextCursor) != "" ||
		!strings.EqualFold(strings.TrimSpace(page.Items[0].Status), "active") || strings.TrimSpace(page.Items[0].ID) == "" {
		return "", apperror.New(apperror.KindBadRequest, "backend.action.target_organization_ambiguous", nil, nil)
	}
	return strings.TrimSpace(page.Items[0].ID), nil
}

func (e *businessActionExecution) bindStoreOrganizationDelivery(ctx context.Context) (identitysdk.StoreOrganizationDelivery, error) {
	if e.dependencies.BindStoreOrganizationDelivery == nil {
		return nil, apperror.New(apperror.KindInternal, "identity.store_organization_delivery_transaction_required", nil, nil)
	}
	if strings.TrimSpace(e.requestIdentity.AccessToken) == "" {
		return nil, apperror.New(apperror.KindForbidden, "identity.store_organization_delivery_request_identity_required", nil, nil)
	}
	delivery, err := e.dependencies.BindStoreOrganizationDelivery(ctx)
	if err != nil {
		return nil, normalizeIdentityCapabilityError(err, "identity.store_organization_delivery_transaction_required")
	}
	if delivery == nil {
		return nil, apperror.New(apperror.KindInternal, "identity.store_organization_delivery_transaction_required", nil, nil)
	}
	return delivery, nil
}

func (e *businessActionExecution) TargetOrganization() (runtimeext.TargetOrganization, bool) {
	if e == nil || !e.targetResolved || strings.TrimSpace(e.targetOrganization.ID) == "" {
		return runtimeext.TargetOrganization{}, false
	}
	return e.targetOrganization, true
}

func (e *businessActionExecution) ProvisionStoreOrganization(ctx context.Context, request runtimeext.StoreOrganizationProvisionRequest) (runtimeext.StoreOrganizationProvisionResult, error) {
	if e == nil || e.targetGrant == nil || e.targetGrant.Source != runtimeext.TargetOrganizationSourceProvisionedStore {
		return runtimeext.StoreOrganizationProvisionResult{}, apperror.New(apperror.KindForbidden, "backend.action.store_organization_provision_denied", nil, nil)
	}
	request.Code = strings.TrimSpace(request.Code)
	request.Name = strings.TrimSpace(request.Name)
	if !request.Valid() {
		return runtimeext.StoreOrganizationProvisionResult{}, apperror.New(apperror.KindBadRequest, "backend.action.store_organization_provision_invalid", nil, nil)
	}
	if e.storeProvisionRequest != nil {
		if reflect.DeepEqual(*e.storeProvisionRequest, request) {
			return e.storeProvisionResult, nil
		}
		return runtimeext.StoreOrganizationProvisionResult{}, apperror.New(apperror.KindConflict, "backend.idempotency_key_reused", nil, nil)
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.StoreOrganizationProvisionResult{}, err
	}
	delivery, err := e.bindStoreOrganizationDelivery(txCtx)
	if err != nil {
		return runtimeext.StoreOrganizationProvisionResult{}, err
	}
	if e.dependencies.WorkspaceCommercialConfiguration == nil {
		return runtimeext.StoreOrganizationProvisionResult{}, missingExecutorPort("workspace_commercial_configuration")
	}
	commercial, err := e.dependencies.WorkspaceCommercialConfiguration.LockWorkspaceCommercialConfiguration(txCtx, e.workspace.ID)
	if err != nil {
		return runtimeext.StoreOrganizationProvisionResult{}, apperror.New(apperror.KindConflict, "backend.action.store_quota_unavailable", err, nil)
	}
	companyOrganizationID := strings.TrimSpace(commercial.CompanyOrganizationID)
	if companyOrganizationID == "" {
		return runtimeext.StoreOrganizationProvisionResult{}, apperror.New(apperror.KindConflict, "backend.action.workspace_company_authority_unavailable", nil, nil)
	}
	activeStores, err := countActiveStoreOrganizations(txCtx, delivery, strings.TrimSpace(e.requestIdentity.AccessToken), companyOrganizationID, commercial.MaxStores)
	if err != nil {
		return runtimeext.StoreOrganizationProvisionResult{}, err
	}
	if activeStores >= commercial.MaxStores {
		return runtimeext.StoreOrganizationProvisionResult{}, apperror.New(apperror.KindConflict, "backend.action.store_quota_exceeded", nil, map[string]string{"max_stores": fmt.Sprint(commercial.MaxStores)})
	}
	organizationID := stableStoreOrganizationID(e.workspace.ID, e.identity.ExecutionID)
	result, err := delivery.DeliverStoreOrganization(txCtx, identitysdk.StoreOrganizationDeliveryRequest{
		ContractVersion: identitysdk.StoreOrganizationDeliveryContractVersionV1,
		AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken),
		IdempotencyKey:  e.identity.ExecutionID + ":store_organization",
		Organization: identitysdk.StoreOrganizationMutation{
			Operation:            identitysdk.StoreOrganizationCreate,
			OrganizationID:       organizationID,
			Code:                 request.Code,
			Name:                 request.Name,
			ParentOrganizationID: companyOrganizationID,
			SortOrder:            request.SortOrder,
			ExpectedVersion:      0,
		},
	})
	if err != nil {
		return runtimeext.StoreOrganizationProvisionResult{}, normalizeIdentityCapabilityError(err, "identity.store_organization_delivery.create_failed")
	}
	if strings.TrimSpace(result.Organization.ID) != organizationID {
		return runtimeext.StoreOrganizationProvisionResult{}, apperror.New(apperror.KindInternal, "backend.action.target_organization_identity_mismatch", nil, nil)
	}
	e.setTargetOrganization(organizationID, true)
	e.storeProvisionRequest = &request
	e.storeProvisionResult = runtimeext.StoreOrganizationProvisionResult{Target: e.targetOrganization, Version: result.Organization.Version, Replayed: result.Replayed}
	return e.storeProvisionResult, nil
}

func (e *businessActionExecution) RenameStoreOrganization(ctx context.Context, request runtimeext.StoreOrganizationRenameRequest) (runtimeext.StoreOrganizationMutationResult, error) {
	if !e.storeOrganizationMutationGranted(runtimeext.StoreOrganizationMutationRename) {
		return runtimeext.StoreOrganizationMutationResult{}, apperror.New(apperror.KindForbidden, "backend.action.store_organization_mutation_grant_denied", nil, nil)
	}
	request.Name = strings.TrimSpace(request.Name)
	if !request.Valid() {
		return runtimeext.StoreOrganizationMutationResult{}, apperror.New(apperror.KindBadRequest, "backend.action.store_organization_rename_invalid", nil, nil)
	}
	if e.storeRenameRequest != nil {
		if reflect.DeepEqual(*e.storeRenameRequest, request) {
			return e.storeRenameResult, nil
		}
		return runtimeext.StoreOrganizationMutationResult{}, apperror.New(apperror.KindConflict, "backend.idempotency_key_reused", nil, nil)
	}
	result, err := e.mutateFixedStoreOrganization(ctx, identitysdk.StoreOrganizationRename, request.Name, request.ExpectedVersion, "rename")
	if err != nil {
		return runtimeext.StoreOrganizationMutationResult{}, err
	}
	e.storeRenameRequest, e.storeRenameResult = &request, result
	return result, nil
}

func (e *businessActionExecution) DisableStoreOrganization(ctx context.Context, request runtimeext.StoreOrganizationDisableRequest) (runtimeext.StoreOrganizationMutationResult, error) {
	if !e.storeOrganizationMutationGranted(runtimeext.StoreOrganizationMutationDisable) {
		return runtimeext.StoreOrganizationMutationResult{}, apperror.New(apperror.KindForbidden, "backend.action.store_organization_mutation_grant_denied", nil, nil)
	}
	if !request.Valid() {
		return runtimeext.StoreOrganizationMutationResult{}, apperror.New(apperror.KindBadRequest, "backend.action.store_organization_disable_invalid", nil, nil)
	}
	if e.storeDisableRequest != nil {
		if reflect.DeepEqual(*e.storeDisableRequest, request) {
			return e.storeDisableResult, nil
		}
		return runtimeext.StoreOrganizationMutationResult{}, apperror.New(apperror.KindConflict, "backend.idempotency_key_reused", nil, nil)
	}
	result, err := e.mutateFixedStoreOrganization(ctx, identitysdk.StoreOrganizationDisable, "", request.ExpectedVersion, "disable")
	if err != nil {
		return runtimeext.StoreOrganizationMutationResult{}, err
	}
	e.storeDisableRequest, e.storeDisableResult = &request, result
	return result, nil
}

func (e *businessActionExecution) storeOrganizationMutationGranted(operation runtimeext.StoreOrganizationMutationOperation) bool {
	if e == nil || e.storeMutationGrant == nil {
		return false
	}
	for _, granted := range e.storeMutationGrant.Operations {
		if granted == operation {
			return true
		}
	}
	return false
}

func (e *businessActionExecution) mutateFixedStoreOrganization(ctx context.Context, operation identitysdk.StoreOrganizationOperation, name string, expectedVersion int64, idempotencySuffix string) (runtimeext.StoreOrganizationMutationResult, error) {
	if e == nil || e.targetGrant == nil || e.targetGrant.Source != runtimeext.TargetOrganizationSourceRecordOwner || !e.targetResolved || strings.TrimSpace(e.targetOrganization.ID) == "" {
		return runtimeext.StoreOrganizationMutationResult{}, apperror.New(apperror.KindForbidden, "backend.action.store_organization_mutation_target_denied", nil, nil)
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.StoreOrganizationMutationResult{}, err
	}
	delivery, err := e.bindStoreOrganizationDelivery(txCtx)
	if err != nil {
		return runtimeext.StoreOrganizationMutationResult{}, err
	}
	targetID := strings.TrimSpace(e.targetOrganization.ID)
	delivered, err := delivery.DeliverStoreOrganization(txCtx, identitysdk.StoreOrganizationDeliveryRequest{
		ContractVersion: identitysdk.StoreOrganizationDeliveryContractVersionV1,
		AccessToken:     strings.TrimSpace(e.requestIdentity.AccessToken), IdempotencyKey: e.identity.ExecutionID + ":store_organization_" + idempotencySuffix,
		Organization: identitysdk.StoreOrganizationMutation{Operation: operation, OrganizationID: targetID, Name: name, ExpectedVersion: expectedVersion},
	})
	if err != nil {
		return runtimeext.StoreOrganizationMutationResult{}, normalizeIdentityCapabilityError(err, "identity.store_organization_delivery."+idempotencySuffix+"_failed")
	}
	organization := delivered.Organization
	if strings.TrimSpace(organization.ID) != targetID || organization.Version < 1 || operation == identitysdk.StoreOrganizationRename && strings.TrimSpace(organization.Name) != name || operation == identitysdk.StoreOrganizationDisable && !strings.EqualFold(strings.TrimSpace(organization.Status), "disabled") {
		return runtimeext.StoreOrganizationMutationResult{}, apperror.New(apperror.KindInternal, "backend.action.store_organization_identity_mismatch", nil, nil)
	}
	return runtimeext.StoreOrganizationMutationResult{Target: e.targetOrganization, Name: strings.TrimSpace(organization.Name), Status: strings.TrimSpace(organization.Status), Version: organization.Version, Replayed: delivered.Replayed}, nil
}

func countActiveStoreOrganizations(ctx context.Context, delivery identitysdk.StoreOrganizationDelivery, accessToken, companyID string, maxStores int) (int, error) {
	cursor, active, pages := "", 0, 0
	for {
		page, err := delivery.ListStoreOrganizations(ctx, identitysdk.StoreOrganizationListRequest{
			ContractVersion: identitysdk.StoreOrganizationDeliveryContractVersionV1,
			AccessToken:     accessToken, PageSize: identitysdk.StoreOrganizationMaxPageSize, Cursor: cursor,
		})
		if err != nil {
			return 0, normalizeIdentityCapabilityError(err, "identity.store_organization_delivery.list_failed")
		}
		for _, organization := range page.Items {
			if strings.TrimSpace(organization.ParentOrganizationID) != companyID {
				return 0, apperror.New(apperror.KindConflict, "backend.action.store_company_scope_conflict", nil, nil)
			}
			if strings.EqualFold(strings.TrimSpace(organization.Status), "active") {
				active++
				if active >= maxStores {
					return active, nil
				}
			}
		}
		next := strings.TrimSpace(page.NextCursor)
		if next == "" {
			return active, nil
		}
		pages++
		if next == cursor || pages > 10_000 {
			return 0, apperror.New(apperror.KindInternal, "identity.store_organization_delivery.page_invalid", nil, nil)
		}
		cursor = next
	}
}

func stableStoreOrganizationID(workspaceID, executionID string) string {
	digest := sha256.Sum256([]byte("runtime-action-store-organization-v1\x00" + strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(executionID)))
	return "store_" + hex.EncodeToString(digest[:16])
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
	bundle.DataPolicies = append(append([]identitysdk.DataPolicy(nil), bundle.DataPolicies...), identitysdk.DataPolicy{
		Key:      "runtime.provisioned_store." + e.identity.ExecutionID,
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
	}, identitysdk.ResourceFacts{"owner_org_id": targetID}, time.Now().UTC())
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
				"object": commit.Object.Key, "record_id": firstNonempty(commit.Record.ID, commit.RecordID),
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
	code := ""
	var identityError *identitysdk.Error
	if errors.As(err, &identityError) {
		code = strings.TrimSpace(identityError.Code)
	}
	if code == "" {
		code = strings.TrimSpace(apperror.CodeOf(err))
	}
	if code == "" {
		code = fallback
	}
	kind := apperror.KindInternal
	lower := strings.ToLower(code)
	switch {
	case strings.Contains(lower, "denied"), strings.Contains(lower, "forbidden"), strings.Contains(lower, "scope_mismatch"), strings.Contains(lower, "request_identity_required"):
		kind = apperror.KindForbidden
	case strings.Contains(lower, "conflict"), strings.Contains(lower, "stale"), strings.Contains(lower, "idempotency_key_reused"):
		kind = apperror.KindConflict
	case strings.Contains(lower, "not_found"):
		kind = apperror.KindNotFound
	case strings.Contains(lower, "invalid"), strings.Contains(lower, "required") && !strings.Contains(lower, "transaction_required"):
		kind = apperror.KindBadRequest
	}
	return apperror.New(kind, code, err, nil)
}
