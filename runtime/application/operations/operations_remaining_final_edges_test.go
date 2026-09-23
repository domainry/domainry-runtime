package operations

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

type cancelingLeaseSnapshotProbe struct {
	cancel context.CancelFunc
}

func (p cancelingLeaseSnapshotProbe) OperationsLeaseSnapshot(context.Context, string, time.Time) (operationsmodel.OperationsLeaseSnapshot, error) {
	p.cancel()
	return operationsmodel.OperationsLeaseSnapshot{Live: 1}, nil
}

func (cancelingLeaseSnapshotProbe) ForceReleaseOperationsLease(context.Context, operationsmodel.OperationsLeaseReleaseRequest) (operationsmodel.OperationsLeaseReleaseResult, bool, error) {
	return operationsmodel.OperationsLeaseReleaseResult{}, false, nil
}

func TestOperationsControlFinalAvailabilityDrainAndCancellationEdges(t *testing.T) {
	principal := operationsAdminPrincipal()
	request := OperationsControlRequest{Kind: operationsmodel.OperationsControlMaintenance, Owner: "runtime", Active: true, Reason: "test"}
	operations := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return "control-edge" })
	if _, err := NewOperationsControlApplicationService(nil, operations, nil, nil).Set(t.Context(), request, "nil-repository", principal); apperror.CodeOf(err) != "backend.operations.control_unavailable" {
		t.Fatalf("nil repository error = %v", err)
	}
	if _, err := NewOperationsControlApplicationService(&operationsControlRepositoryProbe{}, nil, nil, nil).Set(t.Context(), request, "nil-operations", principal); apperror.CodeOf(err) != "backend.operations.control_unavailable" {
		t.Fatalf("nil operations error = %v", err)
	}

	for _, active := range []bool{false, true} {
		ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
		service := NewOperationsControlApplicationService(&operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{}}, NewOperationsApplicationService(ledger, nil, nil, func() string { return "drain-condition" }), nil, nil)
		result, err := service.Set(t.Context(), OperationsControlRequest{Kind: operationsmodel.OperationsControlInstanceDrain, Owner: "instance", Active: active, Reason: "test"}, "drain", principal)
		if err != nil || result.DrainSnapshot != nil {
			t.Fatalf("active=%t result=%#v err=%v", active, result, err)
		}
	}
	if action := operationsControlNextAction(operationsmodel.OperationsControl{}, &operationsmodel.OperationsLeaseSnapshot{}); action == "inspect the reported live leases; force release only after expiry or independent stuck verification" {
		t.Fatalf("zero-live drain action = %q", action)
	}

	ctx, cancel := context.WithCancel(t.Context())
	service := NewOperationsControlApplicationService(nil, nil, cancelingLeaseSnapshotProbe{cancel: cancel}, nil)
	service.drainSettleDelay = time.Nanosecond
	service.drainWaitTimeout = time.Second
	if _, err := service.waitForInstanceDrain(ctx, "instance"); err != context.Canceled {
		t.Fatalf("in-loop cancellation error = %v", err)
	}
}

