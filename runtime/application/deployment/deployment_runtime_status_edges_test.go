package deployment

import accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

import (
	"context"
	"errors"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"reflect"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

var errDeploymentStatus = errors.New("deployment status failed")

type deploymentStatusFixture struct {
	snapshot          appschemamodel.ApplicationSchemaSnapshot
	schedulerStatus   map[string]any
	schedulerErr      error
	pingErr           error
	migration         deploymentmodel.MigrationStatus
	migrationErr      error
	workflowValues    []workflowmodel.WorkflowExecution
	workflowErr       error
	invocationValues  []integrationsdk.Invocation
	invocationErr     error
	auditValues       []auditmodel.AuditEvent
	auditErr          error
	recordTotals      map[string]int
	recordErrors      map[string]error
	operational       deploymentmodel.IdempotencyOperationalStatus
	operationalErr    error
	receipts          []idempotency.ReceiptSummary
	receiptsErr       error
	changed           bool
	mutationErr       error
	cleanupResult     deploymentmodel.IdempotencyCleanupResult
	cleanupErr        error
	cleanupCalls      chan deploymentmodel.IdempotencyCleanupRequest
	cleanupWaitCancel bool
	idempotencyMetric idempotency.MetricsSnapshot
}

func (f *deploymentStatusFixture) SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return f.snapshot
}
func (f *deploymentStatusFixture) Status(context.Context, principalmodel.SystemScope) (map[string]any, error) {
	return f.schedulerStatus, f.schedulerErr
}
func (f *deploymentStatusFixture) Ping(context.Context) error { return f.pingErr }
func (f *deploymentStatusFixture) MigrationStatus(context.Context) (deploymentmodel.MigrationStatus, error) {
	return f.migration, f.migrationErr
}
func (f *deploymentStatusFixture) ListExecutions(context.Context, string, int) ([]workflowmodel.WorkflowExecution, error) {
	return f.workflowValues, f.workflowErr
}
func (f *deploymentStatusFixture) ListInvocations(context.Context, string, string, string, string, string, int) ([]integrationsdk.Invocation, error) {
	return f.invocationValues, f.invocationErr
}
func (f *deploymentStatusFixture) ListAuditEvents(context.Context, string, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return f.auditValues, f.auditErr
}
func (f *deploymentStatusFixture) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return f.auditValues, f.auditErr
}
func (f *deploymentStatusFixture) ListAuditOptions(context.Context, string, auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	return nil, f.auditErr
}
func (f *deploymentStatusFixture) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{Total: f.recordTotals[object.Key]}, f.recordErrors[object.Key]
}
func (f *deploymentStatusFixture) IdempotencyOperationalStatus(context.Context, string, time.Time) (deploymentmodel.IdempotencyOperationalStatus, error) {
	return f.operational, f.operationalErr
}
func (f *deploymentStatusFixture) IdempotencyOperationalStatusForSystem(context.Context, principalmodel.SystemScope, time.Time) (deploymentmodel.IdempotencyOperationalStatus, error) {
	return f.operational, f.operationalErr
}
func (f *deploymentStatusFixture) ListIdempotencyReceipts(context.Context, string, string, int) ([]idempotency.ReceiptSummary, error) {
	return f.receipts, f.receiptsErr
}
func (f *deploymentStatusFixture) RetryIdempotencyReceipt(context.Context, string, string, string) (bool, error) {
	return f.changed, f.mutationErr
}
func (f *deploymentStatusFixture) ResetIdempotencyReceipt(context.Context, string, string, string) (bool, error) {
	return f.changed, f.mutationErr
}
func (f *deploymentStatusFixture) RunIdempotencyCleanup(ctx context.Context, request deploymentmodel.IdempotencyCleanupRequest) (deploymentmodel.IdempotencyCleanupResult, error) {
	if f.cleanupCalls != nil {
		f.cleanupCalls <- request
	}
	if f.cleanupWaitCancel {
		<-ctx.Done()
		return deploymentmodel.IdempotencyCleanupResult{}, ctx.Err()
	}
	return f.cleanupResult, f.cleanupErr
}
func (f *deploymentStatusFixture) IdempotencyMetrics(context.Context) idempotency.MetricsSnapshot {
	return f.idempotencyMetric
}

func deploymentStatusService(fixture *deploymentStatusFixture) *DeploymentRuntimeStatusApplicationService {
	return NewDeploymentRuntimeStatusApplicationService(fixture, fixture, fixture, fixture, fixture, fixture, fixture)
}

