package action

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func accountErasureTestExecution(t *testing.T) (*businessActionExecution, *identityHandlerDeliveryStub, map[string]recordmodel.Record, *lifecyclecontract.AccountErasureApproval) {
	t.Helper()
	delivery := &identityHandlerDeliveryStub{result: identitysdk.HandlerDeliveryResult{User: identitysdk.User{ID: "subject-1", OrgID: "store-north", Status: "disabled"}},
		resolveResult: identitysdk.HandlerBoundIdentity{UserID: "subject-1", OrganizationID: "store-north", Active: true, Version: 4,
			ProfileBinding: &identitysdk.HandlerProfileBinding{BindingKey: "employee", ObjectKey: "employee_profile", ProfileID: "profile-1", IdentityUserID: "subject-1", Status: "active", Version: 7}}}
	e := identityDeliveryTestExecution(t, delivery)
	e.identityGrant = nil
	e.action.Key = "erase_request.approve"
	e.action.EffectSet.Read = append(e.action.EffectSet.Read, definitionmodel.ActionObjectEffect{ObjectKey: "erase_request"})
	e.accountErasureGrant = &runtimeext.AccountErasureCapability{Operations: []runtimeext.AccountErasureOperation{runtimeext.AccountErasureStage, runtimeext.AccountErasureGet},
		ProfileBinding: runtimeext.IdentityProfileBindingCapability{BindingKey: "employee", ObjectKey: "employee_profile"}, RequestObjectKey: "erase_request", RequestProfileField: "profile_id", RequestRequesterField: "requested_by"}
	records := map[string]recordmodel.Record{
		"employee_profile": {ID: "profile-1", OwnerOrgID: "store-north", UpdatedAt: "now", Data: map[string]any{"identity_user_id": "subject-1"}},
		"erase_request":    {ID: "approval-1", OwnerOrgID: "store-north", UpdatedAt: "now", Data: map[string]any{"profile_id": "profile-1", "requested_by": "subject-1"}},
	}
	e.dependencies.GetRecordForUpdate = func(ctx context.Context, object, id string, _ principalmodel.Principal) (recordmodel.Record, error) {
		if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
			t.Fatal("account erasure read was outside Action transaction")
		}
		return records[object], nil
	}
	approval := &lifecyclecontract.AccountErasureApproval{}
	e.dependencies.StageApprovedAccountErasure = func(ctx context.Context, trusted lifecyclecontract.AccountErasureApproval) (lifecyclemodel.SubjectRequest, error) {
		if ctx.Value(actionUnitOfWorkTransactionContextKey{}) != true {
			t.Fatal("queue was outside Action transaction")
		}
		*approval = trusted
		return lifecyclemodel.SubjectRequest{ID: trusted.RequestID, WorkspaceID: trusted.WorkspaceID, Status: lifecyclemodel.SubjectRequestApproved, UpdatedAt: time.Now()}, nil
	}
	t.Cleanup(func() { e.unitOfWork.rollBack(t.Context()) })
	return e, delivery, records, approval
}

func accountErasureStageFixture() runtimeext.AccountErasureStageRequest {
	return runtimeext.AccountErasureStageRequest{ProfileID: "profile-1", ApprovalID: "approval-1", ExpectedIdentityVersion: 4, ExpectedBindingVersion: 7}
}

