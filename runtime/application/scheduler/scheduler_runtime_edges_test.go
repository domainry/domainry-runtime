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

type schedulerRuntimeFake struct {
	processResult workflowmodel.WorkflowProcessResult
	processErr    error
	processCalls  int
}

type schedulerTargetedRuntimeFake struct {
	schedulerRuntimeFake
	target string
}

func (f *schedulerTargetedRuntimeFake) ProcessDueWorkflowExecutionsForTarget(_ context.Context, target string, _ int, _ principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	f.target = target
	return f.processResult, f.processErr
}

type schedulerTickSignalRuntime struct {
	once sync.Once
	tick chan struct{}
}

func (f *schedulerTickSignalRuntime) ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	f.once.Do(func() { close(f.tick) })
	return workflowmodel.WorkflowProcessResult{}, nil
}

func (f *schedulerRuntimeFake) ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	f.processCalls++
	return f.processResult, f.processErr
}

func TestSchedulerProcessClaimedWorkflowSuccessErrorAndUnsupported(t *testing.T) {
	now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"target_type": "workflow"}}
	run := schedulerLeasedRun("run-1")

	t.Run("success", func(t *testing.T) {
		runtime := &schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "execution-1", Status: "succeeded"}}}}
		repository := &schedulerRepositoryFake{}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		result, err := service.ProcessClaimedRun(t.Context(), definition, run, 5, principal, now)
		if err != nil || result.Processed != 1 || runtime.processCalls != 1 || len(repository.committed) != 1 || repository.committed[0][0].Record.Data["status"] != "succeeded" {
			t.Fatalf("result=%+v err=%v calls=%d commits=%+v", result, err, runtime.processCalls, repository.committed)
		}
	})

	t.Run("directs a published workflow definition to its target", func(t *testing.T) {
		runtime := &schedulerTargetedRuntimeFake{schedulerRuntimeFake: schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "execution-1", Status: "succeeded"}}}}}
		repository := &schedulerRepositoryFake{}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		targetedDefinition := definition
		targetedDefinition.Data["target_key"] = "commission_review_workflow"
		result, err := service.ProcessClaimedRun(t.Context(), targetedDefinition, run, 5, principal, now)
		if err != nil || result.Processed != 1 || runtime.target != "commission_review_workflow" {
			t.Fatalf("result=%+v err=%v target=%q", result, err, runtime.target)
		}
	})

	t.Run("runtime error finishes retry", func(t *testing.T) {
		wantErr := errors.New("provider timeout")
		runtime := &schedulerRuntimeFake{processErr: wantErr}
		repository := &schedulerRepositoryFake{}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		if _, err := service.ProcessClaimedRun(t.Context(), definition, run, 5, principal, now); !errors.Is(err, wantErr) {
			t.Fatalf("runtime error = %v", err)
		}
		if repository.committed[0][0].Record.Data["status"] != "retrying" {
			t.Fatalf("finished run = %+v", repository.committed[0][0].Record)
		}
	})

	t.Run("finish failure wins", func(t *testing.T) {
		processErr := errors.New("runtime failed")
		commitErr := errors.New("commit failed")
		runtime := &schedulerRuntimeFake{processErr: processErr}
		repository := &schedulerRepositoryFake{commit: func(context.Context, string, []transactionmodel.RecordMutationCommit) error { return commitErr }}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		if _, err := service.ProcessClaimedRun(t.Context(), definition, run, 5, principal, now); !errors.Is(err, commitErr) {
			t.Fatalf("finish error = %v", err)
		}
	})

	t.Run("unsupported target", func(t *testing.T) {
		runtime := &schedulerRuntimeFake{}
		repository := &schedulerRepositoryFake{}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		unsupported := recordmodel.Record{ID: "definition-1", Data: map[string]any{"target_type": "notification"}}
		if _, err := service.ProcessClaimedRun(t.Context(), unsupported, run, 5, principal, now); apperror.CodeOf(err) != "backend.scheduler.unsupported_target_type" {
			t.Fatalf("unsupported error = %v", err)
		}
		if repository.committed[0][0].Record.Data["status"] != "retrying" {
			t.Fatalf("unsupported finish = %+v", repository.committed[0][0].Record)
		}
	})

	if _, err := NewSchedulerApplicationService(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil).ProcessClaimedRun(t.Context(), definition, run, 1, principalmodel.Principal{}, now); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error = %v", err)
	}
}

func TestSchedulerProcessClaimedReportExportSuccessAndError(t *testing.T) {
	now := time.Date(2026, 7, 20, 0, 30, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	run := schedulerLeasedRun("report-run")
	definition := recordmodel.Record{ID: "report-definition", Data: map[string]any{"target_type": "report_export", "target_key": "sales"}}
	objects := append([]definitionmodel.ObjectSchema{}, schedulerTestSchema().snapshot.Objects...)
	for _, key := range []string{"report_definition", "report_query_run", "report_export_audit", "download_task"} {
		objects = append(objects, definitionmodel.ObjectSchema{Key: key})
	}
	schema := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: objects}}

	t.Run("success", func(t *testing.T) {
		repository := &schedulerRepositoryFake{}
		service := NewSchedulerApplicationServiceWithWorker(schema, &schedulerRuntimeFake{}, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		if _, err := service.ProcessClaimedRun(t.Context(), definition, run, 5, principal, now); err != nil {
			t.Fatal(err)
		}
		if len(repository.committed) != 1 || repository.committed[0][0].Record.Data["status"] != "succeeded" || repository.committed[0][0].Record.Data["result_json"] != `{"status":"succeeded","workflow_execution_count":0,"business_evidence_count":3}` {
			t.Fatalf("report finish = %+v", repository.committed)
		}
	})

	t.Run("evidence error is persisted", func(t *testing.T) {
		repository := &schedulerRepositoryFake{}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{}, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		if _, err := service.ProcessClaimedRun(t.Context(), definition, run, 5, principal, now); apperror.CodeOf(err) != "backend.scheduler.target_object_missing" {
			t.Fatalf("report error = %v", err)
		}
		if repository.committed[0][0].Record.Data["status"] != "retrying" {
			t.Fatalf("report failure finish = %+v", repository.committed[0][0].Record)
		}
	})
}

