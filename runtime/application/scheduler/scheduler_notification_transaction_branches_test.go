package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestSchedulerNotificationCommitFailureBranches(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 2, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "minute-job", UpdatedAt: "definition-v1", Data: map[string]any{"name": "Minute job", "schedule_type": "interval", "interval_seconds": 60, "owner_user_id": "owner-a", "next_run_at": "2026-07-28T12:00:00Z"}}
	lateRun := recordmodel.Record{ID: "late-run", Data: map[string]any{"scheduler_definition_key": definition.ID, "triggered_by": "scheduler", "scheduled_for": "2026-07-28T12:00:00Z", "started_at": "2026-07-28T12:01:00Z"}}
	timelyRun := recordmodel.Record{ID: "timely-run", Data: map[string]any{"scheduler_definition_key": definition.ID, "triggered_by": "scheduler", "scheduled_for": "2026-07-28T12:00:00Z", "started_at": "2026-07-28T12:00:10Z"}}
	source := schedulerDefinitionSourceFunc{get: func(context.Context, string) (recordmodel.Record, bool, error) { return definition, true, nil }}
	newService := func(repository *schedulerRepositoryFake, schema schedulerSchemaStub) *SchedulerApplicationService {
		service := NewSchedulerApplicationService(schema, nil, repository, nil)
		service.UseDefinitionSource(source)
		return service
	}
	compiler := func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{ID: intent.ID, EventType: intent.EventType}, nil
	}

	t.Run("missed deadline compile failure", func(t *testing.T) {
		wantErr := errors.New("missed compile failed")
		service := newService(&schedulerRepositoryFake{}, schedulerTestSchema())
		service.UseNotificationCompiler(func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
			if intent.EventType == "scheduler.job.missed_deadline" {
				return notificationmodel.NotificationEvent{}, wantErr
			}
			return compiler(intent)
		})
		if err := service.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, lateRun, nil, nil, "succeeded", "done", now, nil); !errors.Is(err, wantErr) {
			t.Fatalf("commit error=%v", err)
		}
	})

	t.Run("run notification compile failure", func(t *testing.T) {
		wantErr := errors.New("run compile failed")
		service := newService(&schedulerRepositoryFake{}, schedulerTestSchema())
		service.UseNotificationCompiler(func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
			return notificationmodel.NotificationEvent{}, wantErr
		})
		if err := service.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, timelyRun, nil, nil, "retrying", "failed", now, nil); !errors.Is(err, wantErr) {
			t.Fatalf("commit error=%v", err)
		}
	})

	t.Run("run notification appended", func(t *testing.T) {
		repository := &schedulerRepositoryFake{}
		service := newService(repository, schedulerTestSchema())
		service.UseNotificationCompiler(compiler)
		if err := service.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, timelyRun, nil, nil, "retrying", "failed", now, nil); err != nil {
			t.Fatal(err)
		}
		if len(repository.committed) != 1 || len(repository.committed[0][0].NotificationEvents) != 1 || repository.committed[0][0].NotificationEvents[0].EventType != "scheduler.job.failed" {
			t.Fatalf("commits=%+v", repository.committed)
		}
	})

	t.Run("final event object unavailable", func(t *testing.T) {
		service := NewSchedulerApplicationService(schedulerSchemaStub{}, nil, &schedulerRepositoryFake{}, nil)
		if err := service.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, recordmodel.Record{ID: "run", Data: map[string]any{"scheduler_definition_key": ""}}, nil, nil, "succeeded", "done", now, nil); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("event object error=%v", err)
		}
	})
}

func TestSchedulerCursorPreparationFailureBranches(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 2, 0, 0, time.UTC)
	run := recordmodel.Record{ID: "run", Data: map[string]any{"scheduler_definition_key": "definition", "scheduled_for": "2026-07-28T12:00:00Z", "started_at": "2026-07-28T12:00:10Z"}}
	definition := recordmodel.Record{ID: "definition", Data: map[string]any{"schedule_type": "interval", "interval_seconds": 60}}
	source := schedulerDefinitionSourceFunc{get: func(context.Context, string) (recordmodel.Record, bool, error) { return definition, true, nil }}

	service := &SchedulerApplicationService{schema: schedulerTestSchema(), repository: &schedulerRepositoryFake{}}
	if _, err := service.prepareDefinitionCursorCommits(t.Context(), "workspace-a", run, "succeeded", now); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("missing source error=%v", err)
	}

	wantErr := errors.New("cursor failed")
	repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, wantErr
	}}
	service = NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
	service.UseDefinitionSource(source)
	if _, err := service.prepareDefinitionCursorCommits(t.Context(), "workspace-a", run, "succeeded", now); !errors.Is(err, wantErr) {
		t.Fatalf("cursor error=%v", err)
	}

	partial := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "scheduler_cursor"}}}}
	service = NewSchedulerApplicationService(partial, nil, &schedulerRepositoryFake{}, nil)
	service.UseDefinitionSource(source)
	if _, err := service.prepareDefinitionCursorCommits(t.Context(), "workspace-a", run, "succeeded", now); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
		t.Fatalf("cursor event object error=%v", err)
	}
}

func TestSchedulerLegacyCursorFailureBranches(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 2, 0, 0, time.UTC)
	run := recordmodel.Record{ID: "run", Data: map[string]any{"scheduler_definition_key": "definition"}}
	definition := recordmodel.Record{ID: "definition", Data: map[string]any{"schedule_type": "interval", "interval_seconds": 60}}
	source := schedulerDefinitionSourceFunc{get: func(context.Context, string) (recordmodel.Record, bool, error) { return definition, true, nil }}

	service := &SchedulerApplicationService{schema: schedulerTestSchema(), repository: &schedulerRepositoryFake{}}
	if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", run, "succeeded", now); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("missing source error=%v", err)
	}

	wantErr := errors.New("cursor read failed")
	repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, wantErr
	}}
	service = NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
	service.UseDefinitionSource(source)
	if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", run, "succeeded", now); !errors.Is(err, wantErr) {
		t.Fatalf("cursor read error=%v", err)
	}

	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{ID: "definition", UpdatedAt: "old", Data: map[string]any{"next_run_at": "2026-07-28T12:00:00Z"}}, true, nil
	}
	service.updateRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
		return wantErr
	}
	if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", run, "succeeded", now); !errors.Is(err, wantErr) {
		t.Fatalf("cursor update error=%v", err)
	}

	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, nil
	}
	service.updateRecord = nil
	service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
		return nil
	}
	if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", run, "succeeded", now); err != nil {
		t.Fatalf("cursor insert success=%v", err)
	}
}

func TestSchedulerRuntimeAvailabilityNilDependencyBranches(t *testing.T) {
	var nilService *SchedulerApplicationService
	if nilService.RuntimeAvailable(t.Context(), schedulerTestPrincipal("read")) {
		t.Fatal("nil service reported available")
	}
	if (&SchedulerApplicationService{}).RuntimeAvailable(t.Context(), schedulerTestPrincipal("read")) {
		t.Fatal("nil schema reported available")
	}
	if (&SchedulerApplicationService{schema: schedulerTestSchema()}).RuntimeAvailable(t.Context(), schedulerTestPrincipal("read")) {
		t.Fatal("nil definition source reported available")
	}
	service := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil)
	if service.RuntimeAvailable(t.Context(), principalmodel.Principal{}) {
		t.Fatal("unauthorized principal reported available")
	}
}
