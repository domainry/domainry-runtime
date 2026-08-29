package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type integrationFixedClock struct{ now time.Time }

func (c integrationFixedClock) Now() time.Time { return c.now }

type integrationReconciliationMarkResult struct {
	fact    integrationmodel.IntegrationInvocation
	changed bool
	err     error
}

type integrationReconciliationProbe struct {
	*independentDeliveryRepository
	mu          sync.Mutex
	due         []integrationmodel.IntegrationInvocation
	listErr     error
	listCalls   int
	listScope   principalmodel.SystemScope
	listLimit   int
	staleBefore string
	markResults map[string]integrationReconciliationMarkResult
	markCalls   []string
	detectedAt  []string
	listed      chan struct{}
	listOnce    sync.Once
}

func newIntegrationReconciliationProbe() *integrationReconciliationProbe {
	return &integrationReconciliationProbe{
		independentDeliveryRepository: &independentDeliveryRepository{},
		markResults:                   map[string]integrationReconciliationMarkResult{},
		listed:                        make(chan struct{}),
	}
}

func (p *integrationReconciliationProbe) ListPreparedInvocationsForReconciliation(_ context.Context, scope principalmodel.SystemScope, limit int, staleBefore string) ([]integrationmodel.IntegrationInvocation, error) {
	p.mu.Lock()
	p.listCalls++
	p.listScope, p.listLimit, p.staleBefore = scope, limit, staleBefore
	due, err := append([]integrationmodel.IntegrationInvocation(nil), p.due...), p.listErr
	p.mu.Unlock()
	p.listOnce.Do(func() { close(p.listed) })
	return due, err
}

func (p *integrationReconciliationProbe) MarkInvocationReconciliationRequired(_ context.Context, workspaceID, invocationID, detectedAt string) (integrationmodel.IntegrationInvocation, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.markCalls = append(p.markCalls, workspaceID+":"+invocationID)
	p.detectedAt = append(p.detectedAt, detectedAt)
	result := p.markResults[invocationID]
	return result.fact, result.changed, result.err
}

func (p *integrationReconciliationProbe) snapshot() (int, principalmodel.SystemScope, int, string, []string, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.listCalls, p.listScope, p.listLimit, p.staleBefore, append([]string(nil), p.markCalls...), append([]string(nil), p.detectedAt...)
}

type integrationDeliveryWithoutReconciliation struct {
	integrationrepository.IntegrationDeliveryRepository
}

type integrationAcknowledgementProbe struct {
	*independentDeliveryRepository
	due       []integrationmodel.IntegrationOutboxMessage
	marked    integrationmodel.IntegrationOutboxMessage
	markCalls int
	listErr   error
	markErr   error
	changed   *bool
}

func (p *integrationAcknowledgementProbe) ListOverdueOutboxAcknowledgements(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationOutboxMessage, error) {
	return append([]integrationmodel.IntegrationOutboxMessage(nil), p.due...), p.listErr
}

func (p *integrationAcknowledgementProbe) MarkOutboxAcknowledgementReconciliationRequired(context.Context, string, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	p.markCalls++
	changed := true
	if p.changed != nil {
		changed = *p.changed
	}
	return p.marked, changed, p.markErr
}

type integrationAcknowledgementOnlyProbe struct {
	integrationrepository.IntegrationDeliveryRepository
	probe *integrationAcknowledgementProbe
}

func (p integrationAcknowledgementOnlyProbe) ListOverdueOutboxAcknowledgements(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]integrationmodel.IntegrationOutboxMessage, error) {
	return p.probe.ListOverdueOutboxAcknowledgements(ctx, scope, limit, now)
}

func (p integrationAcknowledgementOnlyProbe) MarkOutboxAcknowledgementReconciliationRequired(ctx context.Context, workspaceID, id, now string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	return p.probe.MarkOutboxAcknowledgementReconciliationRequired(ctx, workspaceID, id, now)
}

func integrationReconciliationScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "integration receipt reconciliation test")
}

func assertIntegrationWorkerStopped(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	default:
		t.Fatal("worker was not stopped")
	}
}

