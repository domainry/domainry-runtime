package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type schedulerSchemaSequence struct {
	snapshots []metadatamodel.MetadataSchemaSnapshot
	calls     int
}

type schedulerCancelDeadlineRuntime struct{ cancel context.CancelFunc }

func (r schedulerCancelDeadlineRuntime) ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	r.cancel()
	return workflowmodel.WorkflowProcessResult{}, nil
}

type schedulerHeartbeatWaitingRuntime struct{}

func (schedulerHeartbeatWaitingRuntime) ProcessDueWorkflowExecutions(ctx context.Context, _ int, _ principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	<-ctx.Done()
	return workflowmodel.WorkflowProcessResult{}, nil
}

func (s *schedulerSchemaSequence) SchemaForPrincipal(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
	index := s.calls
	s.calls++
	if len(s.snapshots) == 0 {
		return metadatamodel.MetadataSchemaSnapshot{}
	}
	if index >= len(s.snapshots) {
		index = len(s.snapshots) - 1
	}
	return s.snapshots[index]
}

func schedulerSnapshot(keys ...string) metadatamodel.MetadataSchemaSnapshot {
	objects := make([]definitionmodel.ObjectSchema, 0, len(keys))
	for _, key := range keys {
		objects = append(objects, definitionmodel.ObjectSchema{Key: key})
	}
	return metadatamodel.MetadataSchemaSnapshot{Objects: objects}
}

func TestSchedulerFinalRunHelperConditions(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	if !schedulerExecutionsFailed([]workflowmodel.WorkflowExecution{{Status: "dead_letter"}}) {
		t.Fatal("dead-letter execution was not treated as failed")
	}
	if got := schedulerNextRetryAt(recordmodel.Record{Data: map[string]any{"retry_backoff": "", "retry_backoff_mode": "", "retry_delay_seconds": 10, "retry_max_delay_seconds": 100}}, 1, now); !got.Equal(now.Add(10 * time.Second)) {
		t.Fatalf("blank retry modes = %v", got)
	}
	if got := schedulerNextRetryAt(recordmodel.Record{Data: map[string]any{"retry_backoff": "fixed", "retry_delay_seconds": 10, "retry_max_delay_seconds": 100}}, 1, now); !got.Equal(now.Add(10 * time.Second)) {
		t.Fatalf("uncapped fixed retry = %v", got)
	}
	if got := schedulerNextRetryAt(recordmodel.Record{Data: map[string]any{"retry_backoff": "capped_exponential", "retry_delay_seconds": 10, "retry_max_delay_seconds": 100}}, 1, now); !got.Equal(now.Add(10 * time.Second)) {
		t.Fatalf("uncapped exponential retry = %v", got)
	}
	blankCode := &apperror.AppError{Kind: apperror.KindInternal, Err: errors.New("blank code")}
	if got := schedulerErrorCategory(blankCode); got != "runtime_error" {
		t.Fatalf("blank app error category = %q", got)
	}
}

func TestSchedulerFinalRunPersistenceConditions(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 30, 0, 0, time.UTC)
	wantErr := errors.New("injected run persistence failure")
	run := schedulerLeasedRun("run-1")

	service := NewSchedulerApplicationService(schedulerSchemaStub{}, nil, &schedulerRepositoryFake{}, nil)
	if err := service.finishRun(t.Context(), "workspace-a", run, nil, nil, nil, now); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
		t.Fatalf("finish object error = %v", err)
	}

	service = schedulerPersistenceService(&schedulerRepositoryFake{}, now)
	insertCalls := 0
	service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
		insertCalls++
		if insertCalls == 2 {
			return wantErr
		}
		return nil
	}
	if err := service.appendWorkflowEvents(t.Context(), run, []workflowmodel.WorkflowExecution{{ID: "execution-1", ActionType: "update"}}, now); !errors.Is(err, wantErr) {
		t.Fatalf("action event error = %v", err)
	}

	t.Run("dead-letter object and read failures", func(t *testing.T) {
		missing := NewSchedulerApplicationService(schedulerSchemaStub{snapshot: schedulerSnapshot("job_run")}, nil, &schedulerRepositoryFake{}, nil)
		if err := missing.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, run, nil, nil, "dead_letter", "failed", now, nil); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("dead-letter object error = %v", err)
		}

		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return recordmodel.Record{}, false, wantErr
		}}
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
		if err := service.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, run, nil, nil, "dead_letter", "failed", now, nil); !errors.Is(err, wantErr) {
			t.Fatalf("dead-letter read error = %v", err)
		}
	})

	t.Run("cursor object and blank definition", func(t *testing.T) {
		missing := NewSchedulerApplicationService(schedulerSchemaStub{}, nil, &schedulerRepositoryFake{}, nil)
		if _, err := missing.prepareDefinitionCursorCommits(t.Context(), "workspace-a", run, "failed", now); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("cursor object error = %v", err)
		}
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil)
		commits, err := service.prepareDefinitionCursorCommits(t.Context(), "workspace-a", recordmodel.Record{ID: "run", Data: map[string]any{"scheduler_definition_key": ""}}, "failed", now)
		if err != nil || len(commits) != 0 {
			t.Fatalf("blank cursor definition = %+v, %v", commits, err)
		}
	})
}

