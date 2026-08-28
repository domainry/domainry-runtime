package deployment

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type idempotencyOperationsProbe struct {
	changed bool
	cleanup chan deploymentmodel.IdempotencyCleanupRequest
	calls   int
}

func (p *idempotencyOperationsProbe) IdempotencyOperationalStatus(context.Context, string, time.Time) (deploymentmodel.IdempotencyOperationalStatus, error) {
	p.calls++
	return deploymentmodel.IdempotencyOperationalStatus{Backlog: map[string]int{}}, nil
}

func (p *idempotencyOperationsProbe) IdempotencyOperationalStatusForSystem(context.Context, principalmodel.SystemScope, time.Time) (deploymentmodel.IdempotencyOperationalStatus, error) {
	p.calls++
	return deploymentmodel.IdempotencyOperationalStatus{Backlog: map[string]int{}}, nil
}

func (*idempotencyOperationsProbe) Ping(context.Context) error { return nil }
func (*idempotencyOperationsProbe) MigrationStatus(context.Context) (deploymentmodel.MigrationStatus, error) {
	return deploymentmodel.MigrationStatus{}, nil
}
func (p *idempotencyOperationsProbe) ListIdempotencyReceipts(context.Context, string, string, int) ([]idempotency.ReceiptSummary, error) {
	p.calls++
	return []idempotency.ReceiptSummary{{Owner: "record", ID: "receipt-1"}}, nil
}
func (p *idempotencyOperationsProbe) RetryIdempotencyReceipt(context.Context, string, string, string) (bool, error) {
	p.calls++
	return p.changed, nil
}
func (p *idempotencyOperationsProbe) ResetIdempotencyReceipt(context.Context, string, string, string) (bool, error) {
	p.calls++
	return p.changed, nil
}
func (p *idempotencyOperationsProbe) RunIdempotencyCleanup(_ context.Context, request deploymentmodel.IdempotencyCleanupRequest) (deploymentmodel.IdempotencyCleanupResult, error) {
	p.calls++
	if p.cleanup != nil {
		p.cleanup <- request
	}
	return deploymentmodel.IdempotencyCleanupResult{Acquired: true, LeaseOwner: request.LeaseOwner, Deleted: request.BatchSize}, nil
}

func TestDeploymentApplicationAuthorizesWorkspaceBeforeRepositoryAccess(t *testing.T) {
	probe := &idempotencyOperationsProbe{}
	service := NewDeploymentRuntimeStatusApplicationService("template", "v1", nil, nil, probe, nil, nil, nil, nil)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if _, err := service.IdempotencyReceipts(t.Context(), principal, "", 10); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("receipts error=%v", err)
	}
	if err := service.RetryIdempotencyReceipt(t.Context(), principal, "record", "receipt-1"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("retry error=%v", err)
	}
	if _, err := service.IdempotencyOperationalStatus(t.Context(), ""); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("operational status error=%v", err)
	}
	if _, err := service.IdempotencyOperationalStatusForSystem(t.Context(), principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("system operational status error=%v", err)
	}
	if _, err := service.ProcessIdempotencyCleanup(t.Context(), "runtime", 10, time.Now(), principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("cleanup error=%v", err)
	}
	if probe.calls != 0 {
		t.Fatalf("repository called before scope authorization: %d", probe.calls)
	}
}

func TestIdempotencyOperationsRequireWorkspaceAdminAndMutableReceipt(t *testing.T) {
	probe := &idempotencyOperationsProbe{}
	service := NewDeploymentRuntimeStatusApplicationService("template", "v1", nil, nil, probe, nil, nil, nil, nil)
	if _, err := service.IdempotencyReceipts(t.Context(), principalmodel.Principal{}, "", 10); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("anonymous list error=%v", err)
	}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if receipts, err := service.IdempotencyReceipts(t.Context(), admin, "", 10); err != nil || len(receipts) != 1 {
		t.Fatalf("admin receipts=%#v err=%v", receipts, err)
	}
	if err := service.ResetIdempotencyReceipt(t.Context(), admin, "record", "receipt-1"); apperror.KindOf(err) != apperror.KindConflict {
		t.Fatalf("immutable reset error=%v", err)
	}
	probe.changed = true
	if err := service.RetryIdempotencyReceipt(t.Context(), admin, "record", "receipt-1"); err != nil {
		t.Fatal(err)
	}
}

func TestIdempotencyCleanupWorkerRunsImmediatelyAndStopsWithContext(t *testing.T) {
	probe := &idempotencyOperationsProbe{cleanup: make(chan deploymentmodel.IdempotencyCleanupRequest, 1)}
	service := NewDeploymentRuntimeStatusApplicationService("template", "v1", nil, nil, probe, nil, nil, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	done := service.StartIdempotencyCleanupWorker(ctx, time.Hour, 17)
	select {
	case request := <-probe.cleanup:
		if request.BatchSize != 17 || request.LeaseOwner == "" {
			t.Fatalf("cleanup request=%#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not run immediately")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not stop")
	}
}

func TestProcessIdempotencyCleanupDelegatesLeaseOwnerAndBatch(t *testing.T) {
	probe := &idempotencyOperationsProbe{}
	service := NewDeploymentRuntimeStatusApplicationService("template", "v1", nil, nil, probe, nil, nil, nil, nil)
	result, err := service.ProcessIdempotencyCleanup(t.Context(), "runtime-a", 25, time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test cleanup"))
	if err != nil || !result.Acquired || result.LeaseOwner != "runtime-a" || result.Deleted != 25 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}
