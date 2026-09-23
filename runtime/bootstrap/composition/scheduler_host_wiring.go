package composition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type SchedulerPublishedDefinition struct {
	Key       string
	Data      map[string]any
	CreatedAt string
	UpdatedAt string
}

type SchedulerDefinitionSource interface {
	ListSchedulerDefinitions(context.Context) ([]SchedulerPublishedDefinition, error)
}

type schedulerDefinitionSourceAdapter struct {
	definitions metadatasdk.Definitions
	authored    []schedulersdk.Definition
}

// Status reports the Runtime-side definition projection only. Scheduler service
// owns execution health, clocks, runs, retry state, and dead letters.
func (s schedulerDefinitionSourceAdapter) Status(ctx context.Context, scope principalmodel.SystemScope) (map[string]any, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, apperror.New(apperror.KindForbidden, "backend.system_scope_required", err, nil)
	}
	status := map[string]any{
		"execution_owner":       "domainry-scheduler",
		"definitions_available": s.authored != nil || s.definitions != nil,
	}
	definitions, err := s.ListSchedulerDefinitions(ctx)
	if err != nil {
		status["definition_error"] = err.Error()
		return status, nil
	}
	enabled := 0
	for _, definition := range definitions {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(definition.Data["status"])), "enabled") {
			enabled++
		}
	}
	status["definitions"] = len(definitions)
	status["enabled_definitions"] = enabled
	return status, nil
}

func (s schedulerDefinitionSourceAdapter) ListSchedulerDefinitions(ctx context.Context) ([]SchedulerPublishedDefinition, error) {
	if s.authored != nil {
		return schedulerRegistryRecords(s.authored)
	}
	if s.definitions == nil {
		return nil, nil
	}
	definitions, err := s.definitions.List(ctx, metadatasdk.DefinitionQuery{Owner: metadatasdk.DefinitionOwnerScheduler, ResourceType: "scheduler"})
	if err != nil {
		return nil, err
	}
	records := make([]SchedulerPublishedDefinition, 0, len(definitions))
	for _, definition := range definitions {
		record, decodeErr := schedulerMetadataRecord(definition.ResourceKey, definition.Payload, definition.CreatedAt, definition.UpdatedAt)
		if decodeErr != nil {
			return nil, decodeErr
		}
		records = append(records, record)
	}
	return records, nil
}

func (s schedulerDefinitionSourceAdapter) GetSchedulerDefinition(ctx context.Context, key string) (SchedulerPublishedDefinition, bool, error) {
	if s.authored != nil {
		records, err := schedulerRegistryRecords(s.authored)
		if err != nil {
			return SchedulerPublishedDefinition{}, false, err
		}
		key = strings.TrimSpace(key)
		for _, record := range records {
			if record.Key == key {
				return record, true, nil
			}
		}
		return SchedulerPublishedDefinition{}, false, nil
	}
	if s.definitions == nil {
		return SchedulerPublishedDefinition{}, false, nil
	}
	definition, found, err := s.definitions.Get(ctx, metadatasdk.DefinitionOwnerScheduler, "scheduler", key)
	if err != nil || !found {
		return SchedulerPublishedDefinition{}, found, err
	}
	record, err := schedulerMetadataRecord(definition.ResourceKey, definition.Payload, definition.CreatedAt, definition.UpdatedAt)
	return record, err == nil, err
}

func schedulerAuthoredRecords(definitions []map[string]any) ([]SchedulerPublishedDefinition, error) {
	records := make([]SchedulerPublishedDefinition, 0, len(definitions))
	for _, definition := range definitions {
		key := strings.TrimSpace(fmt.Sprint(definition["key"]))
		if key == "" {
			return nil, fmt.Errorf("Scheduler manifest definition key is required")
		}
		revision := strings.TrimSpace(fmt.Sprint(definition["revision"]))
		if revision == "" || revision == "<nil>" {
			revision = "published"
		}
		records = append(records, SchedulerPublishedDefinition{Key: key, Data: cloneSchedulerDefinitionMap(definition), CreatedAt: revision, UpdatedAt: revision})
	}
	return records, nil
}

