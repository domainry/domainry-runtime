package scheduler

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/requestcontext"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type schedulerRepositoryFake struct {
	mu            sync.Mutex
	get           func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
	list          func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
	update        func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error)
	commit        func(context.Context, string, []transactionmodel.RecordMutationCommit) error
	workspaceList func(context.Context, definitionmodel.ObjectSchema, time.Time) ([]string, error)
	getCalls      int
	committed     [][]transactionmodel.RecordMutationCommit
	workspaces    []string
}

func (f *schedulerRepositoryFake) ListDueRecordTimerWorkspaces(ctx context.Context, object definitionmodel.ObjectSchema, now time.Time) ([]string, error) {
	if f.workspaceList != nil {
		return f.workspaceList(ctx, object, now)
	}
	return nil, nil
}

func (f *schedulerRepositoryFake) GetRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
	f.mu.Lock()
	f.getCalls++
	f.workspaces = append(f.workspaces, workspaceID)
	f.mu.Unlock()
	if f.get != nil {
		return f.get(ctx, workspaceID, object, id)
	}
	return recordmodel.Record{}, false, nil
}

func (f *schedulerRepositoryFake) ListRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if f.list != nil {
		return f.list(ctx, workspaceID, object, query)
	}
	return recordmodel.RecordPageResult{}, nil
}

func (f *schedulerRepositoryFake) UpdateRecordWhere(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, conditions map[string]any) (bool, error) {
	if f.update != nil {
		return f.update(ctx, workspaceID, object, record, conditions)
	}
	return true, nil
}

func (f *schedulerRepositoryFake) CommitRecordMutationBatch(ctx context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) error {
	f.mu.Lock()
	f.workspaces = append(f.workspaces, workspaceID)
	f.committed = append(f.committed, commits)
	f.mu.Unlock()
	if f.commit != nil {
		return f.commit(ctx, workspaceID, commits)
	}
	return nil
}

func (f *schedulerRepositoryFake) ListSchedulerDefinitions(ctx context.Context) ([]recordmodel.Record, error) {
	page, err := f.ListRecords(ctx, principalmodel.InstallationWorkspaceID, definitionmodel.ObjectSchema{Key: "job_definition"}, recordmodel.RecordListQuery{Page: 1, PageSize: 500})
	return page.Items, err
}

func (f *schedulerRepositoryFake) GetSchedulerDefinition(ctx context.Context, key string) (recordmodel.Record, bool, error) {
	return f.GetRecord(ctx, "workspace-a", definitionmodel.ObjectSchema{Key: "job_definition"}, key)
}

func (*schedulerRepositoryFake) ListSchedulerDefinitionVersions(context.Context, string) ([]SchedulerDefinitionVersion, error) {
	return []SchedulerDefinitionVersion{}, nil
}

type schedulerAuditSpy struct {
	requests []auditcontract.AuditAppendRequest
}

func (s *schedulerAuditSpy) AppendAuditTelemetry(_ context.Context, request auditcontract.AuditAppendRequest) {
	s.requests = append(s.requests, request)
}

type schedulerFixedClock struct{ now time.Time }

func (c schedulerFixedClock) Now() time.Time { return c.now }

func schedulerTestPrincipal(permissions ...string) principalmodel.Principal {
	// Historical scheduler tests used workspace.admin as a shorthand for a
	// fully privileged actor. Operational use cases now require an explicit
	// scheduler command permission in addition to any Tenant Admin grant.
	for _, permission := range permissions {
		if permission == "workspace.admin" {
			permissions = append(permissions, "scheduler.command")
			break
		}
	}
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true,
		UserID:      "operator-1",
		WorkspaceID: "workspace-a"},
	}, accessfixture.Bundle{
		Permissions: permissions,
		RecordScope: "all_records",
	},
	)
}

func schedulerTestSchema() schedulerSchemaStub {
	objects := []definitionmodel.ObjectSchema{
		{Key: "scheduler_cursor"},
		{Key: "job_run"},
		{Key: "job_run_event"},
		{Key: "job_dead_letter"},
	}
	return schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: objects}}
}

func assertSchedulerError(t *testing.T, err error, kind apperror.ErrorKind, code string) {
	t.Helper()
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("error = %#v, want kind=%q code=%q", err, kind, code)
	}
}

