package deployment

import (
	"errors"
	"strings"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"

	deploymentcontract "github.com/domainry/domainry-runtime/runtime/domain/deployment/contract"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"

	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/logging"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"go.uber.org/zap"
)

var errMigrationNotCurrent = errors.New("runtime migration is not current")

type SchedulerStatusProvider interface {
	Status(context.Context, principalmodel.SystemScope) (map[string]any, error)
}

type RuntimeSchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
}

type IdempotencyMetricsProvider interface {
	IdempotencyMetrics(context.Context) idempotency.MetricsSnapshot
}

type LifecycleHealthProvider interface {
	HealthForSystem(context.Context, principalmodel.SystemScope, time.Time) (map[string]any, error)
}

func (s *DeploymentRuntimeStatusApplicationService) IdempotencyOperationalStatus(ctx context.Context, workspaceID string) (deploymentmodel.IdempotencyOperationalStatus, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return deploymentmodel.IdempotencyOperationalStatus{}, apperror.New(apperror.KindForbidden, "backend.workspace_scope_required", err, nil)
	}
	operations, ok := s.repository.(deploymentrepository.IdempotencyOperationsRepository)
	if !ok {
		return deploymentmodel.IdempotencyOperationalStatus{Backlog: map[string]int{}}, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	return operations.IdempotencyOperationalStatus(ctx, workspace.String(), s.worker.Clock.Now())
}

func (s *DeploymentRuntimeStatusApplicationService) IdempotencyOperationalStatusForSystem(ctx context.Context, scope principalmodel.SystemScope) (deploymentmodel.IdempotencyOperationalStatus, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return deploymentmodel.IdempotencyOperationalStatus{}, apperror.New(apperror.KindForbidden, "backend.system_scope_required", err, nil)
	}
	operations, ok := s.repository.(deploymentrepository.IdempotencyOperationsRepository)
	if !ok {
		return deploymentmodel.IdempotencyOperationalStatus{Backlog: map[string]int{}}, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	return operations.IdempotencyOperationalStatusForSystem(ctx, scope, s.worker.Clock.Now())
}

func (s *DeploymentRuntimeStatusApplicationService) ProcessIdempotencyCleanup(ctx context.Context, owner string, batchSize int, now time.Time, scope principalmodel.SystemScope) (deploymentmodel.IdempotencyCleanupResult, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return deploymentmodel.IdempotencyCleanupResult{}, apperror.New(apperror.KindForbidden, "backend.system_scope_required", err, nil)
	}
	operations, ok := s.repository.(deploymentrepository.IdempotencyOperationsRepository)
	if !ok {
		return deploymentmodel.IdempotencyCleanupResult{}, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	return operations.RunIdempotencyCleanup(ctx, deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: strings.TrimSpace(owner), LeaseTTL: 2 * time.Minute, BatchSize: batchSize, Now: now})
}

func (s *DeploymentRuntimeStatusApplicationService) StartIdempotencyCleanupWorker(ctx context.Context, interval time.Duration, batchSize int) <-chan struct{} {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	owner := s.worker.WorkerID.String()
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "clean expired idempotency receipts")
	return workerplatform.StartNamedLoop(ctx, "idempotency_cleanup", interval, func() {
		s.worker.Control.RunIfAccepting(func() {
			result, err := s.ProcessIdempotencyCleanup(ctx, owner, batchSize, s.worker.Clock.Now(), scope)
			if err != nil {
				if ctx.Err() == nil {
					fields := append([]zap.Field{zap.String("lease_owner", owner)}, logging.StableErrorFields(err)...)
					logging.FromContext(ctx).Error("idempotency cleanup worker failed", fields...)
				}
				return
			}
			if result.Acquired {
				logging.FromContext(ctx).Info("idempotency cleanup completed", zap.Int("deleted", result.Deleted), zap.Int64("fencing_token", result.FencingToken), zap.String("lease_owner", owner))
			}
		})
	})
}

// DeploymentRuntimeStatusApplicationService reports the health of deployed runtime owners.
type DeploymentRuntimeStatusApplicationService struct {
	templateID      string
	templateVersion string
	schema          RuntimeSchemaProvider
	scheduler       SchedulerStatusProvider
	repository      deploymentrepository.DeploymentRuntimeStatusRepository
	records         deploymentcontract.DeploymentRecordReader
	audit           auditcontract.AuditReader
	workflow        deploymentcontract.DeploymentWorkflowExecutionReader
	delivery        deploymentcontract.DeploymentDeliveryReader
	worker          workerplatform.Dependencies
	lifecycleHealth LifecycleHealthProvider
}

func (s *DeploymentRuntimeStatusApplicationService) ConfigureLifecycleHealth(_ context.Context, provider LifecycleHealthProvider) {
	s.lifecycleHealth = provider
}

