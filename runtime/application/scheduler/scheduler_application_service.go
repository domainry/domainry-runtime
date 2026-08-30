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

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	schedulercontract "github.com/domainry/domainry-runtime/runtime/domain/scheduler/contract"
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
	SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
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

// SchedulerApplicationService owns Runtime definition authoring, downstream
// dispatch and record timers. Clock runs and dead letters live in Scheduler.
type SchedulerApplicationService struct {
	schema              SchemaProvider
	runtime             SchedulerOperationRuntime
	repository          schedulercontract.SchedulerRecordRepository
	authoritativeClock  SchedulerAuthoritativeClock
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

// UseRepositoryAuthoritativeClock enables database-time lease arbitration for
// distributed workers while keeping deterministic application tests injectable.
func (s *SchedulerApplicationService) UseRepositoryAuthoritativeClock(context.Context) {
	if s == nil {
		return
	}
	if clock, ok := s.repository.(SchedulerAuthoritativeClock); ok {
		s.authoritativeClock = clock
	}
}

func (s *SchedulerApplicationService) UseNotificationCompiler(compiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)) {
	if s != nil {
		s.compileNotification = compiler
	}
}

func schedulerOperationAllowed(principal principalmodel.Principal) error {
	if err := schedulerAuthorizeCommand(principal); err != nil {
		return err
	}
	if principal.HasPermission("workspace.admin") || principal.HasExactPermission("scheduler.command") {
		return nil
	}
	return forbidden("backend.scheduler.permission_required")
}

func schedulerAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func schedulerAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func (s *SchedulerApplicationService) objectForPrincipal(ctx context.Context, principal principalmodel.Principal, objectKey string) (definitionmodel.ObjectSchema, error) {
	if s == nil || s.schema == nil {
		return definitionmodel.ObjectSchema{}, notFound("backend.scheduler.runtime_object_not_found", "object_key", objectKey)
	}
	for _, object := range s.schema.SchemaForPrincipal(ctx, principal).Objects {
		if object.Key == objectKey {
			return object, nil
		}
	}
	return definitionmodel.ObjectSchema{}, notFound("backend.scheduler.runtime_object_not_found", "object_key", objectKey)
}

func schemaObjectMap(objects []definitionmodel.ObjectSchema) map[string]definitionmodel.ObjectSchema {
	result := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, object := range objects {
		result[object.Key] = object
	}
	return result
}

func existingStringBefore(record recordmodel.Record, key string) string {
	value := strings.TrimSpace(fmt.Sprint(record.Data[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}

func ExistingStringBefore(record recordmodel.Record, key string) string {
	return existingStringBefore(record, key)
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (s *SchedulerApplicationService) UseReportSnapshotRuntime(runtime ReportSnapshotRuntime) {
	s.reportSnapshots = runtime
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