func TestSchedulerWorkerConfigNormalizationAndAccessors(t *testing.T) {
	defaults := DefaultWorkerConfig()
	if !defaults.Enabled || defaults.PollInterval != 500*time.Millisecond || defaults.BatchSize != 25 || defaults.LeaseTTL != 5*time.Minute || defaults.MaxCatchupWindows != 1 {
		t.Fatalf("defaults = %+v", defaults)
	}
	service := NewSchedulerApplicationService(nil, nil, nil, nil)
	service.ConfigureWorker(WorkerConfig{PollInterval: -1, BatchSize: 501, LeaseTTL: -1, MaxCatchupWindows: -1})
	got := service.WorkerConfig()
	want := WorkerConfig{PollInterval: 500 * time.Millisecond, BatchSize: 500, LeaseTTL: 5 * time.Minute, MaxCatchupWindows: 1}
	if !reflect.DeepEqual(got, want) || service.leaseTTL() != want.LeaseTTL || service.maxCatchupWindows() != want.MaxCatchupWindows {
		t.Fatalf("normalized config = %+v, want %+v", got, want)
	}
	got = NormalizeWorkerConfig(WorkerConfig{PollInterval: time.Second, BatchSize: 7, LeaseTTL: time.Minute, MaxCatchupWindows: 2})
	if got.PollInterval != time.Second || got.BatchSize != 7 || got.LeaseTTL != time.Minute || got.MaxCatchupWindows != 2 {
		t.Fatalf("valid config changed: %+v", got)
	}
	if got = NormalizeWorkerConfig(WorkerConfig{BatchSize: -1}); got.BatchSize != 25 {
		t.Fatalf("negative batch size = %d", got.BatchSize)
	}
}

func TestSchedulerPreviewDefinitionBoundaryFailures(t *testing.T) {
	admin := schedulerTestPrincipal("workspace.admin")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	service := NewSchedulerApplicationService(schedulerTestSchema(), nil, nil, nil)
	if _, err := service.PreviewDefinition(cancelled, nil, admin); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preview error = %v", err)
	}

	if _, err := service.PreviewDefinition(t.Context(), nil, schedulerTestPrincipal()); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("permission error = %v", err)
	}
	missing := NewSchedulerApplicationService(schedulerSchemaStub{}, nil, nil, nil)
	if _, err := missing.PreviewDefinition(t.Context(), nil, admin); apperror.CodeOf(err) != "backend.scheduler.target_type_required" {
		t.Fatalf("metadata-independent preview error = %v", err)
	}
	if _, err := service.PreviewDefinition(t.Context(), map[string]any{}, admin); err == nil {
		t.Fatal("invalid scheduler definition accepted")
	}
}