func TestSchedulerFinalPersistenceFailureConditions(t *testing.T) {
	now := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	wantErr := errors.New("injected persistence failure")
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "", "schedule_type": "interval", "interval_seconds": 60, "max_attempts": 3}}

	t.Run("missing schema objects", func(t *testing.T) {
		service := NewSchedulerApplicationService(schedulerSchemaStub{}, nil, &schedulerRepositoryFake{}, nil)
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		if _, _, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("claim object error = %v", err)
		}
		if err := service.heartbeatRun(t.Context(), "workspace-a", schedulerLeasedRun("run-1"), now); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("heartbeat object error = %v", err)
		}
		if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", recordmodel.Record{Data: map[string]any{}}, "succeeded", now); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("cursor object error = %v", err)
		}
		if err := service.appendRunEvent(t.Context(), "workspace-a", "run-1", "created", "message", now, nil); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("event object error = %v", err)
		}
		if err := service.deadLetterRun(t.Context(), "workspace-a", recordmodel.Record{ID: "run-1"}, definition, "failed", now); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("dead-letter object error = %v", err)
		}
	})

	t.Run("blank definition key and unlimited attempts", func(t *testing.T) {
		existing := recordmodel.Record{ID: RunIDForDefinition(definition, "scheduler", now), Data: map[string]any{"status": "queued", "attempt": 1, "max_attempts": 0, "fencing_token": 1}}
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return existing, true, nil
		}}
		service := schedulerPersistenceService(repository, now)
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		if _, claimed, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); err != nil || !claimed {
			t.Fatalf("unlimited attempt claim = claimed=%v err=%v", claimed, err)
		}
	})

	t.Run("leased record without prior expiry", func(t *testing.T) {
		existing := recordmodel.Record{ID: RunIDForDefinition(definition, "scheduler", now), Data: map[string]any{"status": "leased", "attempt": 1, "max_attempts": 3, "fencing_token": 1, "lease_expires_at": ""}}
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return existing, true, nil
		}}
		service := schedulerPersistenceService(repository, now)
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		if _, claimed, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); err != nil || !claimed {
			t.Fatalf("expired empty lease claim = claimed=%v err=%v", claimed, err)
		}
	})

	t.Run("insert failure followed by read failure", func(t *testing.T) {
		getCalls := 0
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			getCalls++
			if getCalls == 1 {
				return recordmodel.Record{}, false, nil
			}
			return recordmodel.Record{}, false, wantErr
		}}
		repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error {
			return wantErr
		}
		service := schedulerPersistenceService(repository, now)
		if _, _, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); !errors.Is(err, wantErr) {
			t.Fatalf("insert/read failure = %v", err)
		}
	})

	t.Run("dead-letter and cursor write failures", func(t *testing.T) {
		existing := recordmodel.Record{ID: RunIDForDefinition(definition, "scheduler", now), Data: map[string]any{"scheduler_definition_key": definition.ID, "status": "failed", "attempt": 3, "max_attempts": 3, "error_message": "failed"}}
		repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			if object.Key == "job_run" {
				return existing, true, nil
			}
			return recordmodel.Record{}, false, nil
		}}
		service := schedulerPersistenceService(repository, now)
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return wantErr
		}
		if _, _, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); !errors.Is(err, wantErr) {
			t.Fatalf("dead-letter insert error = %v", err)
		}

		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		service.updateRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return wantErr
		}
		if _, _, err := service.claimRun(t.Context(), "workspace-a", definition, "scheduler", now); !errors.Is(err, wantErr) {
			t.Fatalf("dead-letter update error = %v", err)
		}

		repository.get = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			if object.Key == "job_definition" {
				return definition, true, nil
			}
			return recordmodel.Record{}, false, nil
		}
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return wantErr
		}
		if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", recordmodel.Record{ID: "run-1", Data: map[string]any{"scheduler_definition_key": definition.ID}}, "succeeded", now); !errors.Is(err, wantErr) {
			t.Fatalf("cursor update error = %v", err)
		}
	})

	t.Run("cursor read and blank id", func(t *testing.T) {
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return recordmodel.Record{}, false, wantErr
		}}
		service := schedulerPersistenceService(repository, now)
		if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", recordmodel.Record{ID: "run", Data: map[string]any{"scheduler_definition_key": ""}}, "failed", now); err != nil {
			t.Fatalf("blank definition id = %v", err)
		}
		if err := service.advanceDefinitionCursor(t.Context(), "workspace-a", recordmodel.Record{ID: "run", Data: map[string]any{"scheduler_definition_key": definition.ID}}, "failed", now); !errors.Is(err, wantErr) {
			t.Fatalf("cursor read error = %v", err)
		}
	})
}

