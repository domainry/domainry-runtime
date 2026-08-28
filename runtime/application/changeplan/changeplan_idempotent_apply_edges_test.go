package changeplan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type changePlanOperationRepositoryFake struct {
	changePlanDraftRepositoryFake
	claim       changeplanmodel.ChangePlanOperationClaimResult
	claimErr    error
	completeErr error
	failErr     error
	completed   int
	failed      int
}

func (f *changePlanOperationRepositoryFake) TryBeginOperation(context.Context, string, changeplanmodel.ChangePlanOperationClaimRequest) (changeplanmodel.ChangePlanOperationClaimResult, error) {
	return f.claim, f.claimErr
}

func (f *changePlanOperationRepositoryFake) CompleteOperation(_ context.Context, _ string, completion changeplanmodel.ChangePlanOperationCompletion) (changeplanmodel.ChangePlanOperationExecution, error) {
	f.completed++
	return changeplanmodel.ChangePlanOperationExecution{ID: completion.ExecutionID}, f.completeErr
}

func (f *changePlanOperationRepositoryFake) FailOperation(_ context.Context, _ string, failure changeplanmodel.ChangePlanOperationFailure) (changeplanmodel.ChangePlanOperationExecution, error) {
	f.failed++
	return changeplanmodel.ChangePlanOperationExecution{ID: failure.ExecutionID}, f.failErr
}

func acquiredChangePlanClaim() changeplanmodel.ChangePlanOperationClaimResult {
	return changeplanmodel.ChangePlanOperationClaimResult{Decision: idempotency.DecisionAcquired, Execution: changeplanmodel.ChangePlanOperationExecution{ID: "execution-1", WorkspaceID: "workspace-1", IdempotencyKey: "request-1", RequestFingerprint: "fingerprint", LeaseOwner: "owner", FencingToken: 1}}
}

func TestChangePlanApplyIdempotentInputAndClaimDecisions(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)
	principal := changePlanAdmin()
	service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, nil, &changePlanAuditFake{}, nil)
	missingWorkspace := principal
	missingWorkspace.WorkspaceID = ""
	if _, _, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key", snapshot, graph, missingWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error = %v", err)
	}
	if _, _, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key", snapshot, graph, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown principal error = %v", err)
	}

	if _, _, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key", snapshot, graph, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("missing operation repository error = %v", err)
	}
	if _, _, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, " ", snapshot, graph, principal); apperror.CodeOf(err) != idempotency.ErrorCodeMissingKey {
		t.Fatalf("missing key error = %v", err)
	}
	denied := principal
	denied = changePlanWithoutPermissions(denied)
	if _, _, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key", snapshot, graph, denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error = %v", err)
	}
	empty := plan
	empty.PlanID = " "
	if _, _, err := service.ApplyIdempotent(t.Context(), empty, "", "key", snapshot, graph, principal); apperror.CodeOf(err) != "backend.change_plan.plan_id_required" {
		t.Fatalf("plan ID error = %v", err)
	}
	if _, _, err := service.ApplyIdempotent(t.Context(), plan, "wrong", "key", snapshot, graph, principal); apperror.CodeOf(err) != "backend.change_plan.confirmation_invalid" {
		t.Fatalf("confirmation error = %v", err)
	}

	wantErr := errors.New("claim failed")
	repository := &changePlanOperationRepositoryFake{claimErr: wantErr}
	service = NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, nil)
	if _, _, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key", snapshot, graph, principal); !errors.Is(err, wantErr) {
		t.Fatalf("claim error = %v", err)
	}

	for _, test := range []struct {
		decision idempotency.Decision
		code     string
	}{
		{idempotency.DecisionFingerprintConflict, idempotency.ErrorCodeKeyReused},
		{idempotency.DecisionInProgress, idempotency.ErrorCodeInProgress},
		{idempotency.Decision("unknown"), "backend.internal"},
	} {
		repository.claimErr = nil
		repository.claim = acquiredChangePlanClaim()
		repository.claim.Decision = test.decision
		if _, _, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key", snapshot, graph, principal); apperror.CodeOf(err) != test.code {
			t.Fatalf("decision %q error = %v", test.decision, err)
		}
	}
}

func TestChangePlanApplyIdempotentReplaySuccessAndError(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)
	repository := &changePlanOperationRepositoryFake{claim: acquiredChangePlanClaim()}
	repository.claim.Decision = idempotency.DecisionReplay
	service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, nil)

	receipt := changePlanApplyReceipt{Result: BusinessChangePlanApplyResult{PlanID: plan.PlanID, Status: "applied", SchemaHash: "schema"}}
	repository.claim.Execution.Result, _ = json.Marshal(receipt)
	result, replayed, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key", snapshot, graph, changePlanAdmin())
	if err != nil || !replayed || result.SchemaHash != "schema" {
		t.Fatalf("replay result=%+v replayed=%v err=%v", result, replayed, err)
	}
	receipt = changePlanApplyReceipt{ErrorKind: apperror.KindConflict, ErrorCode: "replayed.conflict", ErrorParams: map[string]string{"plan": plan.PlanID}}
	repository.claim.Execution.Result, _ = json.Marshal(receipt)
	if _, replayed, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key", snapshot, graph, changePlanAdmin()); !replayed || apperror.CodeOf(err) != "replayed.conflict" {
		t.Fatalf("replay error replayed=%v err=%v", replayed, err)
	}
	repository.claim.Execution.Result = json.RawMessage("bad")
	if _, _, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key", snapshot, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("malformed replay error = %v", err)
	}
}

