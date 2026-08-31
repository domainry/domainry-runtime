package scheduler

import (
	"context"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type schedulerAuthoringRuntime struct{}

func (r schedulerAuthoringRuntime) ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return workflowmodel.WorkflowProcessResult{}, nil
}

type schedulerAuthoringDefinitionSource struct {
	definition PublishedDefinition
	versions   []SchedulerDefinitionVersion
}

func (s schedulerAuthoringDefinitionSource) ListSchedulerDefinitions(context.Context) ([]PublishedDefinition, error) {
	return []PublishedDefinition{s.definition}, nil
}
func (s schedulerAuthoringDefinitionSource) GetSchedulerDefinition(_ context.Context, key string) (PublishedDefinition, bool, error) {
	return s.definition, s.definition.Key == key, nil
}
func (s schedulerAuthoringDefinitionSource) ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error) {
	return s.versions, nil
}

func TestSchedulerDefinitionReadsPublishedMetadataAndVersions(t *testing.T) {
	service := NewSchedulerApplicationService(schedulerAuthoringRuntime{})
	service.UseDefinitionSource(schedulerAuthoringDefinitionSource{
		definition: PublishedDefinition{Key: "daily", Data: map[string]any{"key": "daily", "target_type": "workflow", "target_key": "scheduled:daily"}},
		versions:   []SchedulerDefinitionVersion{{VersionID: "v1", Event: "metadata_version", Data: map[string]any{"key": "daily"}}},
	})
	principal := schedulerTestPrincipal("workspace.admin")
	loaded, err := service.GetDefinition(t.Context(), "daily", principal)
	if err != nil || loaded.Key != "daily" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	versions, err := service.DefinitionVersions(t.Context(), "daily", principal)
	if err != nil || len(versions) != 1 || versions[0].VersionID != "v1" {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
}