func TestDeploymentIdempotencyStatusAndMutationEdges(t *testing.T) {
	t.Parallel()

	fixture := &deploymentStatusFixture{operational: deploymentmodel.IdempotencyOperationalStatus{Backlog: map[string]int{"pending": 2}}, receipts: []idempotency.ReceiptSummary{{ID: "receipt-1"}}, recordTotals: map[string]int{}, recordErrors: map[string]error{}}
	service := deploymentStatusService(fixture)
	status, err := service.IdempotencyOperationalStatus(t.Context(), "workspace-a")
	if err != nil || status.Backlog["pending"] != 2 {
		t.Fatalf("workspace status=%#v err=%v", status, err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "status")
	status, err = service.IdempotencyOperationalStatusForSystem(t.Context(), scope)
	if err != nil || status.Backlog["pending"] != 2 {
		t.Fatalf("system status=%#v err=%v", status, err)
	}
	unsupported := NewDeploymentRuntimeStatusApplicationService(fixture, fixture, readinessRepository{}, fixture, fixture, fixture, fixture)
	if _, err := unsupported.IdempotencyOperationalStatus(t.Context(), "workspace-a"); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("unsupported workspace status error=%v", err)
	}
	if _, err := unsupported.IdempotencyOperationalStatusForSystem(t.Context(), scope); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("unsupported system status error=%v", err)
	}
	if _, err := unsupported.ProcessIdempotencyCleanup(t.Context(), "owner", 1, time.Now(), scope); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("unsupported cleanup error=%v", err)
	}

	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"runtime.appschema.validate_application_definition"}})
	nonAdmin := admin
	accessfixture.Set(&nonAdmin, accessfixture.Bundle{})
	if receipts, err := service.IdempotencyReceipts(t.Context(), nonAdmin, "", 1); err != nil || len(receipts) != 1 {
		t.Fatalf("authenticated receipts=%v error=%v", receipts, err)
	}
	if _, err := unsupported.IdempotencyReceipts(t.Context(), admin, "", 1); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("unsupported receipts error=%v", err)
	}
	receipts, err := service.IdempotencyReceipts(t.Context(), admin, "pending", 0)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipts=%#v err=%v", receipts, err)
	}
	fixture.receiptsErr = errDeploymentStatus
	if _, err := service.IdempotencyReceipts(t.Context(), admin, "", 201); !errors.Is(err, errDeploymentStatus) {
		t.Fatalf("receipts repository error=%v", err)
	}
	fixture.receiptsErr = nil
	fixture.changed = true
	if err := service.RetryIdempotencyReceipt(t.Context(), nonAdmin, "owner", "id"); err != nil {
		t.Fatalf("authenticated retry error=%v", err)
	}
	fixture.changed = false
	if err := unsupported.RetryIdempotencyReceipt(t.Context(), admin, "owner", "id"); apperror.CodeOf(err) != idempotency.ErrorCodeReceiptUnavailable {
		t.Fatalf("unsupported retry error=%v", err)
	}
	fixture.mutationErr = errDeploymentStatus
	if err := service.RetryIdempotencyReceipt(t.Context(), admin, "owner", "id"); apperror.CodeOf(err) != "backend.internal" || !errors.Is(err, errDeploymentStatus) {
		t.Fatalf("retry repository error=%v", err)
	}
	fixture.mutationErr, fixture.changed = nil, true
	if err := service.ResetIdempotencyReceipt(t.Context(), admin, "owner", "id"); err != nil {
		t.Fatalf("reset error=%v", err)
	}
}

