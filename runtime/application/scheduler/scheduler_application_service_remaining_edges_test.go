package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type schedulerDefinitionHistoryStub struct{}

func (schedulerDefinitionHistoryStub) Events(context.Context, auditmodel.AuditEventQuery, principalmodel.Principal) ([]auditmodel.AuditEvent, error) {
	return nil, nil
}

func TestSchedulerRunNotificationUsesExplicitOwnersAndRecoveryState(t *testing.T) {
	definition := recordmodel.Record{ID: "nightly", UpdatedAt: "definition-v2", Data: map[string]any{"name": "Nightly import", "owner_user_ids": []any{"owner-b", "owner-a", "owner-a"}}}
	source := schedulerDefinitionSourceFunc{get: func(context.Context, string) (recordmodel.Record, bool, error) { return definition, true, nil }}
	repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
		if object.Key == "scheduler_cursor" {
			return recordmodel.Record{ID: definition.ID, Data: map[string]any{"last_run_status": "dead_letter"}}, true, nil
		}
		return recordmodel.Record{}, false, nil
	}}
	service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
	service.UseDefinitionSource(source)
	var intents []notificationmodel.NotificationIntent
	service.UseNotificationCompiler(func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		intents = append(intents, intent)
		return notificationmodel.NotificationEvent{ID: intent.ID, EventType: intent.EventType, RecipientUserIDs: intent.RecipientUserIDs, GroupKey: intent.GroupKey, ActionState: intent.ActionState}, nil
	})
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	run := recordmodel.Record{ID: "run-3", UpdatedAt: "run-v3", Data: map[string]any{"scheduler_definition_key": definition.ID, "attempt": 3, "scheduled_for": "2026-07-28T11:00:00Z", "error_category": "timeout"}}
	event, ok, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "retrying", now)
	if err != nil || !ok || event.EventType != "scheduler.job.repeated_failure" || event.GroupKey != "scheduler_job:nightly" || len(event.RecipientUserIDs) != 2 || event.RecipientUserIDs[0] != "owner-a" {
		t.Fatalf("failure event=%+v ok=%v intents=%+v err=%v", event, ok, intents, err)
	}
	event, ok, err = service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "succeeded", now)
	if err != nil || !ok || event.EventType != "scheduler.job.recovered" || event.ActionState != notificationmodel.NotificationActionCompleted {
		t.Fatalf("recovery event=%+v ok=%v err=%v", event, ok, err)
	}
}