func TestAccountErasureStagesTrustedSubjectAndDisablesInSameActionTransaction(t *testing.T) {
	e, delivery, _, approval := accountErasureTestExecution(t)
	receipt, err := e.StageAccountErasure(t.Context(), accountErasureStageFixture())
	if err != nil || receipt.Completed || receipt.Status != "approved" || approval.RequestID == "" || receipt.RequestID != approval.RequestID {
		t.Fatalf("receipt=%+v approval=%+v err=%v", receipt, approval, err)
	}
	if approval.SubjectID != "subject-1" || approval.RequestedBy != "subject-1" || approval.ApprovedBy != "actor-1" || approval.WorkspaceID != "workspace-a" || approval.OwnerOrgID != "store-north" || approval.ApprovalID != "approval-1" {
		t.Fatalf("untrusted provenance=%+v", approval)
	}
	if delivery.calls != 1 || delivery.request.User.Operation != identitysdk.HandlerUserDisable || delivery.request.User.User.ID != "subject-1" || delivery.request.AccessToken != "trusted-token" || delivery.request.ProfileBinding.ExpectedVersion != 7 {
		t.Fatalf("disable request=%+v", delivery.request)
	}
	if err := e.validateCapabilityCompletion(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.StageAccountErasure(t.Context(), accountErasureStageFixture()); apperror.CodeOf(err) != "backend.account_erasure.already_used" {
		t.Fatal(err)
	}
}

func TestAccountErasureRejectsForeignIntentScopeAndStaleBindingBeforeQueueing(t *testing.T) {
	for name, mutate := range map[string]func(*businessActionExecution, *identityHandlerDeliveryStub, map[string]recordmodel.Record){
		"undeclared operation": func(e *businessActionExecution, _ *identityHandlerDeliveryStub, _ map[string]recordmodel.Record) {
			e.accountErasureGrant.Operations = []runtimeext.AccountErasureOperation{runtimeext.AccountErasureGet}
		},
		"foreign organization": func(_ *businessActionExecution, _ *identityHandlerDeliveryStub, records map[string]recordmodel.Record) {
			row := records["erase_request"]
			row.OwnerOrgID = "another-store"
			records["erase_request"] = row
		},
		"another profile": func(_ *businessActionExecution, _ *identityHandlerDeliveryStub, records map[string]recordmodel.Record) {
			records["erase_request"].Data["profile_id"] = "profile-other"
		},
		"invented requester": func(_ *businessActionExecution, _ *identityHandlerDeliveryStub, records map[string]recordmodel.Record) {
			records["erase_request"].Data["requested_by"] = "other-subject"
		},
		"self approval": func(e *businessActionExecution, _ *identityHandlerDeliveryStub, _ map[string]recordmodel.Record) {
			e.invocation.Principal.UserID = "subject-1"
		},
		"missing bearer": func(e *businessActionExecution, _ *identityHandlerDeliveryStub, _ map[string]recordmodel.Record) {
			e.requestIdentity.AccessToken = ""
		},
		"stale Identity": func(_ *businessActionExecution, delivery *identityHandlerDeliveryStub, _ map[string]recordmodel.Record) {
			delivery.resolveResult.Version++
		},
		"stale binding": func(_ *businessActionExecution, delivery *identityHandlerDeliveryStub, _ map[string]recordmodel.Record) {
			delivery.resolveResult.ProfileBinding.Version++
		},
		"foreign bound Identity": func(_ *businessActionExecution, delivery *identityHandlerDeliveryStub, _ map[string]recordmodel.Record) {
			delivery.resolveResult.OrganizationID = "another-store"
		},
		"another Action record": func(e *businessActionExecution, _ *identityHandlerDeliveryStub, _ map[string]recordmodel.Record) {
			e.identity.ObjectKey = "erase_request"
			e.identity.RecordID = "approval-other"
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, delivery, records, approval := accountErasureTestExecution(t)
			mutate(e, delivery, records)
			if _, err := e.StageAccountErasure(t.Context(), accountErasureStageFixture()); err == nil {
				t.Fatal("unsafe stage accepted")
			}
			if approval.RequestID != "" || delivery.calls != 0 {
				t.Fatalf("unsafe stage reached queue/disable: %+v calls=%d", approval, delivery.calls)
			}
		})
	}
}

func TestAccountErasureDisableFailureCannotBeIgnoredByProjectHandler(t *testing.T) {
	e, delivery, _, approval := accountErasureTestExecution(t)
	delivery.err = errors.New("disable persistence failure")
	if _, err := e.StageAccountErasure(t.Context(), accountErasureStageFixture()); err == nil {
		t.Fatal("failed disable accepted")
	}
	if approval.RequestID == "" {
		t.Fatal("fixture did not stage queue first")
	}
	if err := e.validateCapabilityCompletion(nil); apperror.CodeOf(err) != "backend.account_erasure.incomplete" {
		t.Fatalf("ignored error can commit queue: %v", err)
	}
}

func TestAccountErasureReceiptUsesExactProfileProvenanceAfterRelationCleared(t *testing.T) {
	for _, status := range []lifecyclemodel.SubjectRequestStatus{lifecyclemodel.SubjectRequestApproved, lifecyclemodel.SubjectRequestFailed, lifecyclemodel.SubjectRequestSucceeded} {
		t.Run(string(status), func(t *testing.T) {
			e, _, records, _ := accountErasureTestExecution(t)
			records["employee_profile"].Data["identity_user_id"] = nil
			e.dependencies.GetAccountErasure = func(_ context.Context, reference lifecyclecontract.AccountErasureReference) (lifecyclemodel.SubjectRequest, error) {
				if reference.WorkspaceID != "workspace-a" || reference.OwnerOrgID != "store-north" || reference.ProfileID != "profile-1" || reference.BindingKey != "employee" || reference.ObjectKey != "employee_profile" {
					t.Fatalf("reference=%+v", reference)
				}
				return lifecyclemodel.SubjectRequest{ID: reference.RequestID, WorkspaceID: reference.WorkspaceID, Status: status, BackupPending: true}, nil
			}
			receipt, err := e.GetAccountErasure(t.Context(), runtimeext.AccountErasureGetRequest{RequestID: "queued-1", ProfileID: "profile-1"})
			if err != nil || receipt.Completed != (status == lifecyclemodel.SubjectRequestSucceeded) || !receipt.BackupPending {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
		})
	}
}