func TestSchedulerFinalOperationFailureConditions(t *testing.T) {
	now := time.Date(2026, 7, 20, 14, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	wantErr := errors.New("injected operation failure")
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "nightly", "target_type": "workflow", "target_key": "scheduled:*", "schedule_type": "interval", "interval_seconds": 60, "max_attempts": 3}}
	newRun := func(scope, key string) recordmodel.Record {
		return recordmodel.Record{ID: "run-1", UpdatedAt: "old", Data: map[string]any{"scheduler_definition_key": definition.ID, "status": "failed", "attempt": 1, "max_attempts": 3, "fencing_token": 1, "last_command_scope": scope, "last_command_key": key}}
	}

	t.Run("operation lookup errors", func(t *testing.T) {
		service := NewSchedulerApplicationService(schedulerSchemaStub{}, nil, &schedulerRepositoryFake{}, nil)
		if _, err := service.RunJob(t.Context(), definition.ID, "request", principal); apperror.CodeOf(err) != "backend.scheduler.definition_not_found" {
			t.Fatalf("definition object error = %v", err)
		}
		if _, err := service.RetryRun(t.Context(), "run-1", "request", principal); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("retry lookup error = %v", err)
		}

		schema := &schedulerSchemaSequence{snapshots: []metadatamodel.MetadataSchemaSnapshot{schedulerSnapshot("job_run"), schedulerSnapshot()}}
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return newRun("", ""), true, nil
		}}
		service = NewSchedulerApplicationService(schema, nil, repository, nil)
		if _, err := service.CancelRun(t.Context(), "run-1", "request", principal); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("run definition object error = %v", err)
		}

		service = NewSchedulerApplicationService(schedulerSchemaStub{}, nil, &schedulerRepositoryFake{}, nil)
		if _, err := service.ResolveDeadLetter(t.Context(), "dead-1", "note", "request", principal); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("resolve object error = %v", err)
		}
	})

	t.Run("manual processing and final lookup errors", func(t *testing.T) {
		newRepository := func() *schedulerRepositoryFake {
			definitionGets := 0
			jobRunGets := 0
			repository := &schedulerRepositoryFake{}
			repository.get = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
				if object.Key == "job_definition" {
					definitionGets++
					if definitionGets == 1 {
						return definition, true, nil
					}
				}
				if object.Key == "job_run" {
					jobRunGets++
					if jobRunGets > 1 && len(repository.committed) > 0 {
						return repository.committed[len(repository.committed)-1][0].Record, true, nil
					}
				}
				return recordmodel.Record{}, false, nil
			}
			return repository
		}
		repository := newRepository()
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{processErr: wantErr}, repository, nil, workerDependenciesAt(now))
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		if _, err := service.RunJob(t.Context(), definition.ID, "request", principal); !errors.Is(err, wantErr) {
			t.Fatalf("manual process error = %v", err)
		}

		full := schedulerTestSchema().snapshot
		snapshots := []metadatamodel.MetadataSchemaSnapshot{full, full, full, full, full, full, full, schedulerSnapshot()}
		schema := &schedulerSchemaSequence{snapshots: snapshots}
		repository = newRepository()
		service = NewSchedulerApplicationServiceWithWorker(schema, &schedulerRuntimeFake{}, repository, nil, workerDependenciesAt(now))
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		if _, err := service.RunJob(t.Context(), definition.ID, "request", principal); err != nil {
			t.Fatalf("metadata definition should not depend on a disappearing record object: %v schema calls=%d", err, schema.calls)
		}
	})

	t.Run("retry idempotency and schema stages", func(t *testing.T) {
		wrongKeyRun := newRun("scheduler.retry", "wrong-key")
		repository := schedulerRunLookupRepository(t, wrongKeyRun, definition)
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{processErr: wantErr}, repository, nil, workerDependenciesAt(now))
		if _, err := service.RetryRun(t.Context(), wrongKeyRun.ID, "request", principal); !errors.Is(err, wantErr) {
			t.Fatalf("retry process error = %v", err)
		}

		full := schedulerTestSchema().snapshot
		for _, test := range []struct {
			name      string
			snapshots []metadatamodel.MetadataSchemaSnapshot
		}{
			{name: "run object", snapshots: []metadatamodel.MetadataSchemaSnapshot{full, full, schedulerSnapshot()}},
			{name: "event object", snapshots: []metadatamodel.MetadataSchemaSnapshot{full, full, full, schedulerSnapshot()}},
		} {
			t.Run(test.name, func(t *testing.T) {
				schema := &schedulerSchemaSequence{snapshots: test.snapshots}
				service := NewSchedulerApplicationServiceWithWorker(schema, &schedulerRuntimeFake{}, schedulerRunLookupRepository(t, newRun("scheduler.retry", "wrong-key"), definition), nil, workerDependenciesAt(now))
				if _, err := service.RetryRun(t.Context(), "run-1", "request", principal); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
					t.Fatalf("%s error = %v schema calls=%d", test.name, err, schema.calls)
				}
			})
		}
	})

	t.Run("cancel idempotency and schema stage", func(t *testing.T) {
		full := schedulerTestSchema().snapshot
		schema := &schedulerSchemaSequence{snapshots: []metadatamodel.MetadataSchemaSnapshot{full, full, schedulerSnapshot()}}
		service := NewSchedulerApplicationServiceWithWorker(schema, nil, schedulerRunLookupRepository(t, newRun("scheduler.cancel", "wrong-key"), definition), nil, workerDependenciesAt(now))
		if _, err := service.CancelRun(t.Context(), "run-1", "request", principal); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("cancel run object error = %v schema calls=%d", err, schema.calls)
		}
	})

	t.Run("resolve event schema stage", func(t *testing.T) {
		deadLetter := recordmodel.Record{ID: "dead-1", UpdatedAt: "old", Data: map[string]any{"status": "open", "job_run_id": "run-1"}}
		schema := &schedulerSchemaSequence{snapshots: []metadatamodel.MetadataSchemaSnapshot{schedulerSnapshot("job_dead_letter"), schedulerSnapshot()}}
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return deadLetter, true, nil
		}}
		service := NewSchedulerApplicationServiceWithWorker(schema, nil, repository, nil, workerDependenciesAt(now))
		if _, err := service.ResolveDeadLetter(t.Context(), deadLetter.ID, "note", "request", principal); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("resolve event object error = %v", err)
		}
	})

	if err := schedulerOperationAllowed(schedulerTestPrincipal("other", "workspace.admin")); err != nil {
		t.Fatalf("later permission should allow operation: %v", err)
	}
}