func (s *DeploymentRuntimeStatusApplicationService) IdempotencyReceipts(ctx context.Context, principal principalmodel.Principal, status string, limit int) ([]idempotency.ReceiptSummary, error) {
	if err := deploymentAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	operations, ok := s.repository.(deploymentrepository.IdempotencyOperationsRepository)
	if !principal.HasPermission("workspace.admin") {
		return nil, apperror.New(apperror.KindForbidden, "auth.permission_denied", nil, nil)
	}
	if !ok {
		return nil, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return operations.ListIdempotencyReceipts(ctx, principal.WorkspaceID, status, limit)
}

func (s *DeploymentRuntimeStatusApplicationService) RetryIdempotencyReceipt(ctx context.Context, principal principalmodel.Principal, owner, id string) error {
	return s.mutateIdempotencyReceipt(ctx, principal, owner, id, false)
}

func (s *DeploymentRuntimeStatusApplicationService) ResetIdempotencyReceipt(ctx context.Context, principal principalmodel.Principal, owner, id string) error {
	return s.mutateIdempotencyReceipt(ctx, principal, owner, id, true)
}

func (s *DeploymentRuntimeStatusApplicationService) mutateIdempotencyReceipt(ctx context.Context, principal principalmodel.Principal, owner, id string, reset bool) error {
	if err := deploymentAuthorizeCommand(principal); err != nil {
		return err
	}
	operations, ok := s.repository.(deploymentrepository.IdempotencyOperationsRepository)
	if !principal.HasPermission("workspace.admin") {
		return apperror.New(apperror.KindForbidden, "auth.permission_denied", nil, nil)
	}
	if !ok {
		return apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	var changed bool
	var err error
	if reset {
		changed, err = operations.ResetIdempotencyReceipt(ctx, principal.WorkspaceID, owner, id)
	} else {
		changed, err = operations.RetryIdempotencyReceipt(ctx, principal.WorkspaceID, owner, id)
	}
	if err != nil {
		return apperror.New(apperror.KindInternal, "backend.internal", err, nil)
	}
	if !changed {
		return apperror.New(apperror.KindConflict, "backend.idempotency.receipt_not_mutable", nil, nil)
	}
	return nil
}

func NewDeploymentRuntimeStatusApplicationService(templateID, templateVersion string, schema RuntimeSchemaProvider, scheduler SchedulerStatusProvider, repository deploymentrepository.DeploymentRuntimeStatusRepository, records deploymentcontract.DeploymentRecordReader, audit auditcontract.AuditReader, workflow deploymentcontract.DeploymentWorkflowExecutionReader, delivery deploymentcontract.DeploymentDeliveryReader) *DeploymentRuntimeStatusApplicationService {
	return NewDeploymentRuntimeStatusApplicationServiceWithWorker(templateID, templateVersion, schema, scheduler, repository, records, audit, workflow, delivery, workerplatform.Dependencies{})
}

func NewDeploymentRuntimeStatusApplicationServiceWithWorker(templateID, templateVersion string, schema RuntimeSchemaProvider, scheduler SchedulerStatusProvider, repository deploymentrepository.DeploymentRuntimeStatusRepository, records deploymentcontract.DeploymentRecordReader, audit auditcontract.AuditReader, workflow deploymentcontract.DeploymentWorkflowExecutionReader, delivery deploymentcontract.DeploymentDeliveryReader, worker workerplatform.Dependencies) *DeploymentRuntimeStatusApplicationService {
	return &DeploymentRuntimeStatusApplicationService{templateID: templateID, templateVersion: templateVersion, schema: schema, scheduler: scheduler, repository: repository, records: records, audit: audit, workflow: workflow, delivery: delivery, worker: workerplatform.NormalizeDependencies(worker)}
}

func (s *DeploymentRuntimeStatusApplicationService) storageStatus(ctx context.Context) (map[string]any, error) {
	status := map[string]any{"ping": "ok"}
	if s.repository == nil {
		status["ping"] = "not_configured"
		return status, nil
	}
	if err := s.repository.Ping(ctx); err != nil {
		status["ping"], status["error"] = "error", err.Error()
		return status, err
	}
	return status, nil
}

func (s *DeploymentRuntimeStatusApplicationService) MonitoringStorageStatus(ctx context.Context) (map[string]any, error) {
	return s.storageStatus(ctx)
}

func (s *DeploymentRuntimeStatusApplicationService) migrationStatus(ctx context.Context) (deploymentmodel.MigrationStatus, error) {
	if s.repository == nil {
		return deploymentmodel.MigrationStatus{Current: true}, nil
	}
	status, err := s.repository.MigrationStatus(ctx)
	if err != nil {
		status.Current, status.Error = false, err.Error()
	}
	return status, err
}

func (s *DeploymentRuntimeStatusApplicationService) MonitoringMigrationStatus(ctx context.Context) (deploymentmodel.MigrationStatus, error) {
	return s.migrationStatus(ctx)
}

func (s *DeploymentRuntimeStatusApplicationService) StorageReadiness(ctx context.Context) error {
	_, err := s.storageStatus(ctx)
	return err
}

func (s *DeploymentRuntimeStatusApplicationService) MigrationReadiness(ctx context.Context) error {
	status, err := s.migrationStatus(ctx)
	if err != nil {
		return err
	}
	if !status.Current {
		return errMigrationNotCurrent
	}
	return nil
}

func (s *DeploymentRuntimeStatusApplicationService) MigrationTelemetry(ctx context.Context) (pending int, current bool, err error) {
	status, err := s.migrationStatus(ctx)
	return status.Pending, status.Current, err
}
