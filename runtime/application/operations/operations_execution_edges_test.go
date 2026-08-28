package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type operationsUpdateFailureProbe struct {
	*operationsRepositoryProbe
	failAt int
	calls  int
}

func (p *operationsUpdateFailureProbe) UpdateOperationsReceipt(ctx context.Context, receipt operationsmodel.OperationsReceipt, expected operationsmodel.OperationsStatus) (bool, error) {
	p.calls++
	if p.calls == p.failAt {
		return false, errors.New("update failed")
	}
	return p.operationsRepositoryProbe.UpdateOperationsReceipt(ctx, receipt, expected)
}

func operationsAdminPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
}

func replaceOperationsReceipt(repository *operationsRepositoryProbe, mutate func(*operationsmodel.OperationsReceipt)) {
	for key, receipt := range repository.receipts {
		mutate(&receipt)
		repository.receipts[key] = receipt
		return
	}
}

func TestOperationsDiagnosticsRegistrationValidationAndCaptureFailures(t *testing.T) {
	var nilService *OperationsApplicationService
	if err := nilService.RegisterDiagnostics(&operationsDiagnosticsProbe{}, "runtime-1"); apperror.CodeOf(err) != "backend.operations.diagnostics_registration_invalid" {
		t.Fatalf("nil registration err=%v", err)
	}
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return "diagnostics-edge" })
	if err := service.RegisterDiagnostics(nil, "runtime-1"); apperror.CodeOf(err) != "backend.operations.diagnostics_registration_invalid" {
		t.Fatalf("nil repository err=%v", err)
	}
	if err := service.RegisterDiagnostics(&operationsDiagnosticsProbe{}, " "); apperror.CodeOf(err) != "backend.operations.diagnostics_registration_invalid" {
		t.Fatalf("empty instance err=%v", err)
	}
	if _, err := service.CaptureDiagnostics(t.Context(), OperationsDiagnosticsCommand{}, "key", operationsAdminPrincipal()); apperror.CodeOf(err) != "backend.operations.diagnostics_unavailable" {
		t.Fatalf("unavailable err=%v", err)
	}
	probe := &operationsDiagnosticsProbe{err: errOperationsDiagnosticsProbe}
	if err := service.RegisterDiagnostics(probe, " runtime-1 "); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CaptureDiagnostics(t.Context(), OperationsDiagnosticsCommand{Sections: []string{"db_pool", "db_pool"}, Reason: "capture"}, "key", operationsAdminPrincipal()); apperror.CodeOf(err) != "backend.operations.diagnostics_capture_failed" {
		t.Fatalf("capture err=%v", err)
	}
	if probe.request.InstanceID != "runtime-1" || probe.request.Page != 1 || probe.request.PageSize != 20 || len(probe.request.Sections) != 1 {
		t.Fatalf("normalized request=%#v", probe.request)
	}
}