func TestSchedulerSimulateJobCommitsRunAndEventInWorkspace(t *testing.T) {
	now := time.Date(2026, 7, 19, 9, 30, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "nightly", "target_type": "workflow", "target_key": "workflow-1", "max_attempts": 5}}
	repository := &schedulerRepositoryFake{get: func(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
		if workspaceID != "workspace-a" || object.Key != "job_definition" || id != definition.ID {
			t.Fatalf("unexpected lookup: workspace=%q object=%q id=%q", workspaceID, object.Key, id)
		}
		return definition, true, nil
	}}
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
	result, err := service.SimulateJob(t.Context(), definition.ID, schedulerTestPrincipal("scheduler.command"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "simulated" || result.Run.Data["status"] != "cancelled" || result.Run.Data["max_attempts"] != 5 {
		t.Fatalf("simulation result = %+v", result)
	}
	if len(repository.committed) != 1 || len(repository.committed[0]) != 2 || repository.committed[0][0].Object.Key != "job_run" || repository.committed[0][1].Object.Key != "job_run_event" {
		t.Fatalf("commits = %+v", repository.committed)
	}
	if repository.workspaces[len(repository.workspaces)-1] != "workspace-a" {
		t.Fatalf("commit workspaces = %+v", repository.workspaces)
	}
}

func TestTenantAdminSchedulerSimulationIsDryAndRequiresDefinitionWrite(t *testing.T) {
	now := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "customer_sync", Data: map[string]any{
		"key": "customer_sync", "status": "enabled", "trigger_type": "scheduled",
		"schedule_type": "cron", "schedule_expression": "0 8 * * 1-5", "timezone": "UTC",
		"target_type": "workflow", "target_key": "scheduled:*",
		"max_attempts": 3, "timeout_seconds": 300, "missed_window_policy": "skip", "max_catchup_windows": 1,
	}}
	repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
		return definition, id == definition.ID, nil
	}}
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})

	if _, err := service.SimulateTenantAdminDefinition(t.Context(), definition.ID, schedulerTestPrincipal("scheduler.definition.read")); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("read-only simulation error = %v", err)
	}
	result, err := service.SimulateTenantAdminDefinition(t.Context(), definition.ID, schedulerTestPrincipal("scheduler.definition.write"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "simulated" || result.Run.Data["status"] != "preview" || result.Run.Data["target_key"] != "scheduled:*" {
		t.Fatalf("dry simulation result = %+v", result)
	}
	if len(repository.committed) != 0 {
		t.Fatalf("Tenant Admin dry simulation wrote Runtime records: %+v", repository.committed)
	}
}

func TestSchedulerInspectDeadLetterBoundaries(t *testing.T) {
	admin := schedulerTestPrincipal("job_run.update")
	service := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil)
	if _, err := service.InspectDeadLetter(t.Context(), "dead-1", schedulerTestPrincipal()); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("permission error = %v", err)
	}
	partial := schedulerTestSchema()
	partial.snapshot.Objects = []definitionmodel.ObjectSchema{{Key: "job_definition"}}
	service.schema = partial
	if _, err := service.InspectDeadLetter(t.Context(), "dead-1", admin); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
		t.Fatalf("missing dead-letter object error = %v", err)
	}
	service.schema = schedulerTestSchema()
	if _, err := service.InspectDeadLetter(t.Context(), "dead-1", admin); apperror.CodeOf(err) != "backend.scheduler.dead_letter_not_found" {
		t.Fatalf("not found error = %v", err)
	}
	wantErr := errors.New("store unavailable")
	service.repository = &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, wantErr
	}}
	if _, err := service.InspectDeadLetter(t.Context(), "dead-1", admin); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("store error = %v", err)
	}
	deadLetter := recordmodel.Record{ID: "dead-1", Data: map[string]any{"status": "open"}}
	service.repository = &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return deadLetter, true, nil
	}}
	got, err := service.InspectDeadLetter(t.Context(), " dead-1 ", admin)
	if err != nil || got.ID != deadLetter.ID {
		t.Fatalf("inspect = %+v, %v", got, err)
	}
}