func TestDeploymentCleanupWorkerDefaultsAndError(t *testing.T) {
	fixture := &deploymentStatusFixture{cleanupCalls: make(chan deploymentmodel.IdempotencyCleanupRequest, 1), cleanupErr: errDeploymentStatus, recordTotals: map[string]int{}, recordErrors: map[string]error{}}
	service := deploymentStatusService(fixture)
	ctx, cancel := context.WithCancel(t.Context())
	done := service.StartIdempotencyCleanupWorker(ctx, 0, 0)
	select {
	case request := <-fixture.cleanupCalls:
		if request.BatchSize != 500 || request.LeaseOwner == "" {
			t.Fatalf("default cleanup request=%#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not stop")
	}
}

func TestDeploymentCleanupWorkerSuppressesErrorAfterCancellation(t *testing.T) {
	fixture := &deploymentStatusFixture{cleanupCalls: make(chan deploymentmodel.IdempotencyCleanupRequest, 1), cleanupWaitCancel: true, recordTotals: map[string]int{}, recordErrors: map[string]error{}}
	service := deploymentStatusService(fixture)
	ctx, cancel := context.WithCancel(t.Context())
	done := service.StartIdempotencyCleanupWorker(ctx, time.Hour, 1)
	select {
	case <-fixture.cleanupCalls:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not stop after cancellation")
	}
}

func TestDeploymentCleanupWorkerSkipsCompletionLogWhenLeaseNotAcquired(t *testing.T) {
	fixture := &deploymentStatusFixture{cleanupCalls: make(chan deploymentmodel.IdempotencyCleanupRequest, 1), cleanupResult: deploymentmodel.IdempotencyCleanupResult{Acquired: false}, recordTotals: map[string]int{}, recordErrors: map[string]error{}}
	service := deploymentStatusService(fixture)
	ctx, cancel := context.WithCancel(t.Context())
	done := service.StartIdempotencyCleanupWorker(ctx, time.Hour, 1)
	select {
	case <-fixture.cleanupCalls:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not run")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not stop")
	}
}

func TestDeploymentStorageAndMigrationEdges(t *testing.T) {
	t.Parallel()

	fixture := &deploymentStatusFixture{migration: deploymentmodel.MigrationStatus{Current: true}, recordTotals: map[string]int{}, recordErrors: map[string]error{}}
	service := deploymentStatusService(fixture)
	nilRepository := NewDeploymentRuntimeStatusApplicationService(fixture, fixture, nil, fixture, fixture, fixture, fixture)
	if status, err := nilRepository.storageStatus(t.Context()); err != nil || status["ping"] != "not_configured" {
		t.Fatalf("nil storage=%v err=%v", status, err)
	}
	if migration, err := nilRepository.migrationStatus(t.Context()); err != nil || !migration.Current {
		t.Fatalf("nil migration=%#v err=%v", migration, err)
	}
	fixture.pingErr = errDeploymentStatus
	if status, err := service.storageStatus(t.Context()); !errors.Is(err, errDeploymentStatus) || status["ping"] != "error" {
		t.Fatalf("failed storage=%v err=%v", status, err)
	}
	fixture.pingErr, fixture.migrationErr = nil, errDeploymentStatus
	if status, err := service.migrationStatus(t.Context()); !errors.Is(err, errDeploymentStatus) || status.Current || status.Error == "" {
		t.Fatalf("failed migration=%#v err=%v", status, err)
	}
	if err := service.MigrationReadiness(t.Context()); !errors.Is(err, errDeploymentStatus) {
		t.Fatalf("migration readiness error=%v", err)
	}
	fixture.migrationErr, fixture.migration = nil, deploymentmodel.MigrationStatus{Current: true}
	if err := service.MigrationReadiness(t.Context()); err != nil {
		t.Fatalf("current migration readiness=%v", err)
	}
}

func TestDeploymentMetricOwnersSuccessAndFailure(t *testing.T) {
	t.Parallel()

	objects := []definitionmodel.ObjectSchema{{Key: "z"}, {Key: "a"}}
	fixture := &deploymentStatusFixture{
		snapshot:       appschemamodel.ApplicationSchemaSnapshot{Objects: objects, Actions: []definitionmodel.ActionSchema{{Key: "action"}}, Workflows: []definitionmodel.WorkflowSchema{{Key: "workflow"}}},
		workflowValues: []workflowmodel.WorkflowExecution{{}}, invocationValues: []integrationsdk.Invocation{{}}, auditValues: []auditmodel.AuditEvent{{}},
		recordTotals: map[string]int{"a": 1, "z": 2}, recordErrors: map[string]error{}, migration: deploymentmodel.MigrationStatus{Current: true}, schedulerStatus: map[string]any{},
	}
	service := deploymentStatusService(fixture)
	if metrics, err := service.workflowMetrics(t.Context()); err != nil || metrics["configured"] != 1 {
		t.Fatalf("workflow metrics=%v err=%v", metrics, err)
	}
	if metrics, err := service.businessActionMetrics(t.Context()); err != nil || metrics["configured"] == nil {
		t.Fatalf("action metrics=%v err=%v", metrics, err)
	}
	if _, err := service.auditMetrics(t.Context()); err != nil {
		t.Fatalf("audit metrics error=%v", err)
	}
	counts, err := service.businessRecordCounts(t.Context())
	if err != nil || counts["a"] != 1 || counts["z"] != 2 || fixture.snapshot.Objects[0].Key != "z" {
		t.Fatalf("record counts=%v original=%v err=%v", counts, fixture.snapshot.Objects, err)
	}

	fixture.workflowErr, fixture.invocationErr, fixture.auditErr = errDeploymentStatus, errDeploymentStatus, errDeploymentStatus
	fixture.recordErrors = map[string]error{"a": errDeploymentStatus, "z": errors.New("second")}
	if metrics, err := service.workflowMetrics(t.Context()); !errors.Is(err, errDeploymentStatus) || metrics == nil {
		t.Fatalf("failed workflow metrics=%v err=%v", metrics, err)
	}
	if metrics, err := service.businessActionMetrics(t.Context()); !errors.Is(err, errDeploymentStatus) || metrics == nil {
		t.Fatalf("failed action metrics=%v err=%v", metrics, err)
	}
	if metrics, err := service.auditMetrics(t.Context()); !errors.Is(err, errDeploymentStatus) || metrics == nil {
		t.Fatalf("failed audit metrics=%v err=%v", metrics, err)
	}
	if counts, err := service.businessRecordCounts(t.Context()); !errors.Is(err, errDeploymentStatus) || len(counts) != 0 {
		t.Fatalf("failed record counts=%v err=%v", counts, err)
	}
}