func schedulerRegistryRecords(definitions []schedulersdk.Definition) ([]SchedulerPublishedDefinition, error) {
	records := make([]SchedulerPublishedDefinition, 0, len(definitions))
	for _, definition := range definitions {
		payload, err := json.Marshal(definition)
		if err != nil {
			return nil, fmt.Errorf("encode Scheduler registry definition %q: %w", definition.Key, err)
		}
		data := map[string]any{}
		if err := json.Unmarshal(payload, &data); err != nil {
			return nil, fmt.Errorf("project Scheduler registry definition %q: %w", definition.Key, err)
		}
		revision := strings.TrimSpace(definition.Revision)
		if revision == "" {
			revision = "published"
		}
		records = append(records, SchedulerPublishedDefinition{Key: definition.Key, Data: data, CreatedAt: revision, UpdatedAt: revision})
	}
	return records, nil
}

func schedulerMetadataRecord(key string, payload []byte, createdAt, updatedAt string) (SchedulerPublishedDefinition, error) {
	data := map[string]any{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return SchedulerPublishedDefinition{}, fmt.Errorf("decode scheduler definition %s: %w", key, err)
	}
	data["key"] = key
	return SchedulerPublishedDefinition{Key: key, Data: data, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

func schedulerPublishedDefinitionRecords(definitions []SchedulerPublishedDefinition) []recordmodel.Record {
	records := make([]recordmodel.Record, 0, len(definitions))
	for _, definition := range definitions {
		records = append(records, recordmodel.Record{ID: definition.Key, Data: cloneSchedulerDefinitionMap(definition.Data), CreatedAt: definition.CreatedAt, UpdatedAt: definition.UpdatedAt})
	}
	return records
}

func newTargetExecutionApplicationService(runtime dispatchapplication.WorkflowTargetRuntime, _ workerplatform.Dependencies) *dispatchapplication.TargetExecutionApplicationService {
	return dispatchapplication.NewTargetExecutionApplicationService(runtime)
}

type scheduledWorkflowRuntimeAdapter struct {
	processExecutions                  func(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
	processTargetedExecutions          func(context.Context, string, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
	processTargetedExecutionsForWindow func(context.Context, string, time.Time, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
	processTargetedWindowWithKey       func(context.Context, string, time.Time, int, principalmodel.Principal, string) (workflowmodel.WorkflowProcessResult, error)
}

var _ dispatchapplication.WorkflowTargetRuntime = scheduledWorkflowRuntimeAdapter{}

func (adapter scheduledWorkflowRuntimeAdapter) ExecuteWorkflowTarget(ctx context.Context, request dispatchapplication.WorkflowTargetRequest) (workflowmodel.WorkflowProcessResult, error) {
	if !request.EffectiveAt.IsZero() {
		if adapter.processTargetedWindowWithKey != nil {
			return adapter.processTargetedWindowWithKey(ctx, request.Operation, request.EffectiveAt, request.Limit, request.Principal, request.IdempotencyKey)
		}
		return adapter.ProcessDueWorkflowExecutionsForScheduledWindow(ctx, request.Operation, request.EffectiveAt, request.Limit, request.Principal)
	}
	return adapter.ProcessDueWorkflowExecutionsForTarget(ctx, request.Operation, request.Limit, request.Principal)
}

func (adapter scheduledWorkflowRuntimeAdapter) ProcessDueWorkflowExecutions(ctx context.Context, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return adapter.processExecutions(ctx, limit, principal)
}

func (adapter scheduledWorkflowRuntimeAdapter) ProcessDueWorkflowExecutionsForTarget(ctx context.Context, targetKey string, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	if adapter.processTargetedExecutions == nil {
		return adapter.ProcessDueWorkflowExecutions(ctx, limit, principal)
	}
	return adapter.processTargetedExecutions(ctx, targetKey, limit, principal)
}

func (adapter scheduledWorkflowRuntimeAdapter) ProcessDueWorkflowExecutionsForScheduledWindow(ctx context.Context, targetKey string, scheduledFor time.Time, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	if adapter.processTargetedExecutionsForWindow == nil {
		return adapter.ProcessDueWorkflowExecutionsForTarget(ctx, targetKey, limit, principal)
	}
	return adapter.processTargetedExecutionsForWindow(ctx, targetKey, scheduledFor, limit, principal)
}

func schemaObjectMap(objects []definitionmodel.ObjectSchema) map[string]definitionmodel.ObjectSchema {
	result := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, object := range objects {
		result[object.Key] = object
	}
	return result
}
