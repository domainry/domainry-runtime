package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type operationsRepositoryProbe struct {
	receipts    map[string]operationsmodel.OperationsReceipt
	registerErr error
	getErr      error
	listErr     error
	updateErr   error
	lastLimit   int
	lastFilter  operationsmodel.OperationsReceiptFilter
	searchNil   bool
}

func (p *operationsRepositoryProbe) RegisterOperationsCommand(_ context.Context, receipt operationsmodel.OperationsReceipt) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	if p.registerErr != nil {
		return operationsmodel.OperationsReceipt{}, "", p.registerErr
	}
	key := receipt.Command.Scope.WorkspaceID + ":" + receipt.Command.Scope.SystemPurpose + ":" + receipt.Command.Kind + ":" + receipt.Command.IdempotencyKey
	if existing, found := p.receipts[key]; found {
		return existing, operationspolicy.OperationsClassifySubmission(&existing, receipt.Command), nil
	}
	p.receipts[key] = receipt
	return receipt, operationsmodel.OperationsSubmissionAccepted, nil
}

func (p *operationsRepositoryProbe) GetOperationsReceipt(_ context.Context, scope operationsmodel.OperationsScope, id string) (operationsmodel.OperationsReceipt, bool, error) {
	if p.getErr != nil {
		return operationsmodel.OperationsReceipt{}, false, p.getErr
	}
	for _, receipt := range p.receipts {
		if receipt.Command.ID == id && receipt.Command.Scope.WorkspaceID == scope.WorkspaceID && receipt.Command.Scope.SystemPurpose == scope.SystemPurpose {
			return receipt, true, nil
		}
	}
	return operationsmodel.OperationsReceipt{}, false, nil
}

func (p *operationsRepositoryProbe) ListOperationsReceipts(_ context.Context, scope operationsmodel.OperationsScope, _ operationsmodel.OperationsStatus, limit int) ([]operationsmodel.OperationsReceipt, error) {
	p.lastLimit = limit
	if p.listErr != nil {
		return nil, p.listErr
	}
	result := []operationsmodel.OperationsReceipt{}
	for _, receipt := range p.receipts {
		if receipt.Command.Scope.WorkspaceID == scope.WorkspaceID {
			result = append(result, receipt)
		}
	}
	return result, nil
}

func (p *operationsRepositoryProbe) SearchOperationsReceipts(ctx context.Context, scope operationsmodel.OperationsScope, filter operationsmodel.OperationsReceiptFilter) (operationsmodel.OperationsReceiptPage, error) {
	p.lastFilter = filter
	if p.searchNil {
		return operationsmodel.OperationsReceiptPage{}, nil
	}
	items, err := p.ListOperationsReceipts(ctx, scope, filter.Status, filter.Limit)
	return operationsmodel.OperationsReceiptPage{Items: items, Count: len(items)}, err
}

func (p *operationsRepositoryProbe) UpdateOperationsReceipt(_ context.Context, receipt operationsmodel.OperationsReceipt, expected operationsmodel.OperationsStatus) (bool, error) {
	if p.updateErr != nil {
		return false, p.updateErr
	}
	for key, existing := range p.receipts {
		if existing.Command.ID == receipt.Command.ID && existing.Command.Status == expected {
			p.receipts[key] = receipt
			return true, nil
		}
	}
	return false, nil
}

func TestOperationsSubmitPersistsReplayableWorkspaceReceipt(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	service := NewOperationsApplicationService(repository, nil, func() time.Time { return now }, func() string { return "fixed" })
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	request := OperationsSubmitRequest{Kind: "retention.cleanup", Permission: "workspace.admin", ResourceType: "retention_policy", Reason: "release cleanup", Payload: map[string]any{"mode": "preview"}}

	first, decision, err := service.Submit(t.Context(), request, "backup-1", principal)
	if err != nil || decision != operationsmodel.OperationsSubmissionAccepted || first.Command.ID != "operation_fixed" || first.StatusURL != "/operations/operation_fixed" {
		t.Fatalf("first=%#v decision=%s err=%v", first, decision, err)
	}
	replayed, decision, err := service.Submit(t.Context(), request, "backup-1", principal)
	if err != nil || decision != operationsmodel.OperationsSubmissionReplay || replayed.Command.ID != first.Command.ID {
		t.Fatalf("replay=%#v decision=%s err=%v", replayed, decision, err)
	}
	request.Payload = map[string]any{"mode": "full"}
	if _, decision, err = service.Submit(t.Context(), request, "backup-1", principal); decision != operationsmodel.OperationsSubmissionConflict || apperror.KindOf(err) != apperror.KindConflict {
		t.Fatalf("conflict decision=%s err=%v", decision, err)
	}
}

func TestOperationsSubmitRequiresWorkspaceAndDeclaredPermission(t *testing.T) {
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, nil)
	request := OperationsSubmitRequest{Kind: "retention.cleanup", Permission: "runtime.retention.execute", ResourceType: "retention_policy", Reason: "test"}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{"backup.read"}})
	if _, _, err := service.Submit(t.Context(), request, "key", principal); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("permission error=%v", err)
	}
	accessfixture.Set(&principal, accessfixture.Bundle{Permissions: []string{"runtime.retention.execute"}})
	principal.WorkspaceID = ""
	if _, _, err := service.Submit(t.Context(), request, "key", principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("scope error=%v", err)
	}
}