func TestOperationsDiagnosticsReplayAndTransitionFailures(t *testing.T) {
	principal := operationsAdminPrincipal()
	command := OperationsDiagnosticsCommand{Sections: []string{"db_pool"}, Reason: "capture"}
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "diagnostics-replay" })
	if err := service.RegisterDiagnostics(&operationsDiagnosticsProbe{}, "runtime-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CaptureDiagnostics(t.Context(), command, "key", principal); err != nil {
		t.Fatal(err)
	}
	replaceOperationsReceipt(repository, func(receipt *operationsmodel.OperationsReceipt) { receipt.Result = []byte("{") })
	if _, err := service.CaptureDiagnostics(t.Context(), command, "key", principal); apperror.CodeOf(err) != "backend.operations.diagnostics_receipt_invalid" {
		t.Fatalf("replay err=%v", err)
	}

	for _, test := range []struct {
		name   string
		failAt int
		code   string
	}{
		{"start", 1, "backend.operations.transition_failed"},
		{"finish", 2, "backend.operations.finish_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ledger := &operationsUpdateFailureProbe{operationsRepositoryProbe: &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, failAt: test.failAt}
			service := NewOperationsApplicationService(ledger, nil, nil, func() string { return test.name })
			if err := service.RegisterDiagnostics(&operationsDiagnosticsProbe{}, "runtime-1"); err != nil {
				t.Fatal(err)
			}
			if _, err := service.CaptureDiagnostics(t.Context(), command, "key", principal); apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestOperationsLeaseUnavailableRepositoryAndReplayFailures(t *testing.T) {
	principal := operationsAdminPrincipal()
	command := OperationsLeaseReleaseCommand{Owner: " workflow ", ResourceID: " run-1 ", ExpectedLeaseOwner: " worker-a ", ExpectedFencingToken: 4, VerifiedStuck: true, VerificationEvidence: " expired ", Reason: "recover"}
	var nilService *OperationsLeaseApplicationService
	if _, err := nilService.ForceRelease(t.Context(), command, "key", principal); apperror.CodeOf(err) != "backend.operations.lease_control_unavailable" {
		t.Fatalf("unavailable err=%v", err)
	}
	ledger := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	operations := NewOperationsApplicationService(ledger, nil, nil, func() string { return "lease-edge" })
	repository := &operationsLeaseReleaseRepositoryProbe{err: errOperationsLeaseProbe}
	service := NewOperationsLeaseApplicationService(repository, operations, func() time.Time { return time.Unix(10, 0) })
	if _, err := service.ForceRelease(t.Context(), command, "key", principal); apperror.CodeOf(err) != "backend.operations.lease_release_precondition_failed" || !errors.Is(err, errOperationsLeaseProbe) {
		t.Fatalf("release err=%v", err)
	}
	if repository.request.Owner != "workflow" || repository.request.ResourceID != "run-1" || repository.request.ExpectedLeaseOwner != "worker-a" || repository.request.VerificationEvidence != "expired" {
		t.Fatalf("request=%#v", repository.request)
	}

	ledger = &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	operations = NewOperationsApplicationService(ledger, nil, nil, func() string { return "lease-replay" })
	service = NewOperationsLeaseApplicationService(&operationsLeaseReleaseRepositoryProbe{changed: true}, operations, nil)
	if _, err := service.ForceRelease(t.Context(), command, "key", principal); err != nil {
		t.Fatal(err)
	}
	replaceOperationsReceipt(ledger, func(receipt *operationsmodel.OperationsReceipt) { receipt.Result = []byte("{") })
	if _, err := service.ForceRelease(t.Context(), command, "key", principal); apperror.CodeOf(err) != "backend.operations.lease_receipt_invalid" {
		t.Fatalf("replay err=%v", err)
	}
}

func TestOperationsLeaseTransitionAndAuthorizationFailures(t *testing.T) {
	command := OperationsLeaseReleaseCommand{Owner: "workflow", ResourceID: "run-1", ExpectedLeaseOwner: "worker-a", ExpectedFencingToken: 4, VerifiedStuck: true, Reason: "recover"}
	for _, test := range []struct {
		name      string
		failAt    int
		principal principalmodel.Principal
		code      string
	}{
		{"authorization", 0, principalmodel.Principal{}, "backend.workspace_scope_required"},
		{"start", 1, operationsAdminPrincipal(), "backend.operations.transition_failed"},
		{"finish", 2, operationsAdminPrincipal(), "backend.operations.finish_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ledger := &operationsUpdateFailureProbe{operationsRepositoryProbe: &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, failAt: test.failAt}
			operations := NewOperationsApplicationService(ledger, nil, nil, func() string { return test.name })
			service := NewOperationsLeaseApplicationService(&operationsLeaseReleaseRepositoryProbe{changed: true}, operations, nil)
			if _, err := service.ForceRelease(t.Context(), command, "key", test.principal); apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestExecuteOwnerOperationRejectsRunningReplayWithoutRepeatingOwner(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, func() string { return "running-owner" })
	principal := operationsAdminPrincipal()
	request := OperationsOwnerExecutionRequest{Kind: "scheduler.run.retry", ResourceType: "job_run", ResourceID: "run-1", Reason: "recover", Key: "retry", Payload: map[string]any{"attempt": 2}}
	receipt, _, err := service.Submit(t.Context(), OperationsSubmitRequest{Kind: request.Kind, Permission: "workspace.admin", ResourceType: request.ResourceType, ResourceID: request.ResourceID, Reason: request.Reason, Payload: request.Payload}, request.Key, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(t.Context(), receipt.Command.ID, receipt.Command.Scope, operationsOwnerSystemScope()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	result, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) { calls++; return nil, nil })
	if apperror.CodeOf(err) != "backend.operations.in_progress" || !result.Replayed || result.Receipt.Command.Status != operationsmodel.OperationsStatusStarted || calls != 0 {
		t.Fatalf("result=%#v calls=%d err=%v", result, calls, err)
	}
}

func TestExecuteOwnerOperationValidationMarshalAndPersistenceFailures(t *testing.T) {
	principal := operationsAdminPrincipal()
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, func() string { return "owner-edge" })
	if _, err := service.ExecuteOwnerOperation(t.Context(), OperationsOwnerExecutionRequest{Kind: "backup.restore", ResourceType: "job_run"}, principal, func(context.Context) (any, error) { return nil, nil }); apperror.CodeOf(err) != "backend.operations.definition_mismatch" {
		t.Fatalf("definition err=%v", err)
	}
	request := OperationsOwnerExecutionRequest{Kind: "scheduler.run.retry", ResourceType: "job_run", ResourceID: "run-1", Reason: "recover", Key: "retry"}
	if _, err := service.ExecuteOwnerOperation(t.Context(), request, principalmodel.Principal{}, func(context.Context) (any, error) { return nil, nil }); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	result, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) { return make(chan int), nil })
	if apperror.CodeOf(err) != "backend.operations.owner_result_invalid" || result.Receipt.Command.Status != operationsmodel.OperationsStatusFailed || result.Receipt.FailureClass != operationsmodel.OperationsFailureRetryable {
		t.Fatalf("marshal result=%#v err=%v", result, err)
	}

	for _, test := range []struct {
		name   string
		failAt int
		code   string
	}{
		{"start", 1, "backend.operations.transition_failed"},
		{"finish", 2, "backend.operations.finish_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ledger := &operationsUpdateFailureProbe{operationsRepositoryProbe: &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, failAt: test.failAt}
			service := NewOperationsApplicationService(ledger, nil, nil, func() string { return test.name })
			if _, err := service.ExecuteOwnerOperation(t.Context(), request, principal, func(context.Context) (any, error) { return map[string]any{"ok": true}, nil }); apperror.CodeOf(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestOperationsOwnerPermissionAndFailureClassEdges(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{Permissions: []string{"integration.retry"}})
	if permission := operationsOwnerPermission([]string{"scheduler.run", "integration.retry"}, principal); permission != "integration.retry" {
		t.Fatalf("permission=%q", permission)
	}
	if permission := operationsOwnerPermission([]string{"scheduler.run"}, principal); permission != "scheduler.run" {
		t.Fatalf("fallback=%q", permission)
	}
	for kind, expected := range map[apperror.ErrorKind]operationsmodel.OperationsFailureClass{
		apperror.KindConflict:   operationsmodel.OperationsFailureManualIntervention,
		apperror.KindBadRequest: operationsmodel.OperationsFailureTerminal,
		apperror.KindInternal:   operationsmodel.OperationsFailureRetryable,
	} {
		if actual := operationsOwnerFailureClass(kind); actual != expected {
			t.Fatalf("kind=%s class=%s", kind, actual)
		}
	}
}
