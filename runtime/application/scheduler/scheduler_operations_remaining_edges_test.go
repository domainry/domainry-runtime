package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type schedulerOperatorCallSchema struct {
	mu               sync.Mutex
	operatorCalls    int
	failOperatorCall int
	snapshot         metadatamodel.MetadataSchemaSnapshot
}

func (s *schedulerOperatorCallSchema) SchemaForPrincipal(_ context.Context, principal principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if principal.UserID == "operator-1" {
		s.operatorCalls++
		if s.operatorCalls == s.failOperatorCall {
			return metadatamodel.MetadataSchemaSnapshot{}
		}
	}
	return s.snapshot
}

func TestSchedulerTenantAdminSimulationSourceFailures(t *testing.T) {
	principal := schedulerTestPrincipal("scheduler.definition.write")
	service := &SchedulerApplicationService{}
	if _, err := service.SimulateTenantAdminDefinition(t.Context(), "definition", principal); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("missing source error = %v", err)
	}

	sourceFailure := errors.New("definition lookup failure")
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{getErr: sourceFailure})
	if _, err := service.SimulateTenantAdminDefinition(t.Context(), "definition", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("lookup error = %v", err)
	}
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{})
	if _, err := service.SimulateTenantAdminDefinition(t.Context(), "definition", principal); apperror.CodeOf(err) != "backend.scheduler.definition_not_found" {
		t.Fatalf("not found error = %v", err)
	}
	service.UseDefinitionSource(schedulerSurfaceDefinitionSource{
		found:      true,
		definition: recordmodel.Record{ID: "definition", Data: map[string]any{}},
	})
	if _, err := service.SimulateTenantAdminDefinition(t.Context(), "definition", principal); err == nil {
		t.Fatal("invalid definition was simulated")
	}
}

func TestSchedulerOperationLateObjectLookupFailures(t *testing.T) {
	now := time.Date(2026, 7, 26, 16, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("scheduler.command")
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{
		"key":              "nightly",
		"target_type":      "workflow",
		"target_key":       "scheduled:*",
		"schedule_type":    "interval",
		"interval_seconds": 60,
		"max_attempts":     3,
	}}

	t.Run("manual run final lookup uses owner visibility", func(t *testing.T) {
		schema := &schedulerOperatorCallSchema{failOperatorCall: 1, snapshot: schedulerTestSchema().snapshot}
		runtime := &schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{}}
		repository := &schedulerRepositoryFake{}
		jobRunGets := 0
		repository.get = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			if object.Key == "job_definition" {
				return definition, true, nil
			}
			if object.Key == "job_run" {
				jobRunGets++
				if jobRunGets > 1 && len(repository.committed) > 0 {
					return repository.committed[len(repository.committed)-1][0].Record, true, nil
				}
			}
			return recordmodel.Record{}, false, nil
		}
		service := NewSchedulerApplicationServiceWithWorker(schema, runtime, repository, nil, workerplatform.Dependencies{
			Clock:    schedulerFixedClock{now: now},
			WorkerID: workerplatform.WorkerID("worker-a"),
		})
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		if _, err := service.RunJob(t.Context(), definition.ID, "manual-request", principal); err != nil || schema.operatorCalls != 0 {
			t.Fatalf("manual owner lookup error=%v operator_schema_calls=%d", err, schema.operatorCalls)
		}
	})

	t.Run("retry update lookup", func(t *testing.T) {
		run := recordmodel.Record{ID: "run-1", UpdatedAt: "old", Data: map[string]any{
			"scheduler_definition_key": definition.ID,
			"status":                   "failed",
		}}
		schema := &schedulerOperatorCallSchema{failOperatorCall: 2, snapshot: schedulerTestSchema().snapshot}
		repository := schedulerRunLookupRepository(t, run, definition)
		service := NewSchedulerApplicationService(schema, nil, repository, nil)
		if _, err := service.RetryRun(t.Context(), run.ID, "retry-request", principal); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
			t.Fatalf("retry update lookup error = %v", err)
		}
	})
}