func workerDependenciesAt(now time.Time) workerplatform.Dependencies {
	return workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")}
}

func TestSchedulerFinalRuntimeConditions(t *testing.T) {
	now := time.Date(2026, 7, 20, 15, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	wantErr := errors.New("injected runtime failure")
	dueDefinition := func(id string) recordmodel.Record {
		return recordmodel.Record{ID: id, Data: map[string]any{"key": id, "status": "enabled", "target_type": "workflow", "target_key": "scheduled:*", "schedule_type": "interval", "interval_seconds": 60, "next_run_at": now.Add(-time.Minute).Format(time.RFC3339), "max_attempts": 3}}
	}

	t.Run("available worker starts without legacy definitions", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{}, &schedulerRepositoryFake{}, nil, workerDependenciesAt(now))
		done := service.StartWorker(ctx, WorkerConfig{Enabled: true, PollInterval: time.Hour}, false)
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("available worker did not stop")
		}
	})

	t.Run("workflow processing cancellation stops tick", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		service := NewSchedulerApplicationService(schedulerSchemaStub{}, schedulerCancelDeadlineRuntime{cancel: cancel}, &schedulerRepositoryFake{}, nil)
		service.processWorkerTick(ctx, 1)
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("tick context = %v", ctx.Err())
		}
	})

	t.Run("dynamic definition object disappears", func(t *testing.T) {
		full := schedulerTestSchema().snapshot
		schema := &schedulerSchemaSequence{snapshots: []metadatamodel.MetadataSchemaSnapshot{full, schedulerSnapshot()}}
		service := NewSchedulerApplicationService(schema, &schedulerRuntimeFake{}, &schedulerRepositoryFake{}, nil)
		if _, err := service.ProcessDueJobs(t.Context(), 1, principal, "scheduler"); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("definition object error = %v", err)
		}
	})

	t.Run("definition list and default errors", func(t *testing.T) {
		repository := &schedulerRepositoryFake{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			return recordmodel.RecordPageResult{}, wantErr
		}}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{}, repository, nil, workerDependenciesAt(now))
		if _, err := service.ProcessDueJobs(t.Context(), 1, principal, "scheduler"); !errors.Is(err, wantErr) {
			t.Fatalf("list error = %v", err)
		}

		full := schedulerTestSchema().snapshot
		schema := &schedulerSchemaSequence{snapshots: []metadatamodel.MetadataSchemaSnapshot{full, full, schedulerSnapshot()}}
		service = NewSchedulerApplicationServiceWithWorker(schema, &schedulerRuntimeFake{}, &schedulerRepositoryFake{}, nil, workerDependenciesAt(now))
		if result, err := service.ProcessDueJobs(t.Context(), 1, principal, "scheduler"); err != nil || result.Processed != 0 {
			t.Fatalf("empty published definition set should be idle: %+v, %v", result, err)
		}
	})

	t.Run("inactive and future defaults", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			status string
			next   string
		}{
			{name: "disabled", status: "disabled", next: now.Add(-time.Minute).Format(time.RFC3339)},
			{name: "future", status: "enabled", next: now.Add(time.Minute).Format(time.RFC3339)},
		} {
			t.Run(test.name, func(t *testing.T) {
				repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
					if object.Key == "job_definition" {
						return recordmodel.Record{ID: "scheduler_daily_workflow_scan", Data: map[string]any{"status": test.status, "next_run_at": test.next, "schedule_type": "interval", "interval_seconds": 60}}, true, nil
					}
					return recordmodel.Record{}, false, nil
				}}
				service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{}, repository, nil, workerDependenciesAt(now))
				result, err := service.ProcessDueJobs(t.Context(), 1, principal, "scheduler")
				if err != nil || result.Processed != 0 {
					t.Fatalf("default result = %+v, %v", result, err)
				}
			})
		}
	})

	t.Run("definition loop boundaries", func(t *testing.T) {
		tests := []struct {
			name        string
			definitions []recordmodel.Record
			get         func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
			runtime     *schedulerRuntimeFake
			limit       int
			wantErr     error
			wantCalls   int
		}{
			{name: "claim error", definitions: []recordmodel.Record{dueDefinition("definition-1")}, get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
				return recordmodel.Record{}, false, wantErr
			}, runtime: &schedulerRuntimeFake{}, limit: 1, wantErr: wantErr},
			{name: "unclaimed terminal", definitions: []recordmodel.Record{dueDefinition("definition-1")}, get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
				if object.Key == "job_run" {
					return recordmodel.Record{ID: id, Data: map[string]any{"status": "succeeded"}}, true, nil
				}
				return recordmodel.Record{}, false, nil
			}, runtime: &schedulerRuntimeFake{}, limit: 1},
			{name: "process error", definitions: []recordmodel.Record{dueDefinition("definition-1")}, runtime: &schedulerRuntimeFake{processErr: wantErr}, limit: 1, wantErr: wantErr, wantCalls: 1},
			{name: "limit break", definitions: []recordmodel.Record{dueDefinition("definition-1"), dueDefinition("definition-2")}, runtime: &schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "execution-1", Status: "succeeded"}}}}, limit: 1, wantCalls: 1},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				repository := &schedulerRepositoryFake{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
					return recordmodel.RecordPageResult{Items: test.definitions}, nil
				}, get: test.get}
				service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), test.runtime, repository, nil, workerDependenciesAt(now))
				service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
					return nil
				}
				result, err := service.ProcessDueJobs(t.Context(), test.limit, principal, "scheduler")
				if !errors.Is(err, test.wantErr) || test.runtime.processCalls != test.wantCalls {
					t.Fatalf("result=%+v err=%v calls=%d", result, err, test.runtime.processCalls)
				}
			})
		}
	})

	definitions := []recordmodel.Record{
		{Data: map[string]any{"next_run_at": now.Add(-time.Minute).Format(time.RFC3339)}},
		{Data: map[string]any{"next_run_at": now.Add(-5 * time.Minute).Format(time.RFC3339)}},
	}
	if got := schedulerDefinitionQueueLag(definitions, now); got != 5*time.Minute {
		t.Fatalf("ordered queue lag = %v", got)
	}

	service := schedulerPersistenceService(&schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, wantErr
	}}, now)
	if err := service.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, recordmodel.Record{ID: "run", Data: map[string]any{"scheduler_definition_key": "definition-1"}}, nil, nil, "succeeded", "done", now, nil); !errors.Is(err, wantErr) {
		t.Fatalf("finish cursor error = %v", err)
	}
}