func TestSchedulerProcessDueJobsFallbackAndDefinitionLoop(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	runtime := &schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 2}}
	partial := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "job_definition"}}}}
	service := NewSchedulerApplicationServiceWithWorker(partial, runtime, &schedulerRepositoryFake{}, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
	result, err := service.ProcessDueJobs(t.Context(), 3, principal, "scheduler")
	if err != nil || result.Processed != 2 || runtime.processCalls != 1 {
		t.Fatalf("fallback = %+v, %v calls=%d", result, err, runtime.processCalls)
	}

	definition := recordmodel.Record{ID: "definition-1", Data: map[string]any{"key": "definition-1", "target_type": "workflow", "target_key": "scheduled:*", "status": "enabled"}}
	runID := RunIDForDefinition(definition, "scheduler", now)
	repository := &schedulerRepositoryFake{
		list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{definition}}, nil
		},
		get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
			if object.Key == "job_run" {
				return recordmodel.Record{ID: runID, Data: map[string]any{"status": "queued", "attempt": 0, "fencing_token": 1}}, id == runID, nil
			}
			return recordmodel.Record{}, false, nil
		},
		update: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
			return true, nil
		},
	}
	runtime = &schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "execution-1", Status: "succeeded"}}}}
	service = NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
	service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
		return nil
	}
	result, err = service.ProcessDueJobs(t.Context(), 1, principal, "scheduler")
	if err != nil || result.Processed != 1 || runtime.processCalls != 1 {
		t.Fatalf("definition loop = %+v, %v calls=%d", result, err, runtime.processCalls)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.ProcessDueJobs(cancelled, 1, principal, "scheduler"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled loop error = %v", err)
	}

	t.Run("does not synthesize an unpublished default definition", func(t *testing.T) {
		runtime := &schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "default-execution", Status: "succeeded"}}}}
		repository := &schedulerRepositoryFake{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			return recordmodel.RecordPageResult{}, nil
		}}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		result, err := service.ProcessDueJobs(t.Context(), 1, principal, "scheduler")
		if err != nil || result.Processed != 0 || runtime.processCalls != 0 || len(repository.committed) != 0 {
			t.Fatalf("unpublished default leaked into execution = %+v, %v calls=%d commits=%+v", result, err, runtime.processCalls, repository.committed)
		}
	})
}

func TestSchedulerWorkerTickStartAndQueueLag(t *testing.T) {
	now := time.Date(2026, 7, 20, 2, 0, 0, 0, time.UTC)
	runtime := &schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 1}}
	partial := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "job_definition"}}}}
	service := NewSchedulerApplicationServiceWithWorker(partial, runtime, &schedulerRepositoryFake{}, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
	service.processWorkerTick(t.Context(), 1)
	if runtime.processCalls != 1 {
		t.Fatalf("worker tick process=%d", runtime.processCalls)
	}
	service.processWorkerTick(t.Context(), 1)
	if runtime.processCalls != 2 {
		t.Fatalf("second worker tick process=%d", runtime.processCalls)
	}
	runtime.processErr = errors.New("process failed")
	service.processWorkerTick(t.Context(), 1)
	if runtime.processCalls != 3 {
		t.Fatalf("failed process tick calls=%d", runtime.processCalls)
	}
	runtime.processErr = nil
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	service.processWorkerTick(cancelled, 1)
	if runtime.processCalls != 3 {
		t.Fatal("cancelled tick called runtime")
	}

	if done := service.StartWorker(t.Context(), WorkerConfig{Enabled: false}, true); !channelClosed(done) {
		t.Fatal("disabled worker was not stopped")
	}
	if done := service.StartWorker(t.Context(), WorkerConfig{Enabled: true}, false); !channelClosed(done) {
		t.Fatal("unavailable worker was not stopped")
	}
	workerContext, stopWorker := context.WithCancel(context.Background())
	workerRuntime := &schedulerTickSignalRuntime{tick: make(chan struct{})}
	workerService := NewSchedulerApplicationServiceWithWorker(partial, workerRuntime, &schedulerRepositoryFake{}, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
	done := workerService.StartWorker(workerContext, WorkerConfig{Enabled: true, PollInterval: time.Hour}, true)
	select {
	case <-workerRuntime.tick:
	case <-time.After(time.Second):
		t.Fatal("enabled worker did not tick")
	}
	stopWorker()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enabled worker did not stop")
	}

	definitions := []recordmodel.Record{
		{Data: map[string]any{"next_run_at": now.Add(-5 * time.Minute).Format(time.RFC3339)}},
		{Data: map[string]any{"next_run_at": "bad"}},
		{Data: map[string]any{"next_run_at": now.Add(-time.Minute).Format(time.RFC3339)}},
	}
	if got := schedulerDefinitionQueueLag(definitions, now); got != 5*time.Minute {
		t.Fatalf("queue lag = %v", got)
	}
	if schedulerDefinitionQueueLag(nil, now) != 0 || schedulerDefinitionQueueLag([]recordmodel.Record{{Data: map[string]any{"next_run_at": now.Add(time.Minute).Format(time.RFC3339)}}}, now) != 0 {
		t.Fatal("empty/future queue lag was non-zero")
	}
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}