func TestSchedulerRunRetryAndCancelCommandBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 19, 11, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "nightly", "target_type": "workflow", "target_key": "scheduled:*", "max_attempts": 3}}

	t.Run("run requires key and permission", func(t *testing.T) {
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil)
		if _, err := service.RunJob(t.Context(), definition.ID, " ", principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
			t.Fatalf("missing key error = %v", err)
		}
		if _, err := service.RunJob(t.Context(), definition.ID, "request-1", schedulerTestPrincipal()); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
			t.Fatalf("permission error = %v", err)
		}
	})

	t.Run("run replays existing manual claim", func(t *testing.T) {
		var existing recordmodel.Record
		repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
			switch object.Key {
			case "job_definition":
				return definition, true, nil
			case "job_run":
				existing = recordmodel.Record{ID: id, Data: map[string]any{"status": "succeeded"}}
				return existing, true, nil
			default:
				t.Fatalf("unexpected object %q", object.Key)
				return recordmodel.Record{}, false, nil
			}
		}}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
		result, err := service.RunJob(t.Context(), definition.ID, "request-1", principal)
		if err != nil || result.Status != "replayed" || result.Run.ID != existing.ID {
			t.Fatalf("run replay = %+v, %v", result, err)
		}
	})

	run := recordmodel.Record{ID: "run-1", UpdatedAt: "old", Data: map[string]any{"scheduler_definition_key": definition.ID, "status": "failed", "fencing_token": 2}}
	t.Run("retry replay", func(t *testing.T) {
		commandKey := schedulerCommandIdempotencyKey("retry", run.ID, "request-1")
		replayed := recordmodel.Record{ID: run.ID, Data: map[string]any{"scheduler_definition_key": definition.ID, "status": "failed", "last_command_scope": "scheduler.retry", "last_command_key": commandKey}}
		repository := schedulerRunLookupRepository(t, replayed, definition)
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
		result, err := service.RetryRun(t.Context(), run.ID, "request-1", principal)
		if err != nil || result.Status != "replayed" || len(repository.committed) != 0 {
			t.Fatalf("retry replay = %+v, %v", result, err)
		}
	})

	t.Run("cancel commits state and audit", func(t *testing.T) {
		repository := schedulerRunLookupRepository(t, run, definition)
		audit := &schedulerAuditSpy{}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, audit, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
		ctx := requestcontext.WithCorrelationID(t.Context(), "correlation-cancel-1")
		result, err := service.CancelRun(ctx, run.ID, "request-2", principal)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "cancelled" || result.Run.Data["status"] != "cancelled" || result.Run.Data["fencing_token"] != 3 {
			t.Fatalf("cancel result = %+v", result)
		}
		if len(repository.committed) != 1 || len(repository.committed[0]) != 2 || repository.committed[0][0].Conditions["fencing_token"] != 2 {
			t.Fatalf("cancel commits = %+v", repository.committed)
		}
		if len(audit.requests) != 1 || audit.requests[0].Event != "scheduler_run_cancel_requested" {
			t.Fatalf("cancel audit = %+v", audit.requests)
		}
		entry := audit.requests[0]
		if entry.Principal.UserID != principal.UserID || entry.RecordID != run.ID ||
			entry.Before["status"] != "failed" || entry.After["status"] != "cancelled" ||
			entry.Metadata["reason"] == "" || entry.Metadata["result"] != "succeeded" ||
			entry.Metadata["correlation_id"] != "correlation-cancel-1" {
			t.Fatalf("cancel audit evidence = %+v", entry)
		}
	})

	t.Run("cancel replay and commit failure", func(t *testing.T) {
		commandKey := schedulerCommandIdempotencyKey("cancel", run.ID, "request-3")
		replayed := recordmodel.Record{ID: run.ID, Data: map[string]any{"scheduler_definition_key": definition.ID, "status": "leased", "last_command_scope": "scheduler.cancel", "last_command_key": commandKey}}
		repository := schedulerRunLookupRepository(t, replayed, definition)
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
		result, err := service.CancelRun(t.Context(), run.ID, "request-3", principal)
		if err != nil || result.Status != "replayed" {
			t.Fatalf("cancel replay = %+v, %v", result, err)
		}

		wantErr := errors.New("commit failed")
		repository = schedulerRunLookupRepository(t, recordmodel.Record{ID: run.ID, UpdatedAt: "old", Data: map[string]any{"scheduler_definition_key": definition.ID, "status": "queued"}}, definition)
		repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error { return wantErr }
		service.repository = repository
		service.definitions = repository
		if _, err := service.CancelRun(t.Context(), run.ID, "request-4", principal); !errors.Is(err, wantErr) {
			t.Fatalf("cancel commit error = %v", err)
		}
	})

	t.Run("missing run and definition", func(t *testing.T) {
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil)
		if _, err := service.CancelRun(t.Context(), "missing", "request", principal); apperror.CodeOf(err) != "backend.scheduler.run_not_found" {
			t.Fatalf("missing run error = %v", err)
		}
		repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			if object.Key == "job_run" {
				return run, true, nil
			}
			return recordmodel.Record{}, false, nil
		}}
		service.repository = repository
		if _, err := service.CancelRun(t.Context(), run.ID, "request", principal); apperror.CodeOf(err) != "backend.scheduler.definition_not_found" {
			t.Fatalf("missing definition error = %v", err)
		}
	})
}