func TestOperationsControlDeadLetterReplayStatusEdges(t *testing.T) {
	principal := operationsAdminPrincipal()
	controlRequest := OperationsControlRequest{Kind: operationsmodel.OperationsControlMaintenance, Owner: "runtime", Active: true, Reason: "test"}
	controlLedger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	controlService := NewOperationsControlApplicationService(&operationsControlRepositoryProbe{controls: map[string]operationsmodel.OperationsControl{}}, NewOperationsApplicationService(controlLedger, nil, nil, func() string { return "control-replay-status" }), nil, nil)
	if _, err := controlService.Set(t.Context(), controlRequest, "control", principal); err != nil {
		t.Fatal(err)
	}
	replaceOperationsReceipt(controlLedger, func(receipt *operationsmodel.OperationsReceipt) {
		receipt.Command.Status = operationsmodel.OperationsStatusCreated
	})
	if _, err := controlService.Set(t.Context(), controlRequest, "control", principal); apperror.CodeOf(err) != "backend.operations.control_revision_conflict" {
		t.Fatalf("control non-succeeded replay error = %v", err)
	}

	deadLedger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	deadSequence := 0
	deadService := NewOperationsApplicationService(deadLedger, nil, nil, func() string {
		deadSequence++
		return fmt.Sprintf("dead-status-%d", deadSequence)
	})
	owner := &bulkDeadLetterOwnerProbe{items: map[string]OperationsDeadLetterItem{"a": {ID: "a", AllowedActions: []string{"ack", "retry"}}}}
	if err := deadService.RegisterDeadLetterOwner("owner", owner); err != nil {
		t.Fatal(err)
	}
	request := OperationsDeadLetterActionRequest{Reason: "test"}
	if _, err := deadService.ActOnDeadLetter(t.Context(), "owner", "a", OperationsDeadLetterAck, request, "ack", principal); err != nil {
		t.Fatal(err)
	}
	if _, err := deadService.ActOnDeadLetter(t.Context(), "owner", "a", OperationsDeadLetterRetry, request, "retry", principal); err != nil {
		t.Fatal(err)
	}
	replaceOperationsReceiptKind(deadLedger, "dead_letter.retry", func(receipt *operationsmodel.OperationsReceipt) {
		receipt.Command.Status = operationsmodel.OperationsStatusCreated
	})
	if _, err := deadService.ActOnDeadLetter(t.Context(), "owner", "a", OperationsDeadLetterRetry, request, "retry", principal); err != nil {
		t.Fatalf("dead-letter non-succeeded replay error = %v", err)
	}
}

func TestOperationsDiagnosticsFinalBoundsSubmissionAndReplayStatusEdges(t *testing.T) {
	principal := operationsAdminPrincipal()
	var nilService *OperationsApplicationService
	if _, err := nilService.CaptureDiagnostics(t.Context(), OperationsDiagnosticsCommand{}, "nil", principal); apperror.CodeOf(err) != "backend.operations.diagnostics_unavailable" {
		t.Fatalf("nil diagnostics service error = %v", err)
	}
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(ledger, nil, nil, func() string { return "diagnostics-final" })
	if err := service.RegisterDiagnostics(&operationsDiagnosticsProbe{}, "runtime"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CaptureDiagnostics(t.Context(), OperationsDiagnosticsCommand{}, "empty", principal); apperror.CodeOf(err) != "backend.operations.diagnostics_section_invalid" {
		t.Fatalf("empty section error = %v", err)
	}
	command := OperationsDiagnosticsCommand{Sections: []string{"db_pool"}, Page: 1, PageSize: 1, Reason: "test"}
	denied := principal
	denied.WorkspaceID = ""
	if _, err := service.CaptureDiagnostics(t.Context(), command, "denied", denied); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("diagnostics submission error = %v", err)
	}
	if _, err := service.CaptureDiagnostics(t.Context(), command, "capture", principal); err != nil {
		t.Fatal(err)
	}
	replaceOperationsReceipt(ledger, func(receipt *operationsmodel.OperationsReceipt) {
		receipt.Command.Status = operationsmodel.OperationsStatusCreated
	})
	if _, err := service.CaptureDiagnostics(t.Context(), command, "capture", principal); err != nil {
		t.Fatalf("diagnostics non-succeeded replay error = %v", err)
	}
}

func TestOperationsLeaseFinalAvailabilityAndReplayStatusEdges(t *testing.T) {
	principal := operationsAdminPrincipal()
	command := OperationsLeaseReleaseCommand{Owner: "worker", ResourceID: "run", ExpectedLeaseOwner: "instance", ExpectedFencingToken: 1, VerifiedStuck: true, Reason: "test"}
	operations := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return "lease-final" })
	if _, err := NewOperationsLeaseApplicationService(nil, operations, nil).ForceRelease(t.Context(), command, "repo", principal); apperror.CodeOf(err) != "backend.operations.lease_control_unavailable" {
		t.Fatalf("nil lease repository error = %v", err)
	}
	if _, err := NewOperationsLeaseApplicationService(&operationsLeaseReleaseRepositoryProbe{}, nil, nil).ForceRelease(t.Context(), command, "operations", principal); apperror.CodeOf(err) != "backend.operations.lease_control_unavailable" {
		t.Fatalf("nil operations service error = %v", err)
	}
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsLeaseApplicationService(&operationsLeaseReleaseRepositoryProbe{changed: true}, NewOperationsApplicationService(ledger, nil, nil, func() string { return "lease-status" }), nil)
	if _, err := service.ForceRelease(t.Context(), command, "release", principal); err != nil {
		t.Fatal(err)
	}
	replaceOperationsReceipt(ledger, func(receipt *operationsmodel.OperationsReceipt) {
		receipt.Command.Status = operationsmodel.OperationsStatusCreated
	})
	if _, err := service.ForceRelease(t.Context(), command, "release", principal); err != nil {
		t.Fatalf("lease non-succeeded replay error = %v", err)
	}
}