func TestSchedulerRunNotificationFailureBoundaries(t *testing.T) {
	var nilService *SchedulerApplicationService
	nilService.UseNotificationCompiler(nil)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	run := recordmodel.Record{ID: "run", Data: map[string]any{"scheduler_definition_key": "definition", "attempt": 1, "scheduled_for": "2026-07-28T12:00:00Z", "started_at": "2026-07-28T12:00:10Z"}}
	compiler := func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{ID: intent.ID, EventType: intent.EventType}, nil
	}

	service := &SchedulerApplicationService{}
	if _, ok, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "retrying", now); err != nil || ok {
		t.Fatalf("missing compiler ok=%v err=%v", ok, err)
	}
	service.UseNotificationCompiler(compiler)
	if _, ok, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "retrying", now); err != nil || ok {
		t.Fatalf("missing definitions ok=%v err=%v", ok, err)
	}

	definition := recordmodel.Record{ID: "definition", Data: map[string]any{"name": "Job", "schedule_type": "interval", "interval_seconds": 60, "owner_user_id": "owner"}}
	var sourceErr error
	found := true
	source := schedulerDefinitionSourceFunc{get: func(context.Context, string) (recordmodel.Record, bool, error) { return definition, found, sourceErr }}
	service.UseDefinitionSource(source)
	sourceErr = errors.New("source failed")
	if _, _, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "retrying", now); !errors.Is(err, sourceErr) {
		t.Fatalf("source error=%v", err)
	}
	sourceErr, found = nil, false
	if _, ok, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "retrying", now); err != nil || ok {
		t.Fatalf("missing definition ok=%v err=%v", ok, err)
	}
	found = true
	definition.Data["owner_user_id"] = ""
	if _, ok, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "retrying", now); err != nil || ok {
		t.Fatalf("missing recipients ok=%v err=%v", ok, err)
	}
	definition.Data["owner_user_id"] = "owner"
	event, ok, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "retrying", now)
	if err != nil || !ok || event.EventType != "scheduler.job.failed" {
		t.Fatalf("first failure event=%+v ok=%v err=%v", event, ok, err)
	}
	definition.Data["owner_user_ids"] = []string{"owner-2"}
	if event, ok, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "dead_letter", now); err != nil || !ok || event.EventType != "scheduler.job.repeated_failure" {
		t.Fatalf("dead-letter event=%+v ok=%v err=%v", event, ok, err)
	}
	if event, ok, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "cancelled", now); err != nil || ok || event.ID != "" {
		t.Fatalf("unsupported status event=%+v ok=%v err=%v", event, ok, err)
	}

	partial := NewSchedulerApplicationService(schedulerSchemaStub{}, nil, &schedulerRepositoryFake{}, nil)
	partial.UseDefinitionSource(source)
	partial.UseNotificationCompiler(compiler)
	if _, _, err := partial.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "succeeded", now); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
		t.Fatalf("cursor object error=%v", err)
	}
	cursorErr := errors.New("cursor failed")
	repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, cursorErr
	}}
	service = NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
	service.UseDefinitionSource(source)
	service.UseNotificationCompiler(compiler)
	if _, _, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "succeeded", now); !errors.Is(err, cursorErr) {
		t.Fatalf("cursor read error=%v", err)
	}

	for _, cursor := range []struct {
		name  string
		value recordmodel.Record
		found bool
	}{
		{name: "missing", found: false},
		{name: "healthy", value: recordmodel.Record{Data: map[string]any{"last_run_status": "succeeded", "last_run_missed_deadline": false}}, found: true},
	} {
		t.Run(cursor.name, func(t *testing.T) {
			repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
				return cursor.value, cursor.found, nil
			}
			if event, ok, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "succeeded", now); err != nil || ok || event.ID != "" {
				t.Fatalf("healthy result=%+v ok=%v err=%v", event, ok, err)
			}
		})
	}
	compileErr := errors.New("compile failed")
	service.UseNotificationCompiler(func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, compileErr
	})
	if _, ok, err := service.compileSchedulerRunNotification(t.Context(), "workspace-a", run, "retrying", now); !errors.Is(err, compileErr) || ok {
		t.Fatalf("compile result ok=%v err=%v", ok, err)
	}
}

