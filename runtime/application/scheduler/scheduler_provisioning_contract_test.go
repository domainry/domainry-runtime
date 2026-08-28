package scheduler

import (
	"context"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func TestPublishedSchedulerDefinitionsProvisionDurableCursorAcrossStartupRestartAndMissedWindow(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	interval := recordmodel.Record{ID: "activation-job", Data: map[string]any{
		"key": "activation-job", "status": "enabled", "schedule_type": "interval", "interval_seconds": 60,
		"target_type": "workflow", "target_key": "scheduled:activate", "max_attempts": 3, "timeout_seconds": 60,
	}}
	missedAt := now.Add(-5 * time.Minute)
	missed := recordmodel.Record{ID: "missed-job", Data: map[string]any{
		"key": "missed-job", "status": "enabled", "schedule_type": "interval", "interval_seconds": 60,
		"target_type": "workflow", "target_key": "scheduled:activate", "max_attempts": 3, "timeout_seconds": 60,
		"missed_window_policy": "catch_up_one", "next_run_at": missedAt.Format(time.RFC3339),
	}}
	disabled := interval
	disabled.ID, disabled.Data = "disabled-job", map[string]any{
		"key": "disabled-job", "status": "disabled", "schedule_type": "interval", "interval_seconds": 60,
		"target_type": "workflow", "target_key": "scheduled:activate", "max_attempts": 3, "timeout_seconds": 60,
	}
	cursors := map[string]recordmodel.Record{}
	repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
		if object.Key == "scheduler_cursor" {
			cursor, found := cursors[id]
			return cursor, found, nil
		}
		return recordmodel.Record{}, false, nil
	}, list: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		if object.Key != "scheduler_cursor" {
			return recordmodel.RecordPageResult{}, nil
		}
		items := make([]recordmodel.Record, 0, len(cursors))
		for _, cursor := range cursors {
			items = append(items, cursor)
		}
		return recordmodel.RecordPageResult{Items: items}, nil
	}}
	source := schedulerDefinitionSourceFunc{list: func(context.Context) ([]recordmodel.Record, error) {
		return []recordmodel.Record{interval, missed, disabled}, nil
	}}
	newService := func() *SchedulerApplicationService {
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{}, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
		service.UseDefinitionSource(source)
		service.insertRecord = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, record recordmodel.Record, _ string) error {
			if object.Key == "scheduler_cursor" {
				cursors[record.ID] = record
			}
			return nil
		}
		return service
	}
	first := newService()
	count, err := first.ProvisionPublishedDefinitions(t.Context(), now, schedulerRuntimeScope())
	if err != nil || count != 2 || len(cursors) != 2 {
		t.Fatalf("startup count=%d cursors=%+v err=%v", count, cursors, err)
	}
	if got := cursors[interval.ID].Data["next_run_at"]; got != now.Add(time.Minute).Format(time.RFC3339) {
		t.Fatalf("initial interval cursor=%v", got)
	}
	if got := cursors[missed.ID].Data["next_run_at"]; got != missedAt.Format(time.RFC3339) {
		t.Fatalf("missed window cursor=%v", got)
	}

	// A new service instance models Runtime restart against the same durable
	// store. Existing anchors are retained and no duplicate cursor is created.
	restarted := newService()
	count, err = restarted.ProvisionPublishedDefinitions(t.Context(), now.Add(30*time.Second), schedulerRuntimeScope())
	if err != nil || count != 0 || len(cursors) != 2 {
		t.Fatalf("restart count=%d cursors=%+v err=%v", count, cursors, err)
	}
	if got := cursors[missed.ID].Data["next_run_at"]; got != missedAt.Format(time.RFC3339) {
		t.Fatalf("restart changed missed cursor=%v", got)
	}
}

func TestSchedulerRuntimeAvailabilityUsesOwnerInfrastructureVisibility(t *testing.T) {
	full := schedulerTestSchema().snapshot
	schema := schedulerPrincipalSchemaFunc(func(_ context.Context, principal principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
		if principal.UserID == "scheduler:worker" {
			return full
		}
		return metadatamodel.MetadataSchemaSnapshot{}
	})
	service := NewSchedulerApplicationService(schema, &schedulerRuntimeFake{}, &schedulerRepositoryFake{}, nil)
	service.UseDefinitionSource(schedulerDefinitionSourceFunc{})
	if !service.RuntimeAvailable(t.Context(), schedulerTestPrincipal("scheduler.command")) {
		t.Fatal("authorized scheduler operator observed a false unprovisioned state")
	}
	if service.RuntimeAvailable(t.Context(), principalmodel.Principal{}) {
		t.Fatal("unauthorized principal observed scheduler infrastructure state")
	}
}

