package scheduler

import metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"errors"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"
	"time"
)

type schedulerContextProbe struct {
	recordrepository.RecordRepository
	getCalls, listCalls, insertCalls int
}

func (p *schedulerContextProbe) ListRecords(ctx context.Context, _ string, _ definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if err := ctx.Err(); err != nil {
		return recordmodel.RecordPageResult{}, err
	}
	p.listCalls++
	return recordmodel.RecordPageResult{}, nil
}
func (p *schedulerContextProbe) GetRecord(ctx context.Context, _ string, _ definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return recordmodel.Record{}, false, err
	}
	p.getCalls++
	return recordmodel.Record{}, false, nil
}
func (p *schedulerContextProbe) InsertRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record) error {
	p.insertCalls++
	return nil
}

type schedulerMutationProbe struct{ repository *schedulerContextProbe }

func (p schedulerMutationProbe) InsertSchedulerRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.repository.InsertRecord(ctx, workspaceID, object, record)
}
func (p schedulerMutationProbe) UpdateSchedulerRecord(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
	return nil
}
func (schedulerMutationProbe) ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return workflowmodel.WorkflowProcessResult{}, nil
}
func TestSchedulerClaimRejectsCancellationBeforeLegacySQL(t *testing.T) {
	probe := &schedulerContextProbe{}
	objects := []definitionmodel.ObjectSchema{{Key: "job_definition"}, {Key: "job_run"}, {Key: "job_run_event"}}
	schema := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: objects}}
	service := NewSchedulerApplicationService(schema, schedulerMutationProbe{repository: probe}, probe, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	definition := recordmodel.Record{ID: "definition_1", Data: map[string]any{"key": "definition_1", "max_attempts": 3}}
	if _, _, err := service.claimRun(ctx, "default", definition, "scheduler", time.Now().UTC()); !errors.Is(err, context.Canceled) {
		t.Fatalf("scheduler claim error=%v, want context.Canceled", err)
	}
	if _, err := service.dueDefinitions(ctx, "default", objects[0], time.Now().UTC()); !errors.Is(err, context.Canceled) {
		t.Fatalf("scheduler definition scan error=%v, want context.Canceled", err)
	}
	if err := service.appendRunEvent(ctx, "default", "run_1", "cancelled", "cancelled", time.Now().UTC(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("scheduler event insert error=%v, want context.Canceled", err)
	}
	if probe.getCalls != 0 || probe.listCalls != 0 || probe.insertCalls != 0 {
		t.Fatalf("cancelled scheduler persistence reached legacy SQL: get=%d list=%d insert=%d", probe.getCalls, probe.listCalls, probe.insertCalls)
	}
}