func TestSchedulerRunAndRetryHappyPathAndConflict(t *testing.T) {
	now := time.Date(2026, 7, 20, 3, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	definition := recordmodel.Record{ID: "definition-1", UpdatedAt: "definition-old", Data: map[string]any{"key": "nightly", "target_type": "workflow", "target_key": "scheduled:*", "schedule_type": "interval", "interval_seconds": 60, "max_attempts": 3}}

	t.Run("manual run completes", func(t *testing.T) {
		runtime := &schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "execution-1", Status: "succeeded"}}}}
		repository := &schedulerRepositoryFake{}
		repository.get = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			switch object.Key {
			case "job_definition":
				return definition, true, nil
			case "job_run":
				if len(repository.committed) == 0 {
					return recordmodel.Record{}, false, nil
				}
				return repository.committed[0][0].Record, true, nil
			default:
				return recordmodel.Record{}, false, nil
			}
		}
		audit := &schedulerAuditSpy{}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, audit, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		result, err := service.RunJob(t.Context(), definition.ID, "request-1", principal)
		if err != nil || result.Status != "completed" || result.Run.Data["status"] != "succeeded" || runtime.processCalls != 1 {
			t.Fatalf("manual run = %+v, %v calls=%d", result, err, runtime.processCalls)
		}
		if len(audit.requests) != 1 || audit.requests[0].Event != "scheduler_job_manual_run" {
			t.Fatalf("manual run audit = %+v", audit.requests)
		}
	})

	t.Run("retry completes", func(t *testing.T) {
		run := recordmodel.Record{ID: "run-1", UpdatedAt: "run-old", Data: map[string]any{"scheduler_definition_key": definition.ID, "status": "failed", "attempt": 1, "max_attempts": 3, "fencing_token": 2}}
		runtime := &schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "execution-2", Status: "succeeded"}}}}
		repository := &schedulerRepositoryFake{}
		jobRunGets := 0
		repository.get = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			switch object.Key {
			case "job_run":
				jobRunGets++
				if jobRunGets == 1 {
					return run, true, nil
				}
				for index := len(repository.committed) - 1; index >= 0; index-- {
					if len(repository.committed[index]) > 0 && repository.committed[index][0].Object.Key == "job_run" {
						return repository.committed[index][0].Record, true, nil
					}
				}
				return run, true, nil
			case "job_definition":
				return definition, true, nil
			default:
				return recordmodel.Record{}, false, nil
			}
		}
		audit := &schedulerAuditSpy{}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, audit, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		result, err := service.RetryRun(t.Context(), run.ID, "request-2", principal)
		if err != nil || result.Status != "retried" || result.Run.Data["status"] != "succeeded" || runtime.processCalls != 1 {
			t.Fatalf("retry = %+v, %v calls=%d commits=%+v", result, err, runtime.processCalls, repository.committed)
		}
		if len(audit.requests) != 1 || audit.requests[0].Event != "scheduler_run_retry_requested" {
			t.Fatalf("retry audit = %+v", audit.requests)
		}
	})

	t.Run("retry optimistic conflict", func(t *testing.T) {
		run := recordmodel.Record{ID: "run-conflict", UpdatedAt: "old", Data: map[string]any{"scheduler_definition_key": definition.ID, "status": "failed"}}
		repository := schedulerRunLookupRepository(t, run, definition)
		repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error {
			return mutation.MutationConflict("job_run", run.ID, mutation.MutationConflictOptimistic, nil)
		}
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
		if _, err := service.RetryRun(t.Context(), run.ID, "request-3", principal); apperror.CodeOf(err) != "backend.scheduler.retry_conflict" {
			t.Fatalf("retry conflict = %v", err)
		}
	})
}

func TestSchedulerRescheduleDefinitionUsesOwnerCursorCommand(t *testing.T) {
	now := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
	nextRunAt := now.Add(-2 * time.Second)
	principal := schedulerTestPrincipal("scheduler.command")
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "nightly", "target_type": "workflow", "target_key": "scheduled:*", "schedule_type": "interval", "interval_seconds": 60}}
	cursor := recordmodel.Record{ID: definition.ID, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), UpdatedAt: now.Add(-time.Minute).Format(time.RFC3339), Data: map[string]any{"scheduler_definition_key": definition.ID, "next_run_at": now.Add(time.Hour).Format(time.RFC3339)}}
	repository := &schedulerRepositoryFake{get: func(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
		switch object.Key {
		case "job_definition":
			return definition, id == definition.ID, nil
		case "scheduler_cursor":
			if workspaceID != principal.WorkspaceID {
				t.Fatalf("cursor workspace = %q", workspaceID)
			}
			return cursor, id == cursor.ID, nil
		default:
			t.Fatalf("unexpected object %q", object.Key)
			return recordmodel.Record{}, false, nil
		}
	}}
	audit := &schedulerAuditSpy{}
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, audit, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
	result, err := service.RescheduleDefinition(t.Context(), definition.ID, nextRunAt, principal)
	if err != nil || result.Status != "rescheduled" || result.Run.Data["next_run_at"] != nextRunAt.Format(time.RFC3339) {
		t.Fatalf("reschedule = %+v err=%v", result, err)
	}
	if len(repository.committed) != 1 || len(repository.committed[0]) != 1 {
		t.Fatalf("reschedule commits = %+v", repository.committed)
	}
	commit := repository.committed[0][0]
	if commit.Object.Key != "scheduler_cursor" || commit.Operation != "update" || commit.Optimistic.ExpectedUpdatedAt != cursor.UpdatedAt {
		t.Fatalf("reschedule commit = %+v", commit)
	}
	if len(audit.requests) != 1 || audit.requests[0].Event != "scheduler_definition_rescheduled" || audit.requests[0].RecordID != definition.ID {
		t.Fatalf("reschedule audit = %+v", audit.requests)
	}
	if _, err := service.RescheduleDefinition(t.Context(), definition.ID, time.Time{}, principal); apperror.CodeOf(err) != "backend.scheduler.next_run_at_required" {
		t.Fatalf("zero reschedule time error = %v", err)
	}
	if _, err := service.RescheduleDefinition(t.Context(), definition.ID, nextRunAt, schedulerTestPrincipal("read")); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("reschedule permission error = %v", err)
	}
}

