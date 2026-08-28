package scheduler

import (
	"context"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type schedulerAuthoringRepository struct {
	record recordmodel.Record
	found  bool
}

func (r *schedulerAuthoringRepository) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, nil
}
func (*schedulerAuthoringRepository) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{}, nil
}
func (*schedulerAuthoringRepository) UpdateRecordWhere(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
	return true, nil
}
func (*schedulerAuthoringRepository) CommitRecordMutationBatch(context.Context, string, []transactionmodel.RecordMutationCommit) error {
	return nil
}

type schedulerAuthoringRuntime struct{ repository *schedulerAuthoringRepository }

func (r schedulerAuthoringRuntime) ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return workflowmodel.WorkflowProcessResult{}, nil
}
func (r schedulerAuthoringRuntime) InsertSchedulerRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record, _ string) error {
	r.repository.record, r.repository.found = record, true
	return nil
}
func (r schedulerAuthoringRuntime) UpdateSchedulerRecord(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record, _ string) error {
	r.repository.record, r.repository.found = record, true
	return nil
}

type schedulerAuthoringDefinitionSource struct {
	record   recordmodel.Record
	versions []SchedulerDefinitionVersion
}

func (s schedulerAuthoringDefinitionSource) ListSchedulerDefinitions(context.Context) ([]recordmodel.Record, error) {
	return []recordmodel.Record{s.record}, nil
}
func (s schedulerAuthoringDefinitionSource) GetSchedulerDefinition(_ context.Context, key string) (recordmodel.Record, bool, error) {
	return s.record, s.record.ID == key, nil
}
func (s schedulerAuthoringDefinitionSource) ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error) {
	return s.versions, nil
}

func TestSchedulerDefinitionReadsPublishedMetadataAndVersions(t *testing.T) {
	repository := &schedulerAuthoringRepository{}
	service := NewSchedulerApplicationService(schedulerTestSchema(), schedulerAuthoringRuntime{repository: repository}, repository, nil)
	service.UseDefinitionSource(schedulerAuthoringDefinitionSource{
		record:   recordmodel.Record{ID: "daily", Data: map[string]any{"key": "daily", "target_type": "workflow", "target_key": "scheduled:daily"}},
		versions: []SchedulerDefinitionVersion{{VersionID: "v1", Event: "metadata_version", Data: map[string]any{"key": "daily"}}},
	})
	principal := schedulerTestPrincipal("workspace.admin")
	loaded, err := service.GetDefinition(t.Context(), "daily", principal)
	if err != nil || loaded.ID != "daily" || repository.found {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	versions, err := service.DefinitionVersions(t.Context(), "daily", principal)
	if err != nil || len(versions) != 1 || versions[0].VersionID != "v1" {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
}