func TestSchedulerFinalClaimedRunFailureWindows(t *testing.T) {
	now := time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	wantErr := errors.New("injected claimed-run failure")
	workflowDefinition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"target_type": "workflow"}}
	reportDefinition := recordmodel.Record{ID: "definition-2", Data: map[string]any{"target_type": "report_export", "target_key": "sales"}}
	fullReportSchema := func() schedulerSchemaStub {
		objects := append([]definitionmodel.ObjectSchema{}, schedulerTestSchema().snapshot.Objects...)
		for _, key := range []string{"report_definition", "report_query_run", "report_export_audit", "download_task"} {
			objects = append(objects, definitionmodel.ObjectSchema{Key: key})
		}
		return schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: objects}}
	}

	t.Run("terminal update failures", func(t *testing.T) {
		tests := []struct {
			name       string
			definition recordmodel.Record
			schema     schedulerSchemaStub
			runtime    SchedulerOperationRuntime
			insert     func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error
		}{
			{name: "workflow success", definition: workflowDefinition, schema: schedulerTestSchema(), runtime: &schedulerRuntimeFake{}},
			{name: "report error", definition: reportDefinition, schema: schedulerTestSchema(), runtime: &schedulerRuntimeFake{}},
			{name: "report success", definition: reportDefinition, schema: fullReportSchema(), runtime: &schedulerRuntimeFake{}, insert: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
				return nil
			}},
			{name: "unsupported", definition: recordmodel.Record{ID: "definition-3", Data: map[string]any{"target_type": "notification"}}, schema: schedulerTestSchema(), runtime: &schedulerRuntimeFake{}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				repository := &schedulerRepositoryFake{commit: func(context.Context, string, []transactionmodel.RecordMutationCommit) error { return wantErr }}
				service := NewSchedulerApplicationServiceWithWorker(test.schema, test.runtime, repository, nil, workerDependenciesAt(now))
				if test.insert != nil {
					service.insertRecord = test.insert
				}
				if _, err := service.ProcessClaimedRun(t.Context(), test.definition, schedulerLeasedRun("run-1"), 1, principal, now); !errors.Is(err, wantErr) {
					t.Fatalf("terminal update error = %v", err)
				}
			})
		}
	})

	t.Run("workflow heartbeat failure", func(t *testing.T) {
		run := schedulerLeasedRun("run-heartbeat")
		repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			if object.Key == "job_run" {
				return run, true, nil
			}
			return recordmodel.Record{}, false, nil
		}, update: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
			return false, wantErr
		}}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), schedulerHeartbeatWaitingRuntime{}, repository, nil, workerDependenciesAt(now))
		service.ConfigureWorker(WorkerConfig{LeaseTTL: 3 * time.Millisecond})
		if _, err := service.ProcessClaimedRun(t.Context(), workflowDefinition, run, 1, principal, now); !errors.Is(err, wantErr) {
			t.Fatalf("workflow heartbeat error = %v", err)
		}
	})

	t.Run("report heartbeat failure", func(t *testing.T) {
		run := schedulerLeasedRun("report-heartbeat")
		repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			if object.Key == "job_run" {
				return run, true, nil
			}
			return recordmodel.Record{}, false, nil
		}, update: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
			return false, wantErr
		}}
		service := NewSchedulerApplicationServiceWithWorker(fullReportSchema(), &schedulerRuntimeFake{}, repository, nil, workerDependenciesAt(now))
		service.ConfigureWorker(WorkerConfig{LeaseTTL: 3 * time.Millisecond})
		service.insertRecord = func(ctx context.Context, _ string, _ definitionmodel.ObjectSchema, _ recordmodel.Record, _ string) error {
			<-ctx.Done()
			return nil
		}
		if _, err := service.ProcessClaimedRun(t.Context(), reportDefinition, run, 1, principal, now); !errors.Is(err, wantErr) {
			t.Fatalf("report heartbeat error = %v", err)
		}
	})
}
