package composition

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulercontract "github.com/domainry/domainry-runtime/runtime/domain/scheduler/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type schedulerApplicationDefinitionSource struct {
	repository appschemarepository.ApplicationSchemaRepository
}

func (s schedulerApplicationDefinitionSource) ListSchedulerDefinitions(ctx context.Context) ([]recordmodel.Record, error) {
	if !schedulerMetadataRepositoryAvailable(s.repository) {
		return nil, nil
	}
	definitions, err := s.repository.ListDefinitions(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "read published scheduler definitions"), "scheduler")
	if err != nil {
		return nil, err
	}
	records := make([]recordmodel.Record, 0, len(definitions))
	for _, definition := range definitions {
		record, decodeErr := schedulerMetadataRecord(definition.ResourceKey, definition.Payload, definition.CreatedAt, definition.UpdatedAt)
		if decodeErr != nil {
			return nil, decodeErr
		}
		records = append(records, record)
	}
	return records, nil
}

func (s schedulerApplicationDefinitionSource) GetSchedulerDefinition(ctx context.Context, key string) (recordmodel.Record, bool, error) {
	if !schedulerMetadataRepositoryAvailable(s.repository) {
		return recordmodel.Record{}, false, nil
	}
	definition, found, err := s.repository.GetDefinition(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "read published scheduler definition"), "scheduler", key)
	if err != nil || !found {
		return recordmodel.Record{}, found, err
	}
	record, err := schedulerMetadataRecord(definition.ResourceKey, definition.Payload, definition.CreatedAt, definition.UpdatedAt)
	return record, err == nil, err
}

func (s schedulerApplicationDefinitionSource) ListSchedulerDefinitionVersions(ctx context.Context, key string) ([]schedulerapplication.SchedulerDefinitionVersion, error) {
	if !schedulerMetadataRepositoryAvailable(s.repository) {
		return nil, nil
	}
	versions, err := s.repository.ListDefinitionVersions(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "read scheduler definition versions"), "scheduler", key)
	if err != nil {
		return nil, err
	}
	out := make([]schedulerapplication.SchedulerDefinitionVersion, 0, len(versions))
	for _, version := range versions {
		data := map[string]any{}
		if err := json.Unmarshal(version.Payload, &data); err != nil {
			return nil, fmt.Errorf("decode scheduler definition version %s: %w", version.SchemaVersion, err)
		}
		out = append(out, schedulerapplication.SchedulerDefinitionVersion{VersionID: version.SchemaVersion, Event: "metadata_version", Data: data, CreatedAt: version.CreatedAt})
	}
	return out, nil
}

func schedulerMetadataRepositoryAvailable(repository appschemarepository.ApplicationSchemaRepository) bool {
	if repository == nil {
		return false
	}
	value := reflect.ValueOf(repository)
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return true
	}
	contract := reflect.TypeOf((*appschemarepository.ApplicationSchemaRepository)(nil)).Elem()
	for index := 0; index < value.NumField(); index++ {
		fieldType := value.Type().Field(index)
		field := value.Field(index)
		if fieldType.Anonymous && fieldType.Type.Implements(contract) && field.Kind() == reflect.Interface && field.IsNil() {
			return false
		}
	}
	return true
}

func schedulerMetadataRecord(key string, payload []byte, createdAt, updatedAt string) (recordmodel.Record, error) {
	data := map[string]any{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return recordmodel.Record{}, fmt.Errorf("decode scheduler definition %s: %w", key, err)
	}
	data["key"] = key
	return recordmodel.Record{ID: key, Data: data, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

func newSchedulerApplicationService(schema CapabilityAuthoringSchemaProvider, runtime schedulerapplication.SchedulerOperationRuntime, repository schedulercontract.SchedulerRecordRepository, audit auditcontract.AuditTelemetryAppender, worker workerplatform.Dependencies) *schedulerapplication.SchedulerApplicationService {
	return schedulerapplication.NewSchedulerApplicationServiceWithWorker(schema, runtime, repository, audit, worker)
}

type schedulerOperationRuntimeAdapter struct {
	processExecutions                  func(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
	processTargetedExecutions          func(context.Context, string, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
	processTargetedExecutionsForWindow func(context.Context, string, time.Time, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
	insertRecord                       func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error
	updateRecord                       func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error
	executeTimer                       func(context.Context, schedulerapplication.RecordTimerExecution, principalmodel.Principal) error
}

var _ schedulerapplication.SchedulerOperationRuntime = schedulerOperationRuntimeAdapter{}
var _ schedulerapplication.TargetedSchedulerOperationRuntime = schedulerOperationRuntimeAdapter{}
var _ schedulerapplication.WindowedTargetedSchedulerOperationRuntime = schedulerOperationRuntimeAdapter{}
var _ schedulerapplication.RecordTimerTargetRuntime = schedulerOperationRuntimeAdapter{}

func (adapter schedulerOperationRuntimeAdapter) ProcessDueWorkflowExecutions(ctx context.Context, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return adapter.processExecutions(ctx, limit, principal)
}

func (adapter schedulerOperationRuntimeAdapter) ProcessDueWorkflowExecutionsForTarget(ctx context.Context, targetKey string, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	if adapter.processTargetedExecutions == nil {
		return adapter.ProcessDueWorkflowExecutions(ctx, limit, principal)
	}
	return adapter.processTargetedExecutions(ctx, targetKey, limit, principal)
}

func (adapter schedulerOperationRuntimeAdapter) ProcessDueWorkflowExecutionsForScheduledWindow(ctx context.Context, targetKey string, scheduledFor time.Time, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	if adapter.processTargetedExecutionsForWindow == nil {
		return adapter.ProcessDueWorkflowExecutionsForTarget(ctx, targetKey, limit, principal)
	}
	return adapter.processTargetedExecutionsForWindow(ctx, targetKey, scheduledFor, limit, principal)
}

func (adapter schedulerOperationRuntimeAdapter) InsertSchedulerRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, reason string) error {
	return adapter.insertRecord(ctx, workspaceID, object, record, reason)
}

func (adapter schedulerOperationRuntimeAdapter) UpdateSchedulerRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, reason string) error {
	return adapter.updateRecord(ctx, workspaceID, object, record, reason)
}

func (adapter schedulerOperationRuntimeAdapter) ExecuteRecordTimer(ctx context.Context, execution schedulerapplication.RecordTimerExecution, principal principalmodel.Principal) error {
	if adapter.executeTimer == nil {
		return fmt.Errorf("record timer runtime is not configured")
	}
	return adapter.executeTimer(ctx, execution, principal)
}

func schemaObjectMap(objects []definitionmodel.ObjectSchema) map[string]definitionmodel.ObjectSchema {
	result := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, object := range objects {
		result[object.Key] = object
	}
	return result
}