func TestDeploymentMonitoringMetricSectionsCollectOwnerObservationsAndErrors(t *testing.T) {
	t.Parallel()

	fixture := &deploymentStatusFixture{
		snapshot:     appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "object"}}},
		recordTotals: map[string]int{"object": 1}, recordErrors: map[string]error{}, migration: deploymentmodel.MigrationStatus{Current: true}, schedulerStatus: nil,
		operational: deploymentmodel.IdempotencyOperationalStatus{Backlog: map[string]int{}},
	}
	service := deploymentStatusService(fixture)
	payload, errorsPayload := service.MonitoringMetricSections(t.Context())
	if payload["objects"] != 1 || payload["idempotency"] == nil || payload["scheduler"] == nil {
		t.Fatalf("success metrics=%#v", payload)
	}
	if len(errorsPayload) != 0 {
		t.Fatalf("unexpected metrics errors=%#v", errorsPayload)
	}
	fixture.workflowErr, fixture.auditErr, fixture.pingErr, fixture.migrationErr = errDeploymentStatus, errDeploymentStatus, errDeploymentStatus, errDeploymentStatus
	fixture.operationalErr, fixture.schedulerErr, fixture.invocationErr = errDeploymentStatus, errDeploymentStatus, errDeploymentStatus
	fixture.recordErrors["object"] = errDeploymentStatus
	payload, errorsPayload = service.MonitoringMetricSections(t.Context())
	for _, key := range []string{"workflow", "audit", "storage", "migration", "idempotency", "scheduler", "domain", "business_actions"} {
		if errorsPayload[key] == "" {
			t.Errorf("missing metrics error %q: payload=%#v errors=%#v", key, payload, errorsPayload)
		}
	}

	fixture.schedulerStatus = map[string]any{"runtime_available": true}
	payload, _ = service.MonitoringMetricSections(t.Context())
	if payload["scheduler"].(map[string]any)["runtime_available"] != true {
		t.Fatalf("non-nil scheduler metrics=%#v", payload)
	}

	withoutIdempotencyMetrics := NewDeploymentRuntimeStatusApplicationService(fixture, fixture, readinessRepository{migration: deploymentmodel.MigrationStatus{Current: true}}, fixture, fixture, fixture, fixture)
	payload, _ = withoutIdempotencyMetrics.MonitoringMetricSections(t.Context())
	if _, exists := payload["idempotency"]; exists {
		t.Fatalf("unexpected idempotency metrics=%#v", payload)
	}
}

func TestDeploymentStatusConstructors(t *testing.T) {
	t.Parallel()
	fixture := &deploymentStatusFixture{}
	service := NewDeploymentRuntimeStatusApplicationService(fixture, fixture, fixture, fixture, fixture, fixture, fixture)
	withWorker := NewDeploymentRuntimeStatusApplicationServiceWithWorker(fixture, fixture, fixture, fixture, fixture, fixture, fixture, service.worker)
	if withWorker.worker.Clock == nil || reflect.ValueOf(withWorker).IsNil() {
		t.Fatalf("services=%#v %#v", service, withWorker)
	}
}