func TestSchedulerMissedDeadlineNotificationAndRecovery(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 2, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "minute-job", UpdatedAt: "definition-v1", Data: map[string]any{"name": "Minute job", "schedule_type": "interval", "interval_seconds": 60, "owner_user_id": "owner-a"}}
	var sourceErr error
	found := true
	source := schedulerDefinitionSourceFunc{get: func(context.Context, string) (recordmodel.Record, bool, error) { return definition, found, sourceErr }}
	repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
		if object.Key == "scheduler_cursor" {
			return recordmodel.Record{ID: definition.ID, Data: map[string]any{"last_run_status": "succeeded", "last_run_missed_deadline": true}}, true, nil
		}
		return recordmodel.Record{}, false, nil
	}}
	service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
	service.UseDefinitionSource(source)
	var intents []notificationmodel.NotificationIntent
	service.UseNotificationCompiler(func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		intents = append(intents, intent)
		return notificationmodel.NotificationEvent{ID: intent.ID, EventType: intent.EventType, SourceEventID: intent.SourceEventID, GroupKey: intent.GroupKey, RecipientUserIDs: intent.RecipientUserIDs, AlertState: intent.AlertState}, nil
	})
	lateRun := recordmodel.Record{ID: "late-run", Data: map[string]any{"scheduler_definition_key": definition.ID, "triggered_by": "scheduler", "scheduled_for": "2026-07-28T12:00:00Z", "started_at": "2026-07-28T12:01:00Z"}}
	event, ok, err := service.compileSchedulerMissedDeadlineNotification(t.Context(), "workspace-a", lateRun, "succeeded", now)
	if err != nil || !ok || event.EventType != "scheduler.job.missed_deadline" || event.SourceEventID != "late-run:missed_deadline:2026-07-28T12:00:00Z" || event.GroupKey != "scheduler_job:minute-job" || event.AlertState != notificationmodel.NotificationAlertFiring {
		t.Fatalf("missed deadline event=%+v ok=%v intents=%+v err=%v", event, ok, intents, err)
	}
	if recovery, recovered, recoveryErr := service.compileSchedulerRunNotification(t.Context(), "workspace-a", lateRun, "succeeded", now); recoveryErr != nil || recovered || recovery.ID != "" {
		t.Fatalf("late run incorrectly recovered alert event=%+v ok=%v err=%v", recovery, recovered, recoveryErr)
	}

	timelyRun := lateRun
	timelyRun.ID = "timely-run"
	timelyRun.Data = map[string]any{"scheduler_definition_key": definition.ID, "triggered_by": "scheduler", "scheduled_for": "2026-07-28T12:01:00Z", "started_at": "2026-07-28T12:01:30Z"}
	recovery, recovered, recoveryErr := service.compileSchedulerRunNotification(t.Context(), "workspace-a", timelyRun, "succeeded", now)
	if recoveryErr != nil || !recovered || recovery.EventType != "scheduler.job.recovered" {
		t.Fatalf("timely recovery event=%+v ok=%v err=%v", recovery, recovered, recoveryErr)
	}

	for _, test := range []struct {
		name string
		run  recordmodel.Record
		want bool
	}{
		{name: "manual", run: recordmodel.Record{Data: map[string]any{"triggered_by": "manual_run", "scheduled_for": "2026-07-28T12:00:00Z", "started_at": "2026-07-28T12:02:00Z"}}},
		{name: "bad scheduled", run: recordmodel.Record{Data: map[string]any{"scheduled_for": "bad", "started_at": "2026-07-28T12:02:00Z"}}},
		{name: "bad started", run: recordmodel.Record{Data: map[string]any{"scheduled_for": "2026-07-28T12:00:00Z", "started_at": "bad"}}},
		{name: "clock before schedule", run: recordmodel.Record{Data: map[string]any{"scheduled_for": "2026-07-28T12:00:00Z", "started_at": "2026-07-28T11:59:59Z"}}},
		{name: "poll jitter", run: timelyRun},
		{name: "whole window", run: lateRun, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := schedulerRunMissedDeadline(definition, test.run); got != test.want {
				t.Fatalf("missed deadline=%v want=%v", got, test.want)
			}
		})
	}

	if _, ok, err := service.compileSchedulerMissedDeadlineNotification(t.Context(), "workspace-a", timelyRun, "retrying", now); err != nil || ok {
		t.Fatalf("non-success notification ok=%v err=%v", ok, err)
	}
	withoutCompiler := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
	withoutCompiler.UseDefinitionSource(source)
	if _, ok, err := withoutCompiler.compileSchedulerMissedDeadlineNotification(t.Context(), "workspace-a", lateRun, "succeeded", now); err != nil || ok {
		t.Fatalf("missing compiler ok=%v err=%v", ok, err)
	}
	withoutDefinitions := &SchedulerApplicationService{compileNotification: func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, nil
	}}
	if _, ok, err := withoutDefinitions.compileSchedulerMissedDeadlineNotification(t.Context(), "workspace-a", lateRun, "succeeded", now); err != nil || ok {
		t.Fatalf("missing definitions ok=%v err=%v", ok, err)
	}

	sourceErr = errors.New("definition failed")
	if _, _, err := service.compileSchedulerMissedDeadlineNotification(t.Context(), "workspace-a", lateRun, "succeeded", now); !errors.Is(err, sourceErr) {
		t.Fatalf("source error=%v", err)
	}
	sourceErr, found = nil, false
	if _, ok, err := service.compileSchedulerMissedDeadlineNotification(t.Context(), "workspace-a", lateRun, "succeeded", now); err != nil || ok {
		t.Fatalf("missing definition ok=%v err=%v", ok, err)
	}
	found = true
	if _, ok, err := service.compileSchedulerMissedDeadlineNotification(t.Context(), "workspace-a", timelyRun, "succeeded", now); err != nil || ok {
		t.Fatalf("timely run notification ok=%v err=%v", ok, err)
	}
	definition.Data["owner_user_id"] = ""
	if _, ok, err := service.compileSchedulerMissedDeadlineNotification(t.Context(), "workspace-a", lateRun, "succeeded", now); err != nil || ok {
		t.Fatalf("missing owner ok=%v err=%v", ok, err)
	}
	definition.Data["owner_user_id"] = "owner-a"
	definition.Data["owner_user_ids"] = []string{"", "owner-a"}
	compileErr := errors.New("compile failed")
	service.UseNotificationCompiler(func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, compileErr
	})
	if _, ok, err := service.compileSchedulerMissedDeadlineNotification(t.Context(), "workspace-a", lateRun, "succeeded", now); !errors.Is(err, compileErr) || ok {
		t.Fatalf("compile error ok=%v err=%v", ok, err)
	}
}

