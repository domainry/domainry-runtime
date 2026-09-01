// Package recordtimer owns Runtime record-scoped delayed execution.
package recordtimer

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordtimercontract "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/contract"
)

type SchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
}

type RecordTimerExecution struct {
	TimerID, WorkspaceID, ObjectKey, RecordID string
	TargetType, TargetKey                     string
	Payload                                   map[string]any
	IdempotencyKey                            string
}

type TargetRuntime interface {
	ExecuteRecordTimer(context.Context, RecordTimerExecution, principalmodel.Principal) error
}

type WorkerConfig struct {
	Enabled      bool
	PollInterval time.Duration
	BatchSize    int
	LeaseTTL     time.Duration
}

func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{Enabled: true, PollInterval: 500 * time.Millisecond, BatchSize: 25, LeaseTTL: 5 * time.Minute}
}

func NormalizeWorkerConfig(config WorkerConfig) WorkerConfig {
	if config.PollInterval <= 0 {
		config.PollInterval = 500 * time.Millisecond
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 25
	}
	if config.BatchSize > 500 {
		config.BatchSize = 500
	}
	if config.LeaseTTL <= 0 {
		config.LeaseTTL = 5 * time.Minute
	}
	return config
}

type RecordTimerApplicationService struct {
	schema              SchemaProvider
	runtime             TargetRuntime
	repository          recordtimercontract.RecordTimerRepository
	configMu            sync.RWMutex
	config              WorkerConfig
	recordTimerCursorMu sync.Mutex
	recordTimerCursor   int
	worker              workerplatform.Dependencies
}

func NewRecordTimerApplicationService(schema SchemaProvider, runtime TargetRuntime, repository recordtimercontract.RecordTimerRepository) *RecordTimerApplicationService {
	return NewRecordTimerApplicationServiceWithWorker(schema, runtime, repository, workerplatform.Dependencies{})
}

func NewRecordTimerApplicationServiceWithWorker(schema SchemaProvider, runtime TargetRuntime, repository recordtimercontract.RecordTimerRepository, worker workerplatform.Dependencies) *RecordTimerApplicationService {
	return &RecordTimerApplicationService{
		schema: schema, runtime: runtime, repository: repository,
		config: DefaultWorkerConfig(), worker: workerplatform.NormalizeDependencies(worker),
	}
}

func (s *RecordTimerApplicationService) ConfigureWorker(config WorkerConfig) {
	s.configMu.Lock()
	s.config = NormalizeWorkerConfig(config)
	s.configMu.Unlock()
}

func (s *RecordTimerApplicationService) WorkerConfig() WorkerConfig {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return NormalizeWorkerConfig(s.config)
}

func (s *RecordTimerApplicationService) leaseTTL() time.Duration {
	return s.WorkerConfig().LeaseTTL
}

func (s *RecordTimerApplicationService) objectForPrincipal(ctx context.Context, principal principalmodel.Principal, objectKey string) (definitionmodel.ObjectSchema, error) {
	if s == nil || s.schema == nil {
		return definitionmodel.ObjectSchema{}, recordTimerError(apperror.KindNotFound, "backend.record_timer.runtime_object_not_found", nil, "object_key", objectKey)
	}
	for _, object := range s.schema.SchemaForPrincipal(ctx, principal).Objects {
		if object.Key == objectKey {
			return object, nil
		}
	}
	return definitionmodel.ObjectSchema{}, recordTimerError(apperror.KindNotFound, "backend.record_timer.runtime_object_not_found", nil, "object_key", objectKey)
}

func recordTimerWorkerPrincipal() principalmodel.Principal {
	principal := principalmodel.NewSystemPrincipal(
		"record-timer:worker",
		principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "record timer dispatch"),
		"record_timer.create", "record_timer.read", "record_timer.update",
		"record_timer_event.create", "record_timer_event.read",
	)
	principal.WorkspaceID = principalmodel.InstallationWorkspaceID
	return principal
}

func recordTimerAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return recordTimerError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func recordTimerAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return recordTimerError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func recordTimerOpsReadAllowed(principal principalmodel.Principal) error {
	if err := recordTimerAuthorizeQuery(principal); err != nil {
		return err
	}
	if principal.HasExactPermission("operations.read") || principal.HasExactPermission("record_timer.command") {
		return nil
	}
	return recordTimerError(apperror.KindForbidden, "backend.record_timer.permission_required", nil)
}

func recordTimerError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: err}
}

func recordTimerInternalError(operation string, err error) error {
	return recordTimerError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func recordTimerString(record recordmodel.Record, key string) string {
	value := strings.TrimSpace(fmt.Sprint(record.Data[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}

func recordTimerInt(value any, fallback int) int {
	var out int
	if _, err := fmt.Sscan(strings.TrimSpace(fmt.Sprint(value)), &out); err == nil {
		return out
	}
	return fallback
}

func recordTimerLimit(limit int) int {
	if limit <= 0 {
		return 25
	}
	if limit > 500 {
		return 500
	}
	return limit
}
