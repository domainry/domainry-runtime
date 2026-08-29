package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func TestSchedulerAdvanceCursorAndAppendEvent(t *testing.T) {
	now := time.Date(2026, 7, 19, 21, 0, 0, 0, time.UTC)
	run := recordmodel.Record{ID: "run-1", Data: map[string]any{}}
	service := schedulerPersistenceService(&schedulerRepositoryFake{}, now)
	if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", run, "succeeded", now); err != nil {
		t.Fatalf("empty definition cursor = %v", err)
	}

	run.Data["scheduler_definition_key"] = "definition-1"
	wantErr := errors.New("get failed")
	repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, wantErr
	}}
	service.repository = repository
	service.definitions = repository
	if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", run, "failed", now); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("cursor get error = %v", err)
	}
	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, nil
	}
	if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", run, "failed", now); err != nil {
		t.Fatalf("missing definition cursor = %v", err)
	}

	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"schedule_type": "interval", "interval_seconds": 60, "next_run_at": now.Add(-time.Minute).Format(time.RFC3339)}}
	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return definition, true, nil
	}
	updates, events := 0, 0
	repository.update = func(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, conditions map[string]any) (bool, error) {
		updates++
		if workspaceID != "workspace-a" || object.Key != "scheduler_cursor" || record.Data["last_run_status"] != "succeeded" || conditions["next_run_at"] == nil {
			t.Fatalf("cursor update = workspace=%q object=%q record=%+v conditions=%#v", workspaceID, object.Key, record, conditions)
		}
		return true, nil
	}
	service.insertRecord = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.Record, _ string) error {
		if object.Key == "job_run_event" {
			events++
		}
		return nil
	}
	if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", run, "succeeded", now); err != nil || updates != 1 || events != 1 {
		t.Fatalf("cursor success err=%v updates=%d events=%d", err, updates, events)
	}

	if err := service.AppendRunEvent(t.Context(), "run", "state", "message", now, nil, identitySystemScopeZero()); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("append scope error = %v", err)
	}
	if err := service.AppendRunEvent(t.Context(), "run", "state", "message", now, nil, schedulerRuntimeScope()); err != nil {
		t.Fatalf("append event = %v", err)
	}
}

func identitySystemScopeZero() (scope principalmodel.SystemScope) { return scope }

func TestSchedulerDeadLetterPersistenceMatrix(t *testing.T) {
	now := time.Date(2026, 7, 19, 22, 0, 0, 0, time.UTC)
	run := recordmodel.Record{ID: "run-1", Data: map[string]any{"scheduler_definition_key": "fallback-definition", "error_message": "failed"}}
	wantErr := errors.New("get failed")
	repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, wantErr
	}}
	service := schedulerPersistenceService(repository, now)
	if err := service.deadLetterRun(t.Context(), "workspace-a", run, recordmodel.Record{}, "", now); !errors.Is(err, wantErr) {
		t.Fatalf("dead-letter get error = %v", err)
	}
	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{ID: "existing"}, true, nil
	}
	if err := service.deadLetterRun(t.Context(), "workspace-a", run, recordmodel.Record{}, "", now); err != nil {
		t.Fatalf("existing dead letter = %v", err)
	}

	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, nil
	}
	inserted := []recordmodel.Record{}
	service.insertRecord = func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record, _ string) error {
		inserted = append(inserted, record)
		return nil
	}
	if err := service.deadLetterRun(t.Context(), "workspace-a", run, recordmodel.Record{}, "", now); err != nil || len(inserted) != 2 {
		t.Fatalf("dead letter success err=%v inserted=%+v", err, inserted)
	}
	if inserted[0].Data["scheduler_definition_key"] != "fallback-definition" || inserted[0].Data["reason"] != "backend.scheduler.dead_letter" || inserted[1].Data["event_type"] != "dead_lettered" {
		t.Fatalf("dead letter records = %+v", inserted)
	}
}

func TestSchedulerReportExportEvidenceSuccessAndFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 23, 0, 0, 0, time.UTC)
	objects := append([]definitionmodel.ObjectSchema{}, schedulerTestSchema().snapshot.Objects...)
	for _, key := range []string{"report_definition", "report_query_run", "report_export_audit", "download_task"} {
		objects = append(objects, definitionmodel.ObjectSchema{Key: key})
	}
	schema := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: objects}}
	run := recordmodel.Record{ID: "run-1", Data: map[string]any{}}
	repository := &schedulerRepositoryFake{}
	service := NewSchedulerApplicationServiceWithWorker(schema, nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
	inserted := []recordmodel.Record{}
	service.insertRecord = func(_ context.Context, workspaceID string, _ definitionmodel.ObjectSchema, record recordmodel.Record, _ string) error {
		if workspaceID != "workspace-a" {
			t.Fatalf("workspace = %q", workspaceID)
		}
		inserted = append(inserted, record)
		return nil
	}
	evidence, err := service.schedulerProcessReportExportDefinition(t.Context(), "workspace-a", recordmodel.Record{Data: map[string]any{}}, run, now)
	if err != nil || len(evidence) != 3 || len(inserted) != 4 || inserted[0].Data["report_code"] != "scheduled_report_export" {
		t.Fatalf("evidence=%+v inserted=%+v err=%v", evidence, inserted, err)
	}

	existing := recordmodel.Record{ID: "existing"}
	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return existing, true, nil
	}
	got, err := service.schedulerInsertRecordIfMissing(t.Context(), "workspace-a", "report_definition", "id", now, nil)
	if err != nil || got.ID != existing.ID {
		t.Fatalf("existing evidence = %+v, %v", got, err)
	}
	if _, err := service.schedulerInsertRecordIfMissing(t.Context(), "workspace-a", "missing", "id", now, nil); apperror.CodeOf(err) != "backend.scheduler.target_object_missing" {
		t.Fatalf("missing object error = %v", err)
	}
	wantErr := errors.New("store failed")
	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, wantErr
	}
	if _, err := service.schedulerInsertRecordIfMissing(t.Context(), "workspace-a", "report_definition", "id", now, nil); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("get evidence error = %v", err)
	}
	repository.get = nil
	service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
		return wantErr
	}
	if _, err := service.schedulerEnsureReportDefinition(t.Context(), "workspace-a", "report", now); !errors.Is(err, wantErr) {
		t.Fatalf("insert evidence error = %v", err)
	}

	for failAt := 1; failAt <= 4; failAt++ {
		t.Run(fmt.Sprintf("stage-%d", failAt), func(t *testing.T) {
			repository := &schedulerRepositoryFake{}
			service := NewSchedulerApplicationServiceWithWorker(schema, nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
			calls := 0
			service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
				calls++
				if calls == failAt {
					return wantErr
				}
				return nil
			}
			if _, err := service.schedulerProcessReportExportDefinition(t.Context(), "workspace-a", recordmodel.Record{Data: map[string]any{"target_key": "sales"}}, run, now); !errors.Is(err, wantErr) || calls != failAt {
				t.Fatalf("stage %d error=%v calls=%d", failAt, err, calls)
			}
		})
	}
}
