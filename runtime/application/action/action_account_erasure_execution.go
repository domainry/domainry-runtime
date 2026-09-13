package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func cloneAccountErasureCapability(value *runtimeext.AccountErasureCapability) *runtimeext.AccountErasureCapability {
	if value == nil {
		return nil
	}
	copy := *value
	copy.Operations = append([]runtimeext.AccountErasureOperation(nil), value.Operations...)
	return &copy
}

func (e *businessActionExecution) accountErasureGranted(operation runtimeext.AccountErasureOperation) bool {
	if e == nil || e.accountErasureGrant == nil {
		return false
	}
	for _, granted := range e.accountErasureGrant.Operations {
		if granted == operation {
			return true
		}
	}
	return false
}

func (e *businessActionExecution) lockAccountErasureRecord(ctx context.Context, object, id string) (recordmodel.Record, error) {
	if e.targetGrant == nil || !e.targetResolved || e.targetOrganization.ID == "" {
		return recordmodel.Record{}, apperror.New(apperror.KindBadRequest, "backend.action.target_organization_unresolved", nil, nil)
	}
	if e.dependencies.GetRecordForUpdate == nil {
		return recordmodel.Record{}, missingExecutorPort("get_account_erasure_record_for_update")
	}
	principal := actionReadEffectAuthorizationPrincipal(e.invocation.Principal, e.action.EffectSet, e.action, object)
	record, err := e.dependencies.GetRecordForUpdate(ctx, object, id, principal)
	if err != nil {
		return recordmodel.Record{}, err
	}
	if strings.TrimSpace(record.ID) != id || record.OwnerOrgID != e.targetOrganization.ID || record.UpdatedAt == "" {
		return recordmodel.Record{}, apperror.New(apperror.KindForbidden, "backend.account_erasure.record_scope_denied", nil, nil)
	}
	return record, nil
}

func (e *businessActionExecution) StageAccountErasure(ctx context.Context, request runtimeext.AccountErasureStageRequest) (runtimeext.AccountErasureReceipt, error) {
	if !e.accountErasureGranted(runtimeext.AccountErasureStage) {
		return runtimeext.AccountErasureReceipt{}, apperror.New(apperror.KindForbidden, "backend.account_erasure.operation_denied", nil, nil)
	}
	if e.accountErasureCalls != 0 || e.identityDeliveryCalls != 0 {
		return runtimeext.AccountErasureReceipt{}, apperror.New(apperror.KindConflict, "backend.account_erasure.already_used", nil, nil)
	}
	request.ProfileID, request.ApprovalID = strings.TrimSpace(request.ProfileID), strings.TrimSpace(request.ApprovalID)
	if request.ProfileID == "" || request.ApprovalID == "" || request.ExpectedIdentityVersion < 1 || request.ExpectedBindingVersion < 1 {
		return runtimeext.AccountErasureReceipt{}, apperror.New(apperror.KindBadRequest, "backend.account_erasure.request_invalid", nil, nil)
	}
	if e.dependencies.StageApprovedAccountErasure == nil || e.dependencies.ResolveProfileBindingField == nil {
		return runtimeext.AccountErasureReceipt{}, missingExecutorPort("stage_account_erasure")
	}
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.AccountErasureReceipt{}, err
	}
	grant := e.accountErasureGrant
	if e.identity.ObjectKey == grant.RequestObjectKey && e.identity.RecordID != "" && e.identity.RecordID != request.ApprovalID {
		return runtimeext.AccountErasureReceipt{}, apperror.New(apperror.KindForbidden, "backend.account_erasure.action_request_mismatch", nil, nil)
	}
	profile, err := e.lockAccountErasureRecord(txCtx, grant.ProfileBinding.ObjectKey, request.ProfileID)
	if err != nil {
		return runtimeext.AccountErasureReceipt{}, err
	}
	approval, err := e.lockAccountErasureRecord(txCtx, grant.RequestObjectKey, request.ApprovalID)
	if err != nil {
		return runtimeext.AccountErasureReceipt{}, err
	}
	relation, found := e.dependencies.ResolveProfileBindingField(grant.ProfileBinding.BindingKey, grant.ProfileBinding.ObjectKey)
	if !found || relation == "" {
		return runtimeext.AccountErasureReceipt{}, missingExecutorPort("account_erasure_profile_metadata")
	}
	subjectID := strings.TrimSpace(toStringOrEmpty(profile.Data[relation]))
	if subjectID == "" || toStringOrEmpty(approval.Data[grant.RequestProfileField]) != request.ProfileID ||
		toStringOrEmpty(approval.Data[grant.RequestRequesterField]) != subjectID || subjectID == e.invocation.Principal.UserID {
		return runtimeext.AccountErasureReceipt{}, apperror.New(apperror.KindForbidden, "backend.account_erasure.subject_request_required", nil, nil)
	}
	delivery, err := e.bindIdentityHandlerDelivery(txCtx)
	if err != nil {
		return runtimeext.AccountErasureReceipt{}, err
	}
	selector := &runtimeext.IdentityHandlerProfileBindingSelector{BindingKey: grant.ProfileBinding.BindingKey, ObjectKey: grant.ProfileBinding.ObjectKey, ProfileID: request.ProfileID}
	bound, err := e.resolveBoundIdentityWithDelivery(txCtx, delivery, subjectID, selector)
	if err != nil {
		return runtimeext.AccountErasureReceipt{}, err
	}
	if bound.OrganizationID != e.targetOrganization.ID || bound.Version != request.ExpectedIdentityVersion || bound.ProfileBinding.Version != request.ExpectedBindingVersion || !bound.Active {
		return runtimeext.AccountErasureReceipt{}, apperror.New(apperror.KindConflict, "backend.account_erasure.identity_version_conflict", nil, nil)
	}
	digest := sha256.Sum256([]byte(e.workspace.ID + "\x00" + e.identity.ExecutionID))
	requestID := "account-erasure-" + hex.EncodeToString(digest[:16])
	trusted := lifecyclecontract.AccountErasureApproval{WorkspaceID: e.workspace.ID, RequestID: requestID, SubjectID: subjectID,
		RequestedBy: subjectID, ApprovedBy: e.invocation.Principal.UserID, OwnerOrgID: e.targetOrganization.ID,
		ActionKey: e.action.Key, ApprovalID: request.ApprovalID, BindingKey: grant.ProfileBinding.BindingKey, ObjectKey: grant.ProfileBinding.ObjectKey, ProfileID: request.ProfileID}
	// Queue and disable join the same outer Action transaction. Neither can be
	// observed if its business obligations or the eventual commit fail.
	e.accountErasureCalls++
	queued, err := e.dependencies.StageApprovedAccountErasure(txCtx, trusted)
	if err != nil {
		return runtimeext.AccountErasureReceipt{}, normalizeIdentityCapabilityError(err, "backend.account_erasure.stage_failed")
	}
	result, err := delivery.DeliverIdentity(txCtx, identitysdk.HandlerDeliveryRequest{ContractVersion: identitysdk.HandlerDeliveryContractVersionV1,
		AccessToken: e.requestIdentity.AccessToken, IdempotencyKey: requestID + ":disable", User: identitysdk.HandlerUserMutation{Operation: identitysdk.HandlerUserDisable,
			User: identitysdk.User{ID: subjectID, OrgID: e.targetOrganization.ID}, ExpectedVersion: request.ExpectedIdentityVersion},
		ProfileBinding: &identitysdk.HandlerProfileBindingMutation{BindingKey: grant.ProfileBinding.BindingKey, ObjectKey: grant.ProfileBinding.ObjectKey,
			ProfileID: request.ProfileID, ExpectedVersion: request.ExpectedBindingVersion, ApprovalID: request.ApprovalID, Reason: "approved account erasure"}})
	if err != nil {
		return runtimeext.AccountErasureReceipt{}, normalizeIdentityCapabilityError(err, "backend.account_erasure.disable_failed")
	}
	if result.User.ID != subjectID || result.User.OrgID != e.targetOrganization.ID || result.User.Status != "disabled" || queued.ID != requestID || queued.WorkspaceID != e.workspace.ID {
		return runtimeext.AccountErasureReceipt{}, apperror.New(apperror.KindInternal, "backend.account_erasure.source_receipt_mismatch", nil, nil)
	}
	e.accountErasureOK = true
	return accountErasureReceipt(queued), nil
}