type schedulerPrincipalSchemaFunc func(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot

func (f schedulerPrincipalSchemaFunc) SchemaForPrincipal(ctx context.Context, principal principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
	return f(ctx, principal)
}

type schedulerWindowRuntimeFake struct {
	schedulerRuntimeFake
	target string
	window time.Time
}

func (f *schedulerWindowRuntimeFake) ProcessDueWorkflowExecutionsForScheduledWindow(_ context.Context, target string, scheduledFor time.Time, _ int, _ principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	f.target, f.window = target, scheduledFor
	return f.processResult, f.processErr
}

func TestSchedulerDispatchesDurableRunWindowToTargetWorkflow(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 5, 0, 0, time.UTC)
	scheduledFor := now.Add(-5 * time.Minute)
	runtime := &schedulerWindowRuntimeFake{schedulerRuntimeFake: schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "execution-1", Status: "succeeded"}}}}}
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, &schedulerRepositoryFake{}, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
	definition := recordmodel.Record{ID: "activation-job", Data: map[string]any{"target_type": "workflow", "target_key": "scheduled:activate"}}
	run := schedulerLeasedRun("run-1")
	run.Data["scheduled_for"] = scheduledFor.Format(time.RFC3339)
	result, err := service.ProcessClaimedRun(t.Context(), definition, run, 1, schedulerTestPrincipal("scheduler.command"), now)
	if err != nil || result.Processed != 1 || runtime.target != "scheduled:activate" || !runtime.window.Equal(scheduledFor) {
		t.Fatalf("result=%+v target=%q window=%s err=%v", result, runtime.target, runtime.window, err)
	}
}

func TestSchedulerFailureRetryAndManualRunDoNotAdvanceAutomaticCursor(t *testing.T) {
	now := time.Date(2026, 8, 12, 8, 5, 0, 0, time.UTC)
	dueAt := now.Add(-time.Minute)
	definition := recordmodel.Record{ID: "activation-job", Data: map[string]any{
		"key": "activation-job", "status": "enabled", "schedule_type": "interval", "interval_seconds": 60,
		"target_type": "workflow", "target_key": "scheduled:activate", "next_run_at": dueAt.Format(time.RFC3339),
	}}
	cursor := recordmodel.Record{ID: definition.ID, UpdatedAt: "cursor-v1", Data: map[string]any{"scheduler_definition_key": definition.ID, "next_run_at": dueAt.Format(time.RFC3339)}}
	repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
		if object.Key == "scheduler_cursor" && id == definition.ID {
			return cursor, true, nil
		}
		return recordmodel.Record{}, false, nil
	}}
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{}, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
	service.UseDefinitionSource(schedulerDefinitionSourceFunc{get: func(_ context.Context, id string) (recordmodel.Record, bool, error) {
		return definition, id == definition.ID, nil
	}})
	run := schedulerLeasedRun(RunIDForDefinition(definition, "scheduler", dueAt))
	run.Data["scheduler_definition_key"] = definition.ID
	run.Data["scheduled_for"] = dueAt.Format(time.RFC3339)

	commits, err := service.prepareDefinitionCursorCommits(t.Context(), principalmodel.InstallationWorkspaceID, run, "retrying", now)
	if err != nil || len(commits) != 0 {
		t.Fatalf("retry cursor commits=%+v err=%v", commits, err)
	}
	manual := run
	manual.Data = map[string]any{}
	for key, value := range run.Data {
		manual.Data[key] = value
	}
	manual.Data["triggered_by"] = "manual_run"
	commits, err = service.prepareDefinitionCursorCommits(t.Context(), principalmodel.InstallationWorkspaceID, manual, "succeeded", now)
	if err != nil || len(commits) != 0 {
		t.Fatalf("manual cursor commits=%+v err=%v", commits, err)
	}
	commits, err = service.prepareDefinitionCursorCommits(t.Context(), principalmodel.InstallationWorkspaceID, run, "succeeded", now)
	if err != nil || len(commits) != 2 || commits[0].Operation != "update" {
		t.Fatalf("success cursor commits=%+v err=%v", commits, err)
	}
	if got := commits[0].Record.Data["next_run_at"]; got != now.Add(time.Minute).Format(time.RFC3339) {
		t.Fatalf("advanced next_run_at=%v", got)
	}
}