func schedulerRunLookupRepository(t *testing.T, run, definition recordmodel.Record) *schedulerRepositoryFake {
	t.Helper()
	return &schedulerRepositoryFake{get: func(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
		if workspaceID != "workspace-a" {
			t.Fatalf("workspace = %q", workspaceID)
		}
		switch object.Key {
		case "job_run":
			return run, id == run.ID, nil
		case "job_definition":
			return definition, id == definition.ID, nil
		case "scheduler_cursor":
			return recordmodel.Record{}, false, nil
		default:
			t.Fatalf("unexpected object %q", object.Key)
			return recordmodel.Record{}, false, nil
		}
	}}
}

func TestSchedulerResolveDeadLetterReplayAndCommit(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	commandKey := schedulerCommandIdempotencyKey("dead_letter.resolve", "dead-1", "request-1")

	t.Run("missing key", func(t *testing.T) {
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil)
		_, err := service.ResolveDeadLetter(t.Context(), "dead-1", "note", " ", principal)
		assertSchedulerError(t, err, apperror.KindBadRequest, "backend.idempotency.key_required")
	})

	t.Run("replay", func(t *testing.T) {
		deadLetter := recordmodel.Record{ID: "dead-1", Data: map[string]any{"resolution_idempotency_key": commandKey}}
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return deadLetter, true, nil
		}}
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
		result, err := service.ResolveDeadLetter(t.Context(), deadLetter.ID, "note", "request-1", principal)
		if err != nil || result.Status != "replayed" || len(repository.committed) != 0 {
			t.Fatalf("replay = %+v, %v commits=%d", result, err, len(repository.committed))
		}
	})

	t.Run("commit with event and audit", func(t *testing.T) {
		deadLetter := recordmodel.Record{ID: "dead-1", UpdatedAt: "old", Data: map[string]any{"status": "open", "job_run_id": "run-1", "scheduler_definition_key": "definition-1"}}
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return deadLetter, true, nil
		}}
		audit := &schedulerAuditSpy{}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, audit, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
		ctx := requestcontext.WithCorrelationID(t.Context(), "correlation-resolve-1")
		result, err := service.ResolveDeadLetter(ctx, deadLetter.ID, " fixed ", "request-1", principal)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "resolved" || result.Run.Data["status"] != "resolved" || result.Run.Data["resolution_note"] != "fixed" {
			t.Fatalf("result = %+v", result)
		}
		if len(repository.committed) != 1 || len(repository.committed[0]) != 2 || repository.committed[0][0].Operation != "update" || repository.committed[0][1].Object.Key != "job_run_event" {
			t.Fatalf("commits = %+v", repository.committed)
		}
		if len(audit.requests) != 1 || audit.requests[0].Event != "scheduler_dead_letter_resolved" || audit.requests[0].RecordID != deadLetter.ID {
			t.Fatalf("audit = %+v", audit.requests)
		}
		entry := audit.requests[0]
		if entry.Principal.UserID != principal.UserID ||
			entry.Before["status"] != "open" || entry.After["status"] != "resolved" ||
			entry.Metadata["reason"] != "fixed" || entry.Metadata["result"] != "succeeded" ||
			entry.Metadata["correlation_id"] != "correlation-resolve-1" {
			t.Fatalf("dead-letter audit evidence = %+v", entry)
		}
	})

	t.Run("commit failure", func(t *testing.T) {
		wantErr := errors.New("commit failed")
		deadLetter := recordmodel.Record{ID: "dead-1", Data: map[string]any{"status": "open"}}
		repository := &schedulerRepositoryFake{
			get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
				return deadLetter, true, nil
			},
			commit: func(context.Context, string, []transactionmodel.RecordMutationCommit) error { return wantErr },
		}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
		if _, err := service.ResolveDeadLetter(t.Context(), deadLetter.ID, "note", "request-1", principal); !errors.Is(err, wantErr) {
			t.Fatalf("commit error = %v", err)
		}
	})
}