func TestOperationsSubmitRejectsUnregisteredOrMismatchedDefinitions(t *testing.T) {
	service := NewOperationsApplicationService(&operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}, nil, nil, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if _, _, err := service.Submit(t.Context(), OperationsSubmitRequest{Kind: "arbitrary.sql", Permission: "workspace.admin", ResourceType: "database", Reason: "unsafe"}, "key", principal); apperror.CodeOf(err) != "backend.operations.kind_not_registered" {
		t.Fatalf("unregistered error=%v", err)
	}
	if _, _, err := service.Submit(t.Context(), OperationsSubmitRequest{Kind: "backup.restore", Permission: "workspace.admin", ResourceType: "scheduler_run", Reason: "mismatch"}, "key", principal); apperror.CodeOf(err) != "backend.operations.definition_mismatch" {
		t.Fatalf("mismatch error=%v", err)
	}
}

func TestOperationsSearchReceiptsRequiresExactReadPermissionAndNormalizesFilter(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})

	if _, err := service.SearchReceipts(t.Context(), operationsmodel.OperationsReceiptFilter{}, principal); err != nil {
		t.Fatalf("workspace administrator authority must not depend on Surface: %v", err)
	}
	accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
		role.Permissions = append(role.Permissions, "operations.read")
	})
	page, err := service.SearchReceipts(t.Context(), operationsmodel.OperationsReceiptFilter{
		Kind:        " workflow.process.retry ",
		CreatedFrom: "2026-07-26T08:00:00+08:00",
		CreatedTo:   "2026-07-26T10:00:00+08:00",
		Search:      " failed ",
		Limit:       1000,
	}, principal)
	if err != nil || page.Items == nil {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	if repository.lastFilter.Kind != "workflow.process.retry" || repository.lastFilter.Search != "failed" || repository.lastFilter.Limit != 100 {
		t.Fatalf("normalized filter=%#v", repository.lastFilter)
	}
	if repository.lastFilter.CreatedFrom != "2026-07-26T00:00:00Z" || repository.lastFilter.CreatedTo != "2026-07-26T02:00:00Z" {
		t.Fatalf("utc filter=%#v", repository.lastFilter)
	}
}

func TestOperationsSearchReceiptsRejectsInvalidFiltersAndMapsRepositoryFailure(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}}
	service := NewOperationsApplicationService(repository, nil, nil, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{"operations.read"}})

	cases := []struct {
		filter operationsmodel.OperationsReceiptFilter
		code   string
	}{
		{filter: operationsmodel.OperationsReceiptFilter{Status: "unknown"}, code: "backend.operations.status_invalid"},
		{filter: operationsmodel.OperationsReceiptFilter{FailureClass: "unknown"}, code: "backend.operations.failure_class_invalid"},
		{filter: operationsmodel.OperationsReceiptFilter{CreatedFrom: "not-a-time"}, code: "backend.operations.created_from_invalid"},
		{filter: operationsmodel.OperationsReceiptFilter{CreatedTo: "not-a-time"}, code: "backend.operations.created_to_invalid"},
		{filter: operationsmodel.OperationsReceiptFilter{CreatedFrom: "2026-07-27T00:00:00Z", CreatedTo: "2026-07-26T00:00:00Z"}, code: "backend.operations.created_range_invalid"},
	}
	for _, current := range cases {
		if _, err := service.SearchReceipts(t.Context(), current.filter, principal); apperror.CodeOf(err) != current.code {
			t.Fatalf("filter=%#v code=%s err=%v", current.filter, current.code, err)
		}
	}

	repository.listErr = errors.New("database unavailable")
	if _, err := service.SearchReceipts(t.Context(), operationsmodel.OperationsReceiptFilter{}, principal); apperror.CodeOf(err) != "backend.operations.read_failed" {
		t.Fatalf("repository error=%v", err)
	}
}

func TestOperationsSearchReceiptFilterCoversAuthorizationValidEnumsAndOpenRanges(t *testing.T) {
	repository := &operationsRepositoryProbe{receipts: map[string]operationsmodel.OperationsReceipt{}, searchNil: true}
	service := NewOperationsApplicationService(repository, nil, nil, nil)
	denied := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	if _, err := service.SearchReceipts(t.Context(), operationsmodel.OperationsReceiptFilter{}, denied); err == nil {
		t.Fatal("unauthorized search accepted")
	}
	principal := operationsTestAdmin()
	for _, status := range []operationsmodel.OperationsStatus{
		operationsmodel.OperationsStatusCreated,
		operationsmodel.OperationsStatusStarted,
		operationsmodel.OperationsStatusSucceeded,
		operationsmodel.OperationsStatusFailed,
	} {
		page, err := service.SearchReceipts(t.Context(), operationsmodel.OperationsReceiptFilter{Status: status, Limit: 1}, principal)
		if err != nil || page.Items == nil {
			t.Fatalf("status=%s page=%#v err=%v", status, page, err)
		}
	}
	for _, failureClass := range []operationsmodel.OperationsFailureClass{
		operationsmodel.OperationsFailureRetryable,
		operationsmodel.OperationsFailureTerminal,
		operationsmodel.OperationsFailureManualIntervention,
	} {
		if _, err := service.SearchReceipts(t.Context(), operationsmodel.OperationsReceiptFilter{FailureClass: failureClass, Limit: 1}, principal); err != nil {
			t.Fatalf("failure class=%s err=%v", failureClass, err)
		}
	}
	for _, filter := range []operationsmodel.OperationsReceiptFilter{
		{CreatedFrom: "2026-07-26T00:00:00Z", Limit: 1},
		{CreatedTo: "2026-07-26T00:00:00Z", Limit: 1},
		{CreatedFrom: "2026-07-26T00:00:00Z", CreatedTo: "2026-07-27T00:00:00Z", Limit: 1},
	} {
		if _, err := service.SearchReceipts(t.Context(), filter, principal); err != nil {
			t.Fatalf("open/ordered range=%#v err=%v", filter, err)
		}
	}
}
