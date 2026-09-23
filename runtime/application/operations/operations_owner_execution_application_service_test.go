package operations

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestExecuteOwnerOperationPersistsTerminalReceiptAndNeverRepeatsOwner(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "owner-operation" })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{operationscontract.ActionEnableAutomationRule}})
	readinessCalls := 0
	request := OperationsOwnerExecutionRequest{Kind: "automation.rule.enable", ResourceType: "automation_rule", ResourceID: "rule-1", Reason: "enable validated rule", Key: "enable-1", Payload: map[string]any{"revision": 2}, ReplayReadiness: func(context.Context, any) error { readinessCalls++; return nil }}
	calls := 0
	ownerOperationID := ""
	execute := func(ctx context.Context) (any, error) {
		calls++
		ownerOperationID = requestcontext.OwnerExecutionID(ctx)
		return map[string]any{"rule_id": "rule-1", "status": "enabled"}, nil
	}
	first, err := service.ExecuteOwnerOperation(t.Context(), request, principal, execute)
	if err != nil || first.Receipt.Command.Status != operationsmodel.OperationsStatusSucceeded || first.Replayed || calls != 1 {
		t.Fatalf("first=%+v calls=%d err=%v", first, calls, err)
	}
	if ownerOperationID != first.Receipt.Command.ID {
		t.Fatalf("owner operation id=%q receipt=%q", ownerOperationID, first.Receipt.Command.ID)
	}
	replay, err := service.ExecuteOwnerOperation(t.Context(), request, principal, execute)
	if err != nil || !replay.Replayed || calls != 1 || readinessCalls != 1 {
		t.Fatalf("replay=%+v calls=%d readiness=%d err=%v", replay, calls, readinessCalls, err)
	}
	value, ok := replay.Value.(map[string]any)
	if !ok || value["rule_id"] != "rule-1" {
		t.Fatalf("replayed owner result=%#v", replay.Value)
	}
}

func TestExecuteWorkflowRetryAcceptsRuntimeCapabilityAndReplaysExactlyOnce(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "workflow-retry" })
	request := OperationsOwnerExecutionRequest{
		Kind: "workflow.execution.retry", ResourceType: "workflow_execution", ResourceID: "execution-1",
		Reason: "operator requested retry", Key: "retry-1", ReplayReadiness: func(context.Context, any) error { return nil },
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{"runtime.workflows.retry_ops_workflow_execution"}})
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

func TestExecuteWorkflowRetryRejectsPrincipalWithoutOwnerPermission(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "workflow-denied" })
	request := OperationsOwnerExecutionRequest{Kind: "workflow.execution.retry", ResourceType: "workflow_execution", ResourceID: "execution-1", Reason: "operator requested retry", Key: "retry-1"}
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
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{operationscontract.ActionRetryRuntimePublication}})
	request := OperationsOwnerExecutionRequest{Kind: "runtime.publication.retry", ResourceType: "runtime_publication_outbox", ResourceID: "message-1", Reason: "provider recovered", Key: "retry-1", ReplayReadiness: func(context.Context, any) error { return nil }}
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

func TestExecuteOwnerOperationReplayRechecksPermissionAndCurrentOwnerReadiness(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "guarded-replay" })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{operationscontract.ActionEnableAutomationRule}})
	readinessErr := apperror.New(apperror.KindConflict, "backend.automation.rule_state_changed", nil, nil)
	ready := true
	readinessCalls := 0
	request := OperationsOwnerExecutionRequest{
		Kind: "automation.rule.enable", ResourceType: "automation_rule", ResourceID: "rule-1", Reason: "enable", Key: "enable-guarded",
		ReplayReadiness: func(context.Context, any) error {
			readinessCalls++
			if !ready {
				return readinessErr
			}
			return nil
		},
	}
	executions := 0
	if _, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) {
		executions++
		return map[string]any{"status": "enabled"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	ready = false
	if _, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) {
		executions++
		return nil, nil
	}); apperror.CodeOf(err) != "backend.automation.rule_state_changed" || executions != 1 || readinessCalls != 1 {
		t.Fatalf("readiness replay executions=%d readiness=%d err=%v", executions, readinessCalls, err)
	}
	ready = true
	revoked := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{})
	if _, err := service.ExecuteOwnerOperation(t.Context(), request, revoked, func(context.Context) (any, error) {
		executions++
		return nil, nil
	}); apperror.CodeOf(err) != "auth.permission_denied" || executions != 1 || readinessCalls != 1 {
		t.Fatalf("revoked replay executions=%d readiness=%d err=%v", executions, readinessCalls, err)
	}
}

func TestExecuteOwnerOperationRejectsTerminalReplayWithoutReadinessGuard(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "unguarded-replay" })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{operationscontract.ActionEnableAutomationRule}})
	request := OperationsOwnerExecutionRequest{Kind: "automation.rule.enable", ResourceType: "automation_rule", ResourceID: "rule-1", Reason: "enable", Key: "enable-unguarded"}
	if _, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) { return map[string]any{"status": "enabled"}, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) { t.Fatal("owner mutation repeated"); return nil, nil }); apperror.CodeOf(err) != "backend.operations.replay_readiness_unavailable" {
		t.Fatalf("unguarded replay err=%v", err)
	}
}
