package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type operationsLegacyProbe struct {
	operation string
	owner     string
	id        string
	status    string
	limit     int
	err       error
}

func (p *operationsLegacyProbe) IdempotencyReceipts(context.Context, principalmodel.Principal, string, int) ([]idempotency.ReceiptSummary, error) {
	p.operation = "list"
	return []idempotency.ReceiptSummary{{ID: "receipt-1"}}, p.err
}
func (p *operationsLegacyProbe) RetryIdempotencyReceipt(_ context.Context, _ principalmodel.Principal, owner, id string) error {
	p.operation, p.owner, p.id = "retry", owner, id
	return p.err
}
func (p *operationsLegacyProbe) ResetIdempotencyReceipt(_ context.Context, _ principalmodel.Principal, owner, id string) error {
	p.operation, p.owner, p.id = "reset", owner, id
	return p.err
}

func operationsTestAdmin() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "operations.read"}})
}

func TestOperationsDefinitionsReceiptAndReceiptListEdges(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, nil)
	if len(service.Definitions()) == 0 {
		t.Fatal("operations definitions are empty")
	}
	unauthorized := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "viewer"}}
	if _, err := service.Receipt(t.Context(), "receipt-1", unauthorized); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("receipt authorization error = %v", err)
	}
	if _, err := service.Receipts(t.Context(), "", 10, unauthorized); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("receipts authorization error = %v", err)
	}
	workspaceAdminOnOps := operationsTestAdmin()
	accessfixture.Set(&workspaceAdminOnOps, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if _, err := service.Receipts(t.Context(), "", 10, workspaceAdminOnOps); err != nil {
		t.Fatalf("workspace administrator authority must not depend on Surface: %v", err)
	}
	admin := operationsTestAdmin()
	repositoryFailure := errors.New("operations repository failed")
	repository.getErr = repositoryFailure
	if _, err := service.Receipt(t.Context(), "receipt-1", admin); apperror.CodeOf(err) != "backend.operations.read_failed" || !errors.Is(err, repositoryFailure) {
		t.Fatalf("receipt repository error = %v", err)
	}
	repository.getErr = nil
	if _, err := service.Receipt(t.Context(), "missing", admin); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("receipt not-found error = %v", err)
	}
	receipt := operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{ID: "receipt-1", Scope: operationsmodel.OperationsScope{WorkspaceID: admin.WorkspaceID}}}
	repository.receipts["receipt-1"] = receipt
	got, err := service.Receipt(t.Context(), " receipt-1 ", admin)
	if err != nil || got.Command.ID != receipt.Command.ID {
		t.Fatalf("receipt=%#v err=%v", got, err)
	}
	repository.listErr = repositoryFailure
	if _, err := service.Receipts(t.Context(), operationsmodel.OperationsStatusStarted, 0, admin); !errors.Is(err, repositoryFailure) || repository.lastLimit != 100 {
		t.Fatalf("list error=%v limit=%d", err, repository.lastLimit)
	}
	repository.listErr = nil
	items, err := service.Receipts(t.Context(), "", 201, admin)
	if err != nil || len(items) != 1 || repository.lastLimit != 100 {
		t.Fatalf("items=%#v limit=%d err=%v", items, repository.lastLimit, err)
	}
}

func TestOperationsLegacyReceiptCompatibilityEdges(t *testing.T) {
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, nil)
	admin := operationsTestAdmin()
	if _, err := service.LegacyReceipts(t.Context(), admin, "failed", 10); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("legacy list error = %v", err)
	}
	if err := service.RetryLegacyReceipt(t.Context(), admin, "record", "receipt-1"); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("legacy retry error = %v", err)
	}
	if err := service.ResetLegacyReceipt(t.Context(), admin, "record", "receipt-1"); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("legacy reset error = %v", err)
	}
	legacyFailure := errors.New("legacy failed")
	legacy := &operationsLegacyProbe{err: legacyFailure}
	service.legacy = legacy
	if items, err := service.LegacyReceipts(t.Context(), admin, "failed", 10); len(items) != 1 || !errors.Is(err, legacyFailure) || legacy.operation != "list" {
		t.Fatalf("items=%#v operation=%q err=%v", items, legacy.operation, err)
	}
	if err := service.RetryLegacyReceipt(t.Context(), admin, "record", "receipt-1"); !errors.Is(err, legacyFailure) || legacy.operation != "retry" || legacy.owner != "record" || legacy.id != "receipt-1" {
		t.Fatalf("operation=%q owner=%q id=%q err=%v", legacy.operation, legacy.owner, legacy.id, err)
	}
	if err := service.ResetLegacyReceipt(t.Context(), admin, "workflow", "receipt-2"); !errors.Is(err, legacyFailure) || legacy.operation != "reset" || legacy.owner != "workflow" || legacy.id != "receipt-2" {
		t.Fatalf("operation=%q owner=%q id=%q err=%v", legacy.operation, legacy.owner, legacy.id, err)
	}
}

func TestOperationsSubmitSystemValidationAndPersistenceFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 13, 14, 0, time.UTC)
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, func() time.Time { return now }, func() string { return "system" })
	admin := operationsTestAdmin()
	if _, _, err := service.SubmitSystem(t.Context(), OperationsSubmitRequest{Kind: "unknown"}, "key", "purpose", admin); apperror.CodeOf(err) != "backend.operations.kind_not_registered" {
		t.Fatalf("unknown kind error = %v", err)
	}
	request := OperationsSubmitRequest{Kind: "runtime.maintenance.enable", Permission: "workspace.admin", ResourceType: "runtime", ResourceID: "runtime", Reason: "maintenance"}
	mismatch := request
	mismatch.ResourceType = "wrong"
	if _, _, err := service.SubmitSystem(t.Context(), mismatch, "key", "purpose", admin); apperror.CodeOf(err) != "backend.operations.definition_mismatch" {
		t.Fatalf("definition error = %v", err)
	}
	if _, _, err := service.SubmitSystem(t.Context(), request, "key", "", admin); apperror.CodeOf(err) != "backend.operations.system_scope_required" {
		t.Fatalf("scope error = %v", err)
	}
	denied := admin
	accessfixture.Set(&denied, accessfixture.Bundle{})
	if _, _, err := service.SubmitSystem(t.Context(), request, "key", "purpose", denied); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("authorization error = %v", err)
	}
	repositoryFailure := errors.New("register failed")
	repository.registerErr = repositoryFailure
	if _, _, err := service.SubmitSystem(t.Context(), request, "key", " purpose ", admin); apperror.CodeOf(err) != "backend.operations.register_failed" || !errors.Is(err, repositoryFailure) {
		t.Fatalf("register error = %v", err)
	}
	repository.registerErr = nil
	receipt, decision, err := service.SubmitSystem(t.Context(), request, "key", " purpose ", admin)
	if err != nil || decision != operationsmodel.OperationsSubmissionAccepted || receipt.Command.Scope.SystemPurpose != "purpose" || receipt.Command.CreatedAt != now {
		t.Fatalf("receipt=%#v decision=%q err=%v", receipt, decision, err)
	}
}
