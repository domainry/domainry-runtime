package scheduler

import (
	"fmt"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	schedulervalidation "github.com/domainry/domainry-runtime/runtime/domain/scheduler/validation"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"sync"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	schedulercontract "github.com/domainry/domainry-runtime/runtime/domain/scheduler/contract"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type SchedulerDefinitionHistoryReader interface {
	Events(context.Context, auditmodel.AuditEventQuery, principalmodel.Principal) ([]auditmodel.AuditEvent, error)
}

type SchedulerDefinitionVersion struct {
	VersionID string         `json:"version_id"`
	Event     string         `json:"event"`
	Data      map[string]any `json:"data"`
	CreatedAt string         `json:"created_at"`
}

func (s *SchedulerApplicationService) UseDefinitionHistoryReader(reader SchedulerDefinitionHistoryReader) {
	if s != nil {
		s.definitionHistory = reader
	}
}

// provisionPublishedDefinitions materializes the durable cursor owned by each
// enabled published definition before its first execution.
func (s *SchedulerApplicationService) provisionPublishedDefinitions(ctx context.Context, workspaceID string, now time.Time) (int, error) {
	if s.definitions == nil {
		return 0, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	definitions, err := s.definitions.ListSchedulerDefinitions(ctx)
	if err != nil {
		return 0, internalError("list scheduler job definitions", err)
	}
	cursorObject, err := s.ownerObject(ctx, "scheduler_cursor")
	if err != nil {
		return 0, err
	}
	existingCursors, err := s.schedulerCursorMap(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	provisioned := 0
	for _, definition := range definitions {
		if strings.TrimSpace(fmt.Sprint(definition.Data["status"])) != "enabled" {
			continue
		}
		if err := schedulervalidation.SchedulerValidateDefinitionContract(ctx, definition.Data); err != nil {
			return provisioned, err
		}
		if _, found := existingCursors[definition.ID]; found {
			continue
		}
		nextRunAt, ok := schedulerDefinitionNextRunAt(definition)
		if !ok {
			nextRunAt = schedulerNextRunAt(definition, now)
		}
		cursor := recordmodel.Record{ID: definition.ID, CreatedAt: now.UTC().Format(time.RFC3339), UpdatedAt: now.UTC().Format(time.RFC3339), Data: map[string]any{
			"scheduler_definition_key": definition.ID, "next_run_at": nextRunAt.UTC().Format(time.RFC3339), "last_run_at": "", "last_run_status": "",
		}}
		if s.insertRecord != nil {
			err = s.insertRecord(ctx, workspaceID, cursorObject, cursor, "provision published scheduler definition")
		} else {
			err = s.repository.CommitRecordMutationBatch(ctx, workspaceID, []transactionmodel.RecordMutationCommit{{Operation: "create", Object: cursorObject, Record: cursor}})
		}
		if err != nil {
			if _, found, getErr := s.repository.GetRecord(ctx, workspaceID, cursorObject, definition.ID); getErr == nil && found {
				continue
			}
			return provisioned, internalError("provision scheduler cursor", err)
		}
		provisioned++
		existingCursors[definition.ID] = cursor
	}
	return provisioned, nil
}

func (s *SchedulerApplicationService) ProvisionPublishedDefinitions(ctx context.Context, now time.Time, scope principalmodel.SystemScope) (int, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return 0, schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	return s.provisionPublishedDefinitions(ctx, principalmodel.InstallationWorkspaceID, now.UTC())
}

func (s *SchedulerApplicationService) GetDefinition(ctx context.Context, definitionID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := schedulerDefinitionReadAllowed(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if s.definitions == nil {
		return recordmodel.Record{}, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	definition, found, err := s.definitions.GetSchedulerDefinition(ctx, strings.TrimSpace(definitionID))
	if err != nil {
		return recordmodel.Record{}, internalError("get scheduler job definition", err)
	}
	if !found {
		return recordmodel.Record{}, notFound("backend.scheduler.definition_not_found")
	}
	return definition, nil
}

func (s *SchedulerApplicationService) PublishedDefinitions(ctx context.Context, principal principalmodel.Principal) ([]recordmodel.Record, error) {
	if err := schedulerDefinitionReadAllowed(principal); err != nil {
		return nil, err
	}
	if s.definitions == nil {
		return nil, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	return s.definitions.ListSchedulerDefinitions(ctx)
}

func (s *SchedulerApplicationService) DefinitionVersions(ctx context.Context, definitionID string, principal principalmodel.Principal) ([]SchedulerDefinitionVersion, error) {
	if _, err := s.GetDefinition(ctx, definitionID, principal); err != nil {
		return nil, err
	}
	if s.definitions == nil {
		return nil, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	return s.definitions.ListSchedulerDefinitionVersions(ctx, strings.TrimSpace(definitionID))
}

func schedulerDefinitionReadAllowed(principal principalmodel.Principal) error {
	if err := schedulerAuthorizeQuery(principal); err != nil {
		return err
	}
	if principal.HasPermission("workspace.admin") || principal.HasExactPermission("metadata.read") || principal.HasExactPermission("scheduler.definition.read") {
		return nil
	}
	return forbidden("backend.scheduler.permission_required")
}

type SchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot
}

type WorkerConfig struct {
	Enabled           bool
	PollInterval      time.Duration
	BatchSize         int
	LeaseTTL          time.Duration
	MaxCatchupWindows int
}

func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{Enabled: true, PollInterval: 500 * time.Millisecond, BatchSize: 25, LeaseTTL: 5 * time.Minute, MaxCatchupWindows: 1}
}

func NormalizeWorkerConfig(cfg WorkerConfig) WorkerConfig {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 25
	}
	if cfg.BatchSize > 500 {
		cfg.BatchSize = 500
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 5 * time.Minute
	}
	if cfg.MaxCatchupWindows <= 0 {
		cfg.MaxCatchupWindows = 1
	}
	return cfg
}

type SchedulerDefinitionPreview struct {
	NextRuns []string `json:"next_runs"`
}

// SchedulerDefinitionSource is the read boundary for published, versioned
// scheduler metadata. Operational records must never implement this port.
type SchedulerDefinitionSource interface {
	ListSchedulerDefinitions(context.Context) ([]recordmodel.Record, error)
	GetSchedulerDefinition(context.Context, string) (recordmodel.Record, bool, error)
	ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error)
}

// SchedulerApplicationService owns scheduled-job lifecycle behavior.
type SchedulerApplicationService struct {
	schema              SchemaProvider
	runtime             SchedulerOperationRuntime
	repository          schedulercontract.SchedulerRecordRepository
	audit               auditcontract.AuditTelemetryAppender
	definitionHistory   SchedulerDefinitionHistoryReader
	definitions         SchedulerDefinitionSource
	insertRecord        func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error
	updateRecord        func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error
	configMu            sync.RWMutex
	config              WorkerConfig
	timerCursorMu       sync.Mutex
	timerCursor         int
	worker              workerplatform.Dependencies
	reportSnapshots     ReportSnapshotRuntime
	compileNotification func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
}

func (s *SchedulerApplicationService) UseDefinitionSource(source SchedulerDefinitionSource) {
	if s != nil {
		s.definitions = source
	}
}

func (s *SchedulerApplicationService) UseNotificationCompiler(compiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)) {
	if s != nil {
		s.compileNotification = compiler
	}
}

func (s *SchedulerApplicationService) UseReportSnapshotRuntime(runtime ReportSnapshotRuntime) {
	s.reportSnapshots = runtime
}

func (s *SchedulerApplicationService) runJob(ctx context.Context, definitionID, idempotencyKey string, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return SchedulerOperationResult{}, badRequest(idempotency.ErrorCodeMissingKey)
	}
	if err := schedulerOpsCommandAllowed(principal); err != nil {
		return SchedulerOperationResult{}, err
	}
	definition, err := s.schedulerDefinitionForOperation(ctx, definitionID, principal)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	now := s.worker.Clock.Now()
	run, claimed, err := s.claimRunWithKey(ctx, principal.WorkspaceID, definition, "manual_run", idempotencyKey, now)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	if !claimed {
		return SchedulerOperationResult{Status: "replayed", Message: "backend.scheduler.run_replayed", Run: run}, nil
	}
	result, err := s.processClaimedRun(ctx, principal.WorkspaceID, definition, run, 25, workflowWorkerPrincipal(), now)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	runObject, err := s.ownerObject(ctx, "job_run")
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	finished, err := s.readFinishedRun(ctx, principal.WorkspaceID, runObject, run.ID)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	s.insertOperationAudit(ctx, "scheduler_job_manual_run", "job_run", finished.ID, principal, "Manual scheduler job run "+definition.ID, nil, finished.Data, map[string]any{
		"scheduler_definition_key": definition.ID,
		"target_type":              schedulerDefinitionTargetType(definition),
		"target_key":               strings.TrimSpace(fmt.Sprint(definition.Data["target_key"])),
	})
	return SchedulerOperationResult{Status: "completed", Message: "backend.scheduler.manual_run_completed", Run: finished, Result: result}, nil
}

type RecordMutationRuntime interface {
	InsertSchedulerRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error
	UpdateSchedulerRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error
}

func NewSchedulerApplicationService(schema SchemaProvider, runtime SchedulerOperationRuntime, repository schedulercontract.SchedulerRecordRepository, audit auditcontract.AuditTelemetryAppender) *SchedulerApplicationService {
	return NewSchedulerApplicationServiceWithWorker(schema, runtime, repository, audit, workerplatform.Dependencies{})
}

func NewSchedulerApplicationServiceWithWorker(schema SchemaProvider, runtime SchedulerOperationRuntime, repository schedulercontract.SchedulerRecordRepository, audit auditcontract.AuditTelemetryAppender, worker workerplatform.Dependencies) *SchedulerApplicationService {
	service := &SchedulerApplicationService{schema: schema, runtime: runtime, repository: repository, audit: audit, config: DefaultWorkerConfig(), worker: workerplatform.NormalizeDependencies(worker)}
	if source, ok := repository.(SchedulerDefinitionSource); ok {
		service.definitions = source
	} else if source, ok := schema.(SchedulerDefinitionSource); ok {
		service.definitions = source
	}
	if adapter, ok := runtime.(RecordMutationRuntime); ok {
		service.insertRecord = adapter.InsertSchedulerRecord
		service.updateRecord = adapter.UpdateSchedulerRecord
	}
	return service
}

func (s *SchedulerApplicationService) ConfigureWorker(config WorkerConfig) {
	s.configMu.Lock()
	s.config = NormalizeWorkerConfig(config)
	s.configMu.Unlock()
}

func (s *SchedulerApplicationService) WorkerConfig() WorkerConfig {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return NormalizeWorkerConfig(s.config)
}

func (s *SchedulerApplicationService) leaseTTL() time.Duration {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return NormalizeWorkerConfig(s.config).LeaseTTL
}

func (s *SchedulerApplicationService) maxCatchupWindows() int {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return NormalizeWorkerConfig(s.config).MaxCatchupWindows
}

func (s *SchedulerApplicationService) PreviewDefinition(ctx context.Context, data map[string]any, principal principalmodel.Principal) (SchedulerDefinitionPreview, error) {
	if err := ctx.Err(); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	if err := schedulerDefinitionWriteAllowed(principal); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	if err := schedulervalidation.SchedulerValidateDefinitionContract(ctx, data); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	return s.previewSchedule(data), nil
}

// PreviewSchedule validates and previews a leaf schedule payload without a
// synthetic Scheduler job definition.
func (s *SchedulerApplicationService) PreviewSchedule(ctx context.Context, data map[string]any, principal principalmodel.Principal) (SchedulerDefinitionPreview, error) {
	if err := ctx.Err(); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	if err := schedulerDefinitionWriteAllowed(principal); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	if err := schedulervalidation.SchedulerValidateScheduleFragment(ctx, data); err != nil {
		return SchedulerDefinitionPreview{}, err
	}
	return s.previewSchedule(data), nil
}

func (s *SchedulerApplicationService) previewSchedule(data map[string]any) SchedulerDefinitionPreview {
	definition := recordmodel.Record{Data: recordvalidation.RecordCloneData(data)}
	cursor := s.worker.Clock.Now()
	preview := SchedulerDefinitionPreview{NextRuns: make([]string, 0, 3)}
	for range 3 {
		next := schedulerpolicy.SchedulerScheduleNextRunAt(definition, cursor)
		preview.NextRuns = append(preview.NextRuns, next.UTC().Format(time.RFC3339))
		cursor = next
	}
	return preview
}

func (s *SchedulerApplicationService) insertOperationAudit(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, after map[string]any, metadata map[string]any) {
	if s.audit == nil {
		return
	}
	normalizedMetadata := make(map[string]any, len(metadata)+3)
	for key, value := range metadata {
		normalizedMetadata[key] = value
	}
	reason := strings.TrimSpace(fmt.Sprint(normalizedMetadata["reason"]))
	if reason == "" || reason == "<nil>" {
		normalizedMetadata["reason"] = summary
	}
	normalizedMetadata["result"] = "succeeded"
	correlationID := strings.TrimSpace(requestcontext.CorrelationID(ctx))
	if correlationID == "" {
		correlationID = event + ":" + recordID
	}
	normalizedMetadata["correlation_id"] = correlationID
	s.audit.AppendAuditTelemetry(ctx, auditcontract.AuditAppendRequest{
		Event: event, ObjectKey: objectKey, RecordID: recordID, Principal: principal,
		Summary: summary, Before: before, After: after, Metadata: normalizedMetadata,
	})
}
