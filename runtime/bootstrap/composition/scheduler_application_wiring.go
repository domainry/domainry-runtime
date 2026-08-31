package composition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type schedulerApplicationDefinitionSource struct {
	definitions metadatasdk.Definitions
	authored    []map[string]any
}

func (s schedulerApplicationDefinitionSource) ListSchedulerDefinitions(ctx context.Context) ([]schedulerapplication.PublishedDefinition, error) {
	if s.authored != nil {
		return schedulerAuthoredRecords(s.authored)
	}
	if s.definitions == nil {
		return nil, nil
	}
	definitions, err := s.definitions.List(ctx, metadatasdk.DefinitionQuery{ResourceType: "scheduler"})
	if err != nil {
		return nil, err
	}
	records := make([]schedulerapplication.PublishedDefinition, 0, len(definitions))
	for _, definition := range definitions {
		record, decodeErr := schedulerMetadataRecord(definition.ResourceKey, definition.Payload, definition.CreatedAt, definition.UpdatedAt)
		if decodeErr != nil {
			return nil, decodeErr
		}
		records = append(records, record)
	}
	return records, nil
}

func (s schedulerApplicationDefinitionSource) GetSchedulerDefinition(ctx context.Context, key string) (schedulerapplication.PublishedDefinition, bool, error) {
	if s.authored != nil {
		records, err := schedulerAuthoredRecords(s.authored)
		if err != nil {
			return schedulerapplication.PublishedDefinition{}, false, err
		}
		key = strings.TrimSpace(key)
		for _, record := range records {
			if record.Key == key {
				return record, true, nil
			}
		}
		return schedulerapplication.PublishedDefinition{}, false, nil
	}
	if s.definitions == nil {
		return schedulerapplication.PublishedDefinition{}, false, nil
	}
	definition, found, err := s.definitions.Get(ctx, "scheduler", key)
	if err != nil || !found {
		return schedulerapplication.PublishedDefinition{}, found, err
	}
	record, err := schedulerMetadataRecord(definition.ResourceKey, definition.Payload, definition.CreatedAt, definition.UpdatedAt)
	return record, err == nil, err
}

func (s schedulerApplicationDefinitionSource) ListSchedulerDefinitionVersions(ctx context.Context, key string) ([]schedulerapplication.SchedulerDefinitionVersion, error) {
	if s.authored != nil {
		record, found, err := s.GetSchedulerDefinition(ctx, key)
		if err != nil || !found {
			return nil, err
		}
		return []schedulerapplication.SchedulerDefinitionVersion{{VersionID: record.UpdatedAt, Event: "manifest_definition", Data: record.Data, CreatedAt: record.CreatedAt}}, nil
	}
	if s.definitions == nil {
		return nil, nil
	}
	record, found, err := s.GetSchedulerDefinition(ctx, key)
	if err != nil || !found {
		return nil, err
	}
	return []schedulerapplication.SchedulerDefinitionVersion{{VersionID: record.UpdatedAt, Event: "manifest_definition", Data: record.Data, CreatedAt: record.CreatedAt}}, nil
}

func schedulerAuthoredRecords(definitions []map[string]any) ([]schedulerapplication.PublishedDefinition, error) {
	records := make([]schedulerapplication.PublishedDefinition, 0, len(definitions))
	for _, definition := range definitions {
		key := strings.TrimSpace(fmt.Sprint(definition["key"]))
		if key == "" {
			return nil, fmt.Errorf("Scheduler manifest definition key is required")
		}
		revision := strings.TrimSpace(fmt.Sprint(definition["revision"]))
		if revision == "" || revision == "<nil>" {
			revision = "published"
		}
		records = append(records, schedulerapplication.PublishedDefinition{Key: key, Data: cloneSchedulerDefinitionMap(definition), CreatedAt: revision, UpdatedAt: revision})
	}
	return records, nil
}

func schedulerMetadataRecord(key string, payload []byte, createdAt, updatedAt string) (schedulerapplication.PublishedDefinition, error) {
	data := map[string]any{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return schedulerapplication.PublishedDefinition{}, fmt.Errorf("decode scheduler definition %s: %w", key, err)
	}
	data["key"] = key
	return schedulerapplication.PublishedDefinition{Key: key, Data: data, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

func schedulerPublishedDefinitionRecords(definitions []schedulerapplication.PublishedDefinition) []recordmodel.Record {
	records := make([]recordmodel.Record, 0, len(definitions))
	for _, definition := range definitions {
		records = append(records, recordmodel.Record{ID: definition.Key, Data: cloneSchedulerDefinitionMap(definition.Data), CreatedAt: definition.CreatedAt, UpdatedAt: definition.UpdatedAt})
	}
	return records
}

func newSchedulerApplicationService(runtime schedulerapplication.ScheduledWorkflowRuntime, worker workerplatform.Dependencies) *schedulerapplication.SchedulerApplicationService {
	return schedulerapplication.NewSchedulerApplicationServiceWithClock(runtime, workerplatform.NormalizeDependencies(worker).Clock)
}

type scheduledWorkflowRuntimeAdapter struct {
	processExecutions                  func(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
	processTargetedExecutions          func(context.Context, string, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
	processTargetedExecutionsForWindow func(context.Context, string, time.Time, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
}

var _ schedulerapplication.ScheduledWorkflowRuntime = scheduledWorkflowRuntimeAdapter{}
var _ schedulerapplication.TargetedScheduledWorkflowRuntime = scheduledWorkflowRuntimeAdapter{}
var _ schedulerapplication.WindowedScheduledWorkflowRuntime = scheduledWorkflowRuntimeAdapter{}

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