func TestStartInvocationReconciliationWorkerGuardsAndRunsImmediateTick(t *testing.T) {
	scope := integrationReconciliationScope()
	service := NewIntegrationApplicationService(ApplicationDependencies{})
	assertIntegrationWorkerStopped(t, service.StartInvocationReconciliationWorker(t.Context(), time.Hour, time.Minute, 10, principalmodel.SystemScope{}))

	service = NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: integrationDeliveryWithoutReconciliation{}})
	assertIntegrationWorkerStopped(t, service.StartInvocationReconciliationWorker(t.Context(), time.Hour, time.Minute, 10, scope))

	now := time.Date(2026, 7, 20, 8, 30, 0, 0, time.UTC)
	probe := newIntegrationReconciliationProbe()
	probe.due = []integrationmodel.IntegrationInvocation{{ID: "invocation-1", WorkspaceID: "workspace", ConnectorKey: "payments", Status: "prepared", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339)}}
	probe.markResults["invocation-1"] = integrationReconciliationMarkResult{
		fact:    integrationmodel.IntegrationInvocation{ID: "invocation-1", WorkspaceID: "workspace", ConnectorKey: "payments", Status: "reconciliation_required"},
		changed: true,
	}
	audited := make(chan struct{}, 1)
	controller := workerplatform.NewController()
	service = NewIntegrationApplicationService(ApplicationDependencies{
		DeliveryRepository: probe,
		Worker:             workerplatform.Dependencies{Clock: integrationFixedClock{now: now}, Control: controller},
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, _ map[string]any) {
			if event == "integration_invocation_reconciliation_required" {
				audited <- struct{}{}
			}
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := service.StartInvocationReconciliationWorker(ctx, 0, 0, 0, scope)
	select {
	case <-audited:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciliation worker did not run its immediate tick")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciliation worker did not stop after cancellation")
	}
	listCalls, gotScope, limit, staleBefore, markCalls, detectedAt := probe.snapshot()
	if listCalls != 1 || gotScope != scope || limit != 100 || staleBefore != now.Add(-defaultInvocationReconciliationAge).Format(time.RFC3339) {
		t.Fatalf("list calls=%d scope=%#v limit=%d staleBefore=%q", listCalls, gotScope, limit, staleBefore)
	}
	if len(markCalls) != 1 || markCalls[0] != "workspace:invocation-1" || len(detectedAt) != 1 || detectedAt[0] != now.Format(time.RFC3339) {
		t.Fatalf("mark calls=%#v detectedAt=%#v", markCalls, detectedAt)
	}
}

func TestReconcileOverdueOutboxAcknowledgementWithoutInvocationRepository(t *testing.T) {
	now := time.Date(2026, 7, 20, 8, 30, 0, 0, time.UTC)
	probe := &integrationAcknowledgementProbe{
		independentDeliveryRepository: &independentDeliveryRepository{},
		due: []integrationmodel.IntegrationOutboxMessage{{
			ID: "outbox-1", WorkspaceID: "workspace", ConnectorKey: "callback-connector", ConnectionKey: "primary",
			Operation: "submit", Status: "sent", AckDeadlineAt: now.Add(-time.Second).Format(time.RFC3339),
		}},
		marked: integrationmodel.IntegrationOutboxMessage{
			ID: "outbox-1", WorkspaceID: "workspace", ConnectorKey: "callback-connector", ConnectionKey: "primary",
			Operation: "submit", Status: "quarantined", Error: "backend.integration.outbox.ack_timeout",
		},
	}
	audited := ""
	service := NewIntegrationApplicationService(ApplicationDependencies{
		DeliveryRepository: probe,
		Worker:             workerplatform.Dependencies{Clock: integrationFixedClock{now: now}},
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, _ map[string]any) {
			audited = event
		},
	})
	result, err := service.ReconcileMissingIntegrationReceipts(t.Context(), time.Minute, 10, integrationReconciliationScope())
	if err != nil || result.Examined != 1 || result.Marked != 1 || len(result.Acknowledgements) != 1 || probe.markCalls != 1 {
		t.Fatalf("result=%#v markCalls=%d err=%v", result, probe.markCalls, err)
	}
	if audited != "integration_outbox_ack_reconciliation_required" {
		t.Fatalf("audit event=%q", audited)
	}
}

func TestStartInvocationReconciliationWorkerContainsRepositoryFailure(t *testing.T) {
	probe := newIntegrationReconciliationProbe()
	probe.listErr = errors.New("database unavailable")
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: probe})
	ctx, cancel := context.WithCancel(t.Context())
	done := service.StartInvocationReconciliationWorker(ctx, time.Hour, time.Minute, 10, integrationReconciliationScope())
	select {
	case <-probe.listed:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciliation failure path was not attempted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("failed reconciliation worker did not stop")
	}
}

func TestInvocationReconciliationTickContainsCancellationAndEmptyResult(t *testing.T) {
	probe := newIntegrationReconciliationProbe()
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: probe})
	service.processInvocationReconciliationTick(t.Context(), time.Minute, 1, integrationReconciliationScope())

	probe.listErr = errors.New("database unavailable")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	service.processInvocationReconciliationTick(ctx, time.Minute, 1, integrationReconciliationScope())
}