func TestSchedulerMissedDeadlineIntentSharesRunTransaction(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 2, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "minute-job", UpdatedAt: "definition-v1", Data: map[string]any{"name": "Minute job", "schedule_type": "interval", "interval_seconds": 60, "owner_user_id": "owner-a", "next_run_at": "2026-07-28T12:00:00Z"}}
	repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
		switch object.Key {
		case "job_definition":
			return definition, true, nil
		case "scheduler_cursor":
			return recordmodel.Record{}, false, nil
		default:
			return recordmodel.Record{}, false, nil
		}
	}}
	service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
	service.UseDefinitionSource(repository)
	service.UseNotificationCompiler(func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{ID: intent.ID, EventType: intent.EventType, SourceEventID: intent.SourceEventID}, nil
	})
	run := recordmodel.Record{ID: "late-run", Data: map[string]any{"scheduler_definition_key": definition.ID, "triggered_by": "scheduler", "scheduled_for": "2026-07-28T12:00:00Z", "started_at": "2026-07-28T12:01:00Z"}}
	if err := service.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, run, nil, nil, "succeeded", "done", now, map[string]any{"status": "leased"}); err != nil {
		t.Fatal(err)
	}
	if len(repository.committed) != 1 || len(repository.committed[0]) < 3 {
		t.Fatalf("transaction batches=%+v", repository.committed)
	}
	root := repository.committed[0][0]
	if len(root.NotificationEvents) != 1 || root.NotificationEvents[0].EventType != "scheduler.job.missed_deadline" {
		t.Fatalf("run transaction notifications=%+v", root.NotificationEvents)
	}
	if got := repository.committed[0][1].Record.Data["last_run_missed_deadline"]; got != true {
		t.Fatalf("cursor missed-deadline state=%v commits=%+v", got, repository.committed[0])
	}
}

type schedulerDefinitionSourceFunc struct {
	get  func(context.Context, string) (recordmodel.Record, bool, error)
	list func(context.Context) ([]recordmodel.Record, error)
}

func (s schedulerDefinitionSourceFunc) ListSchedulerDefinitions(ctx context.Context) ([]recordmodel.Record, error) {
	if s.list != nil {
		return s.list(ctx)
	}
	return nil, nil
}

func (s schedulerDefinitionSourceFunc) GetSchedulerDefinition(ctx context.Context, key string) (recordmodel.Record, bool, error) {
	if s.get != nil {
		return s.get(ctx, key)
	}
	return recordmodel.Record{}, false, nil
}

func (schedulerDefinitionSourceFunc) ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error) {
	return nil, nil
}

func TestSchedulerApplicationDefinitionReadBoundaries(t *testing.T) {
	var nilService *SchedulerApplicationService
	nilService.UseDefinitionHistoryReader(schedulerDefinitionHistoryStub{})
	nilService.UseDefinitionSource(schedulerSurfaceDefinitionSource{})

	service := &SchedulerApplicationService{}
	history := schedulerDefinitionHistoryStub{}
	service.UseDefinitionHistoryReader(history)
	if service.definitionHistory == nil {
		t.Fatal("definition history reader was not installed")
	}

	readPrincipal := schedulerTestPrincipal("scheduler.definition.read")
	if _, err := service.GetDefinition(t.Context(), "definition", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("anonymous definition read error = %v", err)
	}
	if _, err := service.GetDefinition(t.Context(), "definition", readPrincipal); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("missing definition source error = %v", err)
	}
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{})
	if _, err := service.GetDefinition(t.Context(), "definition", readPrincipal); apperror.CodeOf(err) != "backend.scheduler.definition_not_found" {
		t.Fatalf("missing definition error = %v", err)
	}

	if _, err := (&SchedulerApplicationService{}).PublishedDefinitions(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("anonymous published definitions error = %v", err)
	}
	if _, err := (&SchedulerApplicationService{}).PublishedDefinitions(t.Context(), readPrincipal); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("missing published definition source error = %v", err)
	}
	listFailure := errors.New("definition list failure")
	service.UseDefinitionSource(schedulerDefinitionSourceFunc{list: func(context.Context) ([]recordmodel.Record, error) {
		return nil, listFailure
	}})
	if _, err := service.PublishedDefinitions(t.Context(), readPrincipal); !errors.Is(err, listFailure) {
		t.Fatalf("published definition list error = %v", err)
	}
}

