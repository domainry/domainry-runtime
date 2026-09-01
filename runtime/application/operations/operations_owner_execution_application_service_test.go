package operations

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestExecuteOwnerOperationPersistsTerminalReceiptAndNeverRepeatsOwner(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "owner-operation" })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{"runtime.scheduler.retry_ops_scheduler_run"}})
	request := OperationsOwnerExecutionRequest{Kind: "scheduler.run.retry", ResourceType: "scheduler_run", ResourceID: "run-1", Reason: "recover failed run", Key: "retry-1", Payload: map[string]any{"attempt": 2}}
	calls := 0
	execute := func(context.Context) (any, error) {
		calls++
		return map[string]any{"run_id": "run-1", "status": "pending"}, nil
	}
	first, err := service.ExecuteOwnerOperation(t.Context(), request, principal, execute)
	if err != nil || first.Receipt.Command.Status != operationsmodel.OperationsStatusSucceeded || first.Replayed || calls != 1 {
		t.Fatalf("first=%+v calls=%d err=%v", first, calls, err)
	}
	replay, err := service.ExecuteOwnerOperation(t.Context(), request, principal, execute)
	if err != nil || !replay.Replayed || calls != 1 {
		t.Fatalf("replay=%+v calls=%d err=%v", replay, calls, err)
	}
	value, ok := replay.Value.(map[string]any)
	if !ok || value["run_id"] != "run-1" {
		t.Fatalf("replayed owner result=%#v", replay.Value)
	}
}

func TestExecuteSchedulerJobRunAcceptsPublicCapabilityAndReplaysExactlyOnce(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "scheduler-manual-run" })
	request := OperationsOwnerExecutionRequest{
		Kind: "scheduler.job.run", ResourceType: "scheduler_definition", ResourceID: "activation-job",
		Reason: "operator requested activation", Key: "manual-run-1",
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{"runtime.scheduler.run_ops_scheduler_job"}})
	calls := 0
	execute := func(context.Context) (any, error) {
		calls++
		return map[string]any{"run_id": "run-1", "status": "succeeded"}, nil
	}
	first, err := service.ExecuteOwnerOperation(t.Context(), request, principal, execute)
	if err != nil || first.Replayed || calls != 1 || first.Receipt.Command.Status != operationsmodel.OperationsStatusSucceeded {
		t.Fatalf("first=%+v calls=%d err=%v", first, calls, err)
	}
	replay, err := service.ExecuteOwnerOperation(t.Context(), request, principal, execute)
	if err != nil || !replay.Replayed || calls != 1 || replay.Receipt.Command.ID != first.Receipt.Command.ID {
		t.Fatalf("replay=%+v calls=%d err=%v", replay, calls, err)
	}
}

func TestExecuteSchedulerJobRunRejectsPrincipalWithoutOwnerPermission(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "scheduler-denied" })
	request := OperationsOwnerExecutionRequest{Kind: "scheduler.job.run", ResourceType: "scheduler_definition", ResourceID: "activation-job", Reason: "operator requested activation", Key: "manual-run-1"}
	calls := 0
	_, err := service.ExecuteOwnerOperation(t.Context(), request, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "viewer"}}, func(context.Context) (any, error) {
		calls++
		return nil, nil
	})
	if apperror.CodeOf(err) != "auth.permission_denied" || calls != 0 || len(repository.receipts) != 0 {
		t.Fatalf("calls=%d receipts=%d err=%v", calls, len(repository.receipts), err)
	}
}

func TestExecuteOwnerOperationRecordsFailureWithoutBlindReplay(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "failed-owner-operation" })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{"integration.retry"}})
	request := OperationsOwnerExecutionRequest{Kind: "integration.event.retry", ResourceType: "integration_event", ResourceID: "event-1", Reason: "provider recovered", Key: "retry-1"}
	calls := 0
	execute := func(context.Context) (any, error) {
		calls++
		return nil, apperror.New(apperror.KindUnavailable, "backend.integration.provider_unavailable", nil, nil)
	}
	first, err := service.ExecuteOwnerOperation(t.Context(), request, principal, execute)
	if apperror.CodeOf(err) != "backend.integration.provider_unavailable" || first.Receipt.Command.Status != operationsmodel.OperationsStatusFailed || first.Receipt.FailureClass != operationsmodel.OperationsFailureRetryable {
		t.Fatalf("failure=%+v err=%v", first, err)
	}
	if _, err := service.ExecuteOwnerOperation(t.Context(), request, principal, execute); apperror.CodeOf(err) != "backend.integration.provider_unavailable" || calls != 1 {
		t.Fatalf("failed replay calls=%d err=%v", calls, err)
	}
}