func TestChangePlanApplyIdempotentAcquiredCompletionAndFailure(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)
	metadata := &changePlanMetadataFake{}
	runtime := &changePlanRuntimeFake{reloadHash: "schema"}
	repository := &changePlanOperationRepositoryFake{claim: acquiredChangePlanClaim()}
	audit := &changePlanAuditFake{}
	service := NewChangePlanApplicationService(repository, metadata, audit, runtime)

	explicitOwner := changePlanAdmin()
	explicitOwner.RequestID = "request-explicit"
	result, replayed, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key", snapshot, graph, explicitOwner)
	if err != nil || replayed || result.Status != "applied" || repository.completed != 1 {
		t.Fatalf("acquired result=%+v replayed=%v err=%v completed=%d", result, replayed, err, repository.completed)
	}
	repository.completeErr = errors.New("complete failed")
	if _, _, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key-2", snapshot, graph, changePlanAdmin()); !errors.Is(err, repository.completeErr) {
		t.Fatalf("complete error = %v", err)
	}

	invalid := plan
	invalid.SnapshotHash = "stale"
	repository.completeErr = nil
	if _, _, err := service.ApplyIdempotent(t.Context(), invalid, invalid.PlanID, "key-3", snapshot, graph, changePlanAdmin()); err == nil || repository.failed != 1 {
		t.Fatalf("terminal apply error=%v failed=%d", err, repository.failed)
	}
	repository.failErr = errors.New("fail receipt failed")
	if _, _, err := service.ApplyIdempotent(t.Context(), invalid, invalid.PlanID, "key-4", snapshot, graph, changePlanAdmin()); !errors.Is(err, repository.failErr) {
		t.Fatalf("fail operation error = %v", err)
	}
	repository.failErr = nil
	runtime.reloadErr = errors.New("reload failed")
	if _, _, err := service.ApplyIdempotent(t.Context(), plan, plan.PlanID, "key-5", snapshot, graph, changePlanAdmin()); err == nil || repository.failed < 2 {
		t.Fatalf("retryable apply error=%v failed=%d", err, repository.failed)
	}
}

func TestChangePlanIdempotencyReceiptAndAuditHelpers(t *testing.T) {
	plan := BusinessSystemChangePlan{PlanID: "plan"}
	if fingerprint, err := changePlanApplyFingerprint(plan, "plan"); err != nil || len(fingerprint) != 64 {
		t.Fatalf("fingerprint=%q err=%v", fingerprint, err)
	}
	receipt := changePlanReceiptForError(conflict("conflict", "plan", "one"))
	if receipt.ErrorKind != apperror.KindConflict || receipt.ErrorCode != "conflict" || receipt.ErrorParams["plan"] != "one" {
		t.Fatalf("receipt = %+v", receipt)
	}
	service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, nil, &changePlanAuditFake{}, nil)
	service.auditChangePlanIdempotency(t.Context(), plan, changePlanAdmin(), changeplanmodel.ChangePlanOperationClaimResult{}, "ignored")
	claim := acquiredChangePlanClaim()
	service.auditChangePlanIdempotency(t.Context(), plan, changePlanAdmin(), claim, "replayed")
	service.audit = nil
	service.auditChangePlanIdempotency(t.Context(), plan, changePlanAdmin(), claim, "ignored")
	plain := changePlanReceiptForError(errors.New("plain"))
	if plain.ErrorKind != apperror.KindInternal || plain.ErrorParams != nil {
		t.Fatalf("plain receipt=%+v", plain)
	}
	if appErr := changePlanError(apperror.KindBadRequest, "empty-param", nil, "", "value").(*apperror.AppError); len(appErr.Params) != 0 {
		t.Fatalf("empty param error=%+v", appErr)
	}
	invalid := plan
	invalid.Items = []changeplanmodel.BusinessSystemChangeItem{{After: json.RawMessage("{")}}
	if _, err := changePlanApplyFingerprint(invalid, invalid.PlanID); err == nil {
		t.Fatal("invalid raw JSON fingerprint must fail")
	}
	invalidService := NewChangePlanApplicationService(&changePlanOperationRepositoryFake{}, nil, nil, nil)
	if _, _, err := invalidService.ApplyIdempotent(t.Context(), invalid, invalid.PlanID, "invalid-key", changePlanTestSnapshot(), ReferenceGraph{}, changePlanAdmin()); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("invalid fingerprint apply error=%v", err)
	}
}