func (e *businessActionExecution) GetAccountErasure(ctx context.Context, request runtimeext.AccountErasureGetRequest) (runtimeext.AccountErasureReceipt, error) {
	if !e.accountErasureGranted(runtimeext.AccountErasureGet) {
		return runtimeext.AccountErasureReceipt{}, apperror.New(apperror.KindForbidden, "backend.account_erasure.operation_denied", nil, nil)
	}
	request.RequestID, request.ProfileID = strings.TrimSpace(request.RequestID), strings.TrimSpace(request.ProfileID)
	if request.RequestID == "" || request.ProfileID == "" {
		return runtimeext.AccountErasureReceipt{}, apperror.New(apperror.KindBadRequest, "backend.account_erasure.request_invalid", nil, nil)
	}
	if e.dependencies.GetAccountErasure == nil {
		return runtimeext.AccountErasureReceipt{}, missingExecutorPort("get_account_erasure")
	}
	// The erased profile retains its stable record and organization. Receipt
	// authorization uses source provenance rather than its cleared user relation.
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.AccountErasureReceipt{}, err
	}
	grant := e.accountErasureGrant
	if _, err := e.lockAccountErasureRecord(txCtx, grant.ProfileBinding.ObjectKey, request.ProfileID); err != nil {
		return runtimeext.AccountErasureReceipt{}, err
	}
	queued, err := e.dependencies.GetAccountErasure(txCtx, lifecyclecontract.AccountErasureReference{WorkspaceID: e.workspace.ID, RequestID: request.RequestID,
		OwnerOrgID: e.targetOrganization.ID, BindingKey: grant.ProfileBinding.BindingKey, ObjectKey: grant.ProfileBinding.ObjectKey, ProfileID: request.ProfileID})
	if err != nil {
		return runtimeext.AccountErasureReceipt{}, normalizeIdentityCapabilityError(err, "backend.account_erasure.receipt_denied")
	}
	if queued.ID != request.RequestID || queued.WorkspaceID != e.workspace.ID {
		return runtimeext.AccountErasureReceipt{}, apperror.New(apperror.KindInternal, "backend.account_erasure.source_receipt_mismatch", nil, nil)
	}
	return accountErasureReceipt(queued), nil
}

func accountErasureReceipt(request lifecyclemodel.SubjectRequest) runtimeext.AccountErasureReceipt {
	return runtimeext.AccountErasureReceipt{RequestID: request.ID, Status: string(request.Status), Completed: request.Status == lifecyclemodel.SubjectRequestSucceeded,
		BackupPending: request.BackupPending, ExecutionAttempt: request.ExecutionAttempt, UpdatedAt: request.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}