func TestReconcileMissingIntegrationReceiptsFailureAndSkipEdges(t *testing.T) {
	scope := integrationReconciliationScope()
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{}).ReconcileMissingIntegrationReceipts(t.Context(), time.Minute, 1, principalmodel.SystemScope{}); err == nil {
		t.Fatal("invalid system scope accepted")
	}
	withoutRepository := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: integrationDeliveryWithoutReconciliation{}})
	if _, err := withoutRepository.ReconcileMissingIntegrationReceipts(t.Context(), time.Minute, 1, scope); err == nil {
		t.Fatal("missing reconciliation repository accepted")
	}

	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	probe := newIntegrationReconciliationProbe()
	probe.due = []integrationmodel.IntegrationInvocation{
		{ID: "already-changed", WorkspaceID: "workspace", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339)},
		{ID: "mark-failed", WorkspaceID: "workspace", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339)},
	}
	markErr := errors.New("mark failed")
	probe.markResults["already-changed"] = integrationReconciliationMarkResult{changed: false}
	probe.markResults["mark-failed"] = integrationReconciliationMarkResult{err: markErr}
	service := NewIntegrationApplicationService(ApplicationDependencies{
		DeliveryRepository: probe,
		Worker:             workerplatform.Dependencies{Clock: integrationFixedClock{now: now}},
	})
	result, err := service.ReconcileMissingIntegrationReceipts(t.Context(), time.Minute, 999, scope)
	if !errors.Is(err, markErr) || result.Examined != 2 || result.Skipped != 1 || result.Marked != 0 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	_, _, limit, _, markCalls, _ := probe.snapshot()
	if limit != 500 || len(markCalls) != 2 {
		t.Fatalf("limit=%d mark calls=%#v", limit, markCalls)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	probe = newIntegrationReconciliationProbe()
	probe.due = []integrationmodel.IntegrationInvocation{{ID: "cancelled", WorkspaceID: "workspace"}}
	service = NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: probe})
	result, err = service.ReconcileMissingIntegrationReceipts(cancelled, time.Minute, 1, scope)
	if !errors.Is(err, context.Canceled) || result.Examined != 1 {
		t.Fatalf("cancelled result=%#v err=%v", result, err)
	}

	probe = newIntegrationReconciliationProbe()
	service = NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: probe})
	if result, defaultErr := service.ReconcileMissingIntegrationReceipts(t.Context(), 0, 0, scope); defaultErr != nil || result.Examined != 0 {
		t.Fatalf("default reconciliation result=%#v err=%v", result, defaultErr)
	}
	service.operationalMetrics = nil
	if result, err = service.ReconcileMissingIntegrationReceipts(t.Context(), time.Minute, 1, scope); err != nil || result.Examined != 0 || result.Facts == nil {
		t.Fatalf("nil metrics result=%#v err=%v", result, err)
	}
}

func TestAcknowledgementOnlyReconciliationFailureAndSkipEdges(t *testing.T) {
	scope := integrationReconciliationScope()
	message := integrationmodel.IntegrationOutboxMessage{ID: "outbox", WorkspaceID: "workspace"}
	for name, configure := range map[string]func(*integrationAcknowledgementProbe, context.CancelFunc){
		"list": func(probe *integrationAcknowledgementProbe, _ context.CancelFunc) {
			probe.listErr = errIntegrationManagementTest
		},
		"cancel": func(_ *integrationAcknowledgementProbe, cancel context.CancelFunc) { cancel() },
		"mark": func(probe *integrationAcknowledgementProbe, _ context.CancelFunc) {
			probe.markErr = errIntegrationManagementTest
		},
	} {
		t.Run(name, func(t *testing.T) {
			probe := &integrationAcknowledgementProbe{due: []integrationmodel.IntegrationOutboxMessage{message}}
			ctx, cancel := context.WithCancel(t.Context())
			configure(probe, cancel)
			service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: integrationAcknowledgementOnlyProbe{probe: probe}})
			if _, err := service.ReconcileMissingIntegrationReceipts(ctx, time.Minute, 1, scope); err == nil {
				t.Fatal("failure swallowed")
			}
		})
	}
	changed := false
	probe := &integrationAcknowledgementProbe{due: []integrationmodel.IntegrationOutboxMessage{message}, changed: &changed}
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: integrationAcknowledgementOnlyProbe{probe: probe}})
	workerCtx, stopWorker := context.WithCancel(t.Context())
	stopWorker()
	<-service.StartInvocationReconciliationWorker(workerCtx, time.Hour, time.Minute, 1, scope)
	result, err := service.ReconcileMissingIntegrationReceipts(t.Context(), time.Minute, 1, scope)
	if err != nil || result.Skipped != 1 || result.Marked != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
