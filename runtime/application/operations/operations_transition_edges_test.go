package operations

import (
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func newStartedOperationsReceipt(t *testing.T) (*OperationsApplicationService, *operationsRepositoryProbe, operationsmodel.OperationsReceipt, principalmodel.SystemScope) {
	t.Helper()
	now := time.Date(2026, 7, 19, 15, 16, 17, 0, time.UTC)
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, func() time.Time { return now }, func() string { return "transition" })
	request := OperationsSubmitRequest{Kind: "retention.cleanup", ResourceType: "retention_policy", ResourceID: "policy-1", Reason: "cleanup"}
	receipt, _, err := service.Submit(t.Context(), request, "key", operationsAdminPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "operation transition test")
	receipt, err = service.Start(t.Context(), receipt.Command.ID, receipt.Command.Scope, scope)
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, receipt, scope
}

func TestOperationsSubmitAndStartFailureBoundaries(t *testing.T) {
	admin := operationsAdminPrincipal()
	request := OperationsSubmitRequest{Kind: "retention.cleanup", ResourceType: "retention_policy", Reason: "cleanup"}
	var nilService *OperationsApplicationService
	if _, _, err := nilService.submit(t.Context(), request, "runtime.operations.run_lifecycle_cleanup_job", "key", admin.UserID, operationsmodel.OperationsScope{WorkspaceID: admin.WorkspaceID}); apperror.CodeOf(err) != "backend.operations.repository_unavailable" {
		t.Fatalf("nil repository error = %v", err)
	}
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, nil)
	invalidPayload := request
	invalidPayload.Payload = make(chan int)
	if _, _, err := service.Submit(t.Context(), invalidPayload, "key", admin); apperror.CodeOf(err) != "backend.operations.payload_invalid" {
		t.Fatalf("payload error = %v", err)
	}
	invalidCommand := request
	invalidCommand.Reason = ""
	if _, _, err := service.Submit(t.Context(), invalidCommand, "", admin); apperror.KindOf(err) != apperror.KindBadRequest {
		t.Fatalf("command validation error = %v", err)
	}
	systemRequest := OperationsSubmitRequest{Kind: "runtime.maintenance.enable", ResourceType: "runtime", Reason: "maintenance"}
	if _, _, err := service.Submit(t.Context(), systemRequest, "key", admin); apperror.CodeOf(err) != "backend.operations.system_entrypoint_required" {
		t.Fatalf("workspace entrypoint error = %v", err)
	}
	if _, _, err := service.SubmitSystem(t.Context(), request, "key", "purpose", admin); apperror.CodeOf(err) != "backend.operations.system_scope_required" {
		t.Fatalf("system entrypoint error = %v", err)
	}

	if _, err := service.Start(t.Context(), "missing", operationsmodel.OperationsScope{WorkspaceID: admin.WorkspaceID}, principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("start scope error = %v", err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "start edge")
	repository := service.repository.(*operationsRepositoryProbe)
	repository.getErr = errors.New("read failed")
	if _, err := service.Start(t.Context(), "missing", operationsmodel.OperationsScope{WorkspaceID: admin.WorkspaceID}, scope); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("start read error = %v", err)
	}
	repository.getErr = nil
	if _, err := service.Start(t.Context(), "missing", operationsmodel.OperationsScope{WorkspaceID: admin.WorkspaceID}, scope); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("start not-found error = %v", err)
	}
}

func TestOperationsTransitionUpdateConflictAndSuccess(t *testing.T) {
	service, repository, receipt, scope := newStartedOperationsReceipt(t)
	created := receipt
	created.Command.Status = operationsmodel.OperationsStatusCreated
	created.Command.StartedAt = nil
	for key := range repository.receipts {
		repository.receipts[key] = created
	}
	updateFailure := errors.New("transition update failed")
	repository.updateErr = updateFailure
	if _, err := service.Start(t.Context(), created.Command.ID, created.Command.Scope, scope); apperror.CodeOf(err) != "backend.operations.transition_failed" || !errors.Is(err, updateFailure) {
		t.Fatalf("update error = %v", err)
	}
	repository.updateErr = nil
	for key := range repository.receipts {
		conflict := repository.receipts[key]
		conflict.Command.Status = operationsmodel.OperationsStatusStarted
		repository.receipts[key] = conflict
	}
	if _, err := service.Start(t.Context(), created.Command.ID, created.Command.Scope, scope); apperror.CodeOf(err) != "backend.operations.transition_conflict" {
		t.Fatalf("transition conflict = %v", err)
	}
	for key := range repository.receipts {
		repository.receipts[key] = created
	}
	started, err := service.Start(t.Context(), created.Command.ID, created.Command.Scope, scope)
	if err != nil || started.Command.Status != operationsmodel.OperationsStatusStarted || started.Command.StartedAt == nil {
		t.Fatalf("started=%#v err=%v", started, err)
	}
}

func TestOperationsFinishDefaultsValidationPersistenceAndConflict(t *testing.T) {
	service, repository, receipt, scope := newStartedOperationsReceipt(t)
	if _, err := service.Finish(t.Context(), receipt, principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("finish scope error = %v", err)
	}
	if _, err := service.Finish(t.Context(), receipt, scope); apperror.CodeOf(err) != "backend.operations.terminal_status_required" {
		t.Fatalf("terminal status error = %v", err)
	}
	invalid := receipt
	invalid.Command.Status = operationsmodel.OperationsStatusSucceeded
	invalid.StatusURL = ""
	if _, err := service.Finish(t.Context(), invalid, scope); apperror.KindOf(err) != apperror.KindBadRequest {
		t.Fatalf("receipt validation error = %v", err)
	}
	terminal := receipt
	terminal.Command.Status = operationsmodel.OperationsStatusSucceeded
	updateFailure := errors.New("finish update failed")
	repository.updateErr = updateFailure
	if _, err := service.Finish(t.Context(), terminal, scope); apperror.CodeOf(err) != "backend.operations.finish_failed" || !errors.Is(err, updateFailure) {
		t.Fatalf("finish update error = %v", err)
	}
	repository.updateErr = nil
	for key := range repository.receipts {
		delete(repository.receipts, key)
	}
	if _, err := service.Finish(t.Context(), terminal, scope); apperror.CodeOf(err) != "backend.operations.transition_conflict" {
		t.Fatalf("finish conflict = %v", err)
	}
	service, _, receipt, scope = newStartedOperationsReceipt(t)
	receipt.Command.Status = operationsmodel.OperationsStatusSucceeded
	finished, err := service.Finish(t.Context(), receipt, scope)
	if err != nil || finished.Command.FinishedAt == nil || finished.NextAction == "" || len(finished.RelatedIDs) != 1 || finished.Correlation != receipt.Command.ID || len(finished.Evidence) != 1 {
		t.Fatalf("finished=%#v err=%v", finished, err)
	}
}