func TestOperationsOwnerExecutionFinalDefinitionAndReceiptEdges(t *testing.T) {
	principal := operationsAdminPrincipal()
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return "owner-final" })
	if _, err := service.ExecuteOwnerOperation(t.Context(), OperationsOwnerExecutionRequest{Kind: "unknown", ResourceType: "unknown"}, principal, func(context.Context) (any, error) { return nil, nil }); apperror.CodeOf(err) != "backend.operations.definition_mismatch" {
		t.Fatalf("unknown owner operation error = %v", err)
	}
	if _, err := service.ExecuteOwnerOperation(t.Context(), OperationsOwnerExecutionRequest{Kind: "workflow.execution.retry", ResourceType: "wrong"}, principal, func(context.Context) (any, error) { return nil, nil }); apperror.CodeOf(err) != "backend.operations.definition_mismatch" {
		t.Fatalf("resource mismatch error = %v", err)
	}

	request := OperationsOwnerExecutionRequest{Kind: "workflow.execution.retry", ResourceType: "workflow_execution", ResourceID: "execution", Reason: "test", Key: "empty-result", ReplayReadiness: func(context.Context, any) error { return nil }}
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service = NewOperationsApplicationService(ledger, nil, nil, func() string { return "owner-result" })
	if _, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	replaceOperationsReceipt(ledger, func(receipt *operationsmodel.OperationsReceipt) { receipt.Result = nil })
	if result, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) { t.Fatal("owner repeated"); return nil, nil }); err != nil || result.Value != nil {
		t.Fatalf("empty result replay=%#v err=%v", result, err)
	}
	replaceOperationsReceipt(ledger, func(receipt *operationsmodel.OperationsReceipt) { receipt.Result = []byte("{") })
	if _, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) { return nil, nil }); apperror.CodeOf(err) != "backend.operations.receipt_result_invalid" {
		t.Fatalf("malformed owner result error = %v", err)
	}

	createdRequest := request
	createdRequest.Key = "created-replay"
	if _, _, err := service.Submit(t.Context(), OperationsSubmitRequest{Kind: createdRequest.Kind, ResourceType: createdRequest.ResourceType, ResourceID: createdRequest.ResourceID, Reason: createdRequest.Reason}, createdRequest.Key, principal); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteOwnerOperation(t.Context(), createdRequest, principal, func(context.Context) (any, error) { return nil, nil }); err != nil {
		t.Fatalf("created replay execution error = %v", err)
	}

	unknownStatusRequest := request
	unknownStatusRequest.Key = "unknown-status"
	if _, _, err := service.Submit(t.Context(), OperationsSubmitRequest{Kind: unknownStatusRequest.Kind, ResourceType: unknownStatusRequest.ResourceType, ResourceID: unknownStatusRequest.ResourceID, Reason: unknownStatusRequest.Reason}, unknownStatusRequest.Key, principal); err != nil {
		t.Fatal(err)
	}
	for key, receipt := range ledger.receipts {
		if receipt.Command.IdempotencyKey == unknownStatusRequest.Key {
			receipt.Command.Status = ""
			ledger.receipts[key] = receipt
		}
	}
	calls := 0
	if _, err := service.ExecuteOwnerOperation(t.Context(), unknownStatusRequest, principal, func(context.Context) (any, error) { calls++; return nil, nil }); apperror.CodeOf(err) != "backend.operations.receipt_status_invalid" || calls != 0 {
		t.Fatalf("unknown status calls=%d error=%v", calls, err)
	}
}