func TestSchedulerOperationDefinitionBoundaryFailures(t *testing.T) {
	principal := schedulerTestPrincipal("scheduler.command")
	service := &SchedulerApplicationService{}
	if _, err := service.schedulerDefinitionForOperation(t.Context(), "definition", schedulerTestPrincipal()); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("definition permission error = %v", err)
	}
	if _, err := service.schedulerDefinitionForOperation(t.Context(), "definition", principal); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("definition source error = %v", err)
	}

	run := recordmodel.Record{ID: "run-1", Data: map[string]any{"scheduler_definition_key": "definition"}}
	service = &SchedulerApplicationService{
		schema: schedulerTestSchema(),
		repository: &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
			if object.Key == "job_run" {
				return run, true, nil
			}
			return recordmodel.Record{}, false, nil
		}},
	}
	if _, _, err := service.schedulerRunAndDefinition(t.Context(), run.ID, principal); apperror.CodeOf(err) != "backend.scheduler.definition_source_unavailable" {
		t.Fatalf("run definition source error = %v", err)
	}
}

func TestSchedulerRequeueDeadLetterBoundariesAndSuccess(t *testing.T) {
	principal := schedulerTestPrincipal("scheduler.command")
	if _, err := (&SchedulerApplicationService{}).RequeueDeadLetter(t.Context(), "dead", "note", " ", principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
		t.Fatalf("missing key error = %v", err)
	}
	if _, err := (&SchedulerApplicationService{}).RequeueDeadLetter(t.Context(), "dead", "note", "request", schedulerTestPrincipal()); apperror.CodeOf(err) != "backend.scheduler.permission_required" {
		t.Fatalf("permission error = %v", err)
	}

	notFoundService := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil)
	if _, err := notFoundService.RequeueDeadLetter(t.Context(), "missing", "note", "request", principal); apperror.CodeOf(err) != "backend.scheduler.dead_letter_not_found" {
		t.Fatalf("inspect error = %v", err)
	}

	for name, data := range map[string]map[string]any{
		"empty": {"job_run_id": ""},
		"nil":   {},
	} {
		t.Run(name+" run id", func(t *testing.T) {
			deadLetter := recordmodel.Record{ID: "dead-1", Data: data}
			repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
				if object.Key == "job_dead_letter" {
					return deadLetter, true, nil
				}
				return recordmodel.Record{}, false, nil
			}}
			service := NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
			if _, err := service.RequeueDeadLetter(t.Context(), deadLetter.ID, "note", "request", principal); apperror.CodeOf(err) != "backend.scheduler.run_not_found" {
				t.Fatalf("missing run id error = %v", err)
			}
		})
	}

	deadLetter := recordmodel.Record{ID: "dead-1", UpdatedAt: "dead-old", Data: map[string]any{
		"job_run_id":               "run-1",
		"status":                   "open",
		"scheduler_definition_key": "definition-1",
	}}
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "nightly"}}
	makeService := func(commitErr error, includeRun bool) *SchedulerApplicationService {
		retryKey := schedulerCommandIdempotencyKey("retry", "run-1", "request:run")
		run := recordmodel.Record{ID: "run-1", Data: map[string]any{
			"scheduler_definition_key": definition.ID,
			"status":                   "failed",
			"last_command_scope":       "scheduler.retry",
			"last_command_key":         retryKey,
		}}
		repository := &schedulerRepositoryFake{
			get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
				switch object.Key {
				case "job_dead_letter":
					return deadLetter, true, nil
				case "job_run":
					return run, includeRun, nil
				case "job_definition":
					return definition, true, nil
				default:
					return recordmodel.Record{}, false, nil
				}
			},
			commit: func(context.Context, string, []transactionmodel.RecordMutationCommit) error {
				return commitErr
			},
		}
		return NewSchedulerApplicationService(schedulerTestSchema(), nil, repository, nil)
	}

	if _, err := makeService(nil, false).RequeueDeadLetter(t.Context(), deadLetter.ID, "note", "request", principal); apperror.CodeOf(err) != "backend.scheduler.run_not_found" {
		t.Fatalf("retry error = %v", err)
	}
	resolveFailure := errors.New("resolve commit failure")
	if _, err := makeService(resolveFailure, true).RequeueDeadLetter(t.Context(), deadLetter.ID, "note", "request", principal); !errors.Is(err, resolveFailure) {
		t.Fatalf("resolve error = %v", err)
	}
	result, err := makeService(nil, true).RequeueDeadLetter(t.Context(), deadLetter.ID, "note", "request", principal)
	if err != nil || result.Status != "requeued" || result.Run.ID != "run-1" {
		t.Fatalf("requeue result = %+v, err = %v", result, err)
	}
}