func TestSchedulerAuthorizationContextAndErrorHelpers(t *testing.T) {
	if err := schedulerOperationAllowed(principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown scope error = %v", err)
	}
	for _, permission := range []string{"workspace.admin", "scheduler.command", "job_run.update"} {
		if err := schedulerOperationAllowed(schedulerTestPrincipal(permission)); err != nil {
			t.Fatalf("permission %q rejected: %v", permission, err)
		}
	}
	if err := schedulerAuthorizeQuery(schedulerTestPrincipal("read")); err != nil {
		t.Fatalf("query scope rejected: %v", err)
	}
	if err := schedulerAuthorizeQuery(principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("invalid query scope error = %v", err)
	}
	if got := schedulerWorkspaceID(schedulerTestPrincipal("read")); got != "workspace-a" {
		t.Fatalf("workspace = %q", got)
	}
	system := principalmodel.NewSystemPrincipal("scheduler", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test"))
	if got := schedulerWorkspaceID(system); got != principalmodel.InstallationWorkspaceID {
		t.Fatalf("system workspace = %q", got)
	}
	if got := schedulerWorkspaceID(principalmodel.Principal{}); got != "" {
		t.Fatalf("invalid workspace = %q", got)
	}
	if got := serviceErrorCode(nil); got != "" {
		t.Fatalf("nil error code = %q", got)
	}
	if got := serviceErrorCode(errors.New("plain")); got != "backend.internal" {
		t.Fatalf("plain error code = %q", got)
	}
	if got := valueOrDefault(" ", "fallback"); got != "fallback" || valueOrDefault("value", "fallback") != "value" {
		t.Fatalf("valueOrDefault mismatch")
	}
	err := schedulerError(apperror.KindConflict, "conflict", errors.New("cause"), "", "ignored", "resource", "run", "odd")
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || !reflect.DeepEqual(appErr.Params, map[string]string{"resource": "run"}) {
		t.Fatalf("scheduler error = %#v", err)
	}
	if serviceErrorCode(err) != "conflict" {
		t.Fatalf("app error code = %q", serviceErrorCode(err))
	}
	if apperror.CodeOf(conflict("conflict")) != "conflict" {
		t.Fatal("conflict helper did not preserve code")
	}
	(&SchedulerApplicationService{}).insertOperationAudit(t.Context(), "ignored", "", "", principalmodel.Principal{}, "", nil, nil, nil)
}

func TestSchedulerOperationErrorMatrix(t *testing.T) {
	now := time.Date(2026, 7, 20, 6, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	wantErr := errors.New("store failed")
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "nightly", "target_type": "workflow"}}
	run := recordmodel.Record{ID: "run-1", UpdatedAt: "old", Data: map[string]any{"scheduler_definition_key": definition.ID, "status": "failed"}}

	t.Run("simulate lookup and schema failures", func(t *testing.T) {
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return recordmodel.Record{}, false, wantErr
		}}
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
		if _, err := service.SimulateJob(t.Context(), definition.ID, principal); !errors.Is(err, wantErr) {
			t.Fatalf("definition get error = %v", err)
		}
		repository.get = nil
		if _, err := service.SimulateJob(t.Context(), definition.ID, principal); apperror.CodeOf(err) != "backend.scheduler.definition_not_found" {
			t.Fatalf("definition not found error = %v", err)
		}
		repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return definition, true, nil
		}
		partial := schedulerTestSchema()
		partial.snapshot.Objects = []definitionmodel.ObjectSchema{{Key: "job_definition"}}
		service.schema = partial
		if _, err := service.SimulateJob(t.Context(), definition.ID, principal); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("missing run object error = %v", err)
		}
		partial.snapshot.Objects = []definitionmodel.ObjectSchema{{Key: "job_definition"}, {Key: "job_run"}}
		service.schema = partial
		if _, err := service.SimulateJob(t.Context(), definition.ID, principal); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("missing event object error = %v", err)
		}
		service.schema = schedulerTestSchema()
		repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error { return wantErr }
		if _, err := service.SimulateJob(t.Context(), definition.ID, principal); !errors.Is(err, wantErr) {
			t.Fatalf("simulate commit error = %v", err)
		}
	})

	t.Run("run claim read failure", func(t *testing.T) {
		repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			if object.Key == "job_definition" {
				return definition, true, nil
			}
			return recordmodel.Record{}, false, wantErr
		}}
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
		if _, err := service.RunJob(t.Context(), definition.ID, "request", principal); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
			t.Fatalf("run claim error = %v", err)
		}
	})

	t.Run("retry boundary failures", func(t *testing.T) {
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil)
		if _, err := service.RetryRun(t.Context(), run.ID, " ", principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
			t.Fatalf("retry key error = %v", err)
		}
		if _, err := service.RetryRun(t.Context(), run.ID, "request", schedulerTestPrincipal()); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
			t.Fatalf("retry permission error = %v", err)
		}
		repository := schedulerRunLookupRepository(t, run, definition)
		repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error { return wantErr }
		service.repository = repository
		service.definitions = repository
		if _, err := service.RetryRun(t.Context(), run.ID, "request", principal); !errors.Is(err, wantErr) {
			t.Fatalf("retry commit error = %v", err)
		}
	})

	t.Run("cancel key permission and schema failures", func(t *testing.T) {
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil)
		if _, err := service.CancelRun(t.Context(), run.ID, " ", principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
			t.Fatalf("cancel key error = %v", err)
		}
		if _, err := service.CancelRun(t.Context(), run.ID, "request", schedulerTestPrincipal()); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
			t.Fatalf("cancel permission error = %v", err)
		}
		repository := schedulerRunLookupRepository(t, run, definition)
		partial := schedulerTestSchema()
		partial.snapshot.Objects = []definitionmodel.ObjectSchema{{Key: "job_run"}, {Key: "job_definition"}}
		service = NewSchedulerApplicationService(partial, nil, repository, nil)
		if _, err := service.CancelRun(t.Context(), run.ID, "request", principal); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("cancel event schema error = %v", err)
		}
	})

	t.Run("dead letter resolve failures without event", func(t *testing.T) {
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil)
		if _, err := service.ResolveDeadLetter(t.Context(), "dead", "note", "request", schedulerTestPrincipal()); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
			t.Fatalf("resolve permission error = %v", err)
		}
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return recordmodel.Record{}, false, wantErr
		}}
		service.repository = repository
		if _, err := service.ResolveDeadLetter(t.Context(), "dead", "note", "request", principal); !errors.Is(err, wantErr) {
			t.Fatalf("resolve get error = %v", err)
		}
		repository.get = nil
		if _, err := service.ResolveDeadLetter(t.Context(), "dead", "note", "request", principal); apperror.CodeOf(err) != "backend.scheduler.dead_letter_not_found" {
			t.Fatalf("resolve not found error = %v", err)
		}
		dead := recordmodel.Record{ID: "dead", Data: map[string]any{"status": "open"}}
		repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return dead, true, nil
		}
		service = NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
		result, err := service.ResolveDeadLetter(t.Context(), dead.ID, "note", "request", principal)
		if err != nil || result.Status != "resolved" || len(repository.committed[0]) != 1 {
			t.Fatalf("resolve without run = %+v, %v commits=%+v", result, err, repository.committed)
		}
	})

	t.Run("run lookup errors", func(t *testing.T) {
		repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return recordmodel.Record{}, false, wantErr
		}}
		service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
		if _, err := service.CancelRun(t.Context(), run.ID, "request", principal); !errors.Is(err, wantErr) {
			t.Fatalf("run get error = %v", err)
		}
		repository.get = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			if object.Key == "job_run" {
				return run, true, nil
			}
			return recordmodel.Record{}, false, wantErr
		}
		if _, err := service.CancelRun(t.Context(), run.ID, "request", principal); !errors.Is(err, wantErr) {
			t.Fatalf("definition get error = %v", err)
		}
	})
}