func TestSchedulerDefinitionVersionsDetectsVanishedSource(t *testing.T) {
	principal := schedulerTestPrincipal("scheduler.definition.read")
	service := &SchedulerApplicationService{}
	service.UseDefinitionSource(schedulerDefinitionSourceFunc{
		get: func(context.Context, string) (recordmodel.Record, bool, error) {
			service.definitions = nil
			return recordmodel.Record{ID: "definition"}, true, nil
		},
	})
	if _, err := service.DefinitionVersions(t.Context(), "definition", principal); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("vanished version source error = %v", err)
	}
	if _, err := (&SchedulerApplicationService{}).DefinitionVersions(t.Context(), "definition", principal); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("definition lookup error = %v", err)
	}
}

func TestSchedulerDefinitionReadPermissionAlternatives(t *testing.T) {
	for _, permission := range []string{"workspace.admin", "metadata.read", "scheduler.definition.read"} {
		if err := schedulerDefinitionReadAllowed(schedulerTestPrincipal(permission)); err != nil {
			t.Fatalf("definition read permission %q rejected: %v", permission, err)
		}
	}
	if err := schedulerDefinitionReadAllowed(schedulerTestPrincipal("unrelated.read")); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("unrelated read permission error = %v", err)
	}
}

func TestSchedulerPreviewScheduleFailureBoundaries(t *testing.T) {
	service := NewSchedulerApplicationService(nil, nil, nil, nil)
	principal := schedulerTestPrincipal("scheduler.definition.write")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.PreviewSchedule(cancelled, nil, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preview error = %v", err)
	}
	if _, err := service.PreviewSchedule(t.Context(), nil, schedulerTestPrincipal("metadata.read")); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("preview permission error = %v", err)
	}
	if _, err := service.PreviewSchedule(t.Context(), map[string]any{"timezone": "not/a-real-timezone"}, principal); err == nil {
		t.Fatal("invalid schedule fragment was previewed")
	}
}

func TestSchedulerOperationAuditPreservesExplicitReason(t *testing.T) {
	audit := &schedulerAuditSpy{}
	service := NewSchedulerApplicationService(nil, nil, nil, audit)
	service.insertOperationAudit(
		t.Context(),
		"scheduler_test",
		"job_run",
		"run-1",
		schedulerTestPrincipal("scheduler.command"),
		"fallback summary",
		nil,
		nil,
		map[string]any{"reason": "explicit reason"},
	)
	if len(audit.requests) != 1 || audit.requests[0].Metadata["reason"] != "explicit reason" {
		t.Fatalf("audit request = %+v", audit.requests)
	}
	service.insertOperationAudit(
		t.Context(),
		"scheduler_test_fallback",
		"job_run",
		"run-2",
		schedulerTestPrincipal("scheduler.command"),
		"fallback summary",
		nil,
		nil,
		nil,
	)
	if len(audit.requests) != 2 || audit.requests[1].Metadata["reason"] != "fallback summary" {
		t.Fatalf("fallback audit request = %+v", audit.requests)
	}
	service.insertOperationAudit(
		t.Context(),
		"scheduler_test_blank",
		"job_run",
		"run-3",
		schedulerTestPrincipal("scheduler.command"),
		"blank fallback summary",
		nil,
		nil,
		map[string]any{"reason": "  "},
	)
	if len(audit.requests) != 3 || audit.requests[2].Metadata["reason"] != "blank fallback summary" {
		t.Fatalf("blank audit request = %+v", audit.requests)
	}
}
