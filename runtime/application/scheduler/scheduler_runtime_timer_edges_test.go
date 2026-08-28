package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type recordTimerRuntimeFake struct {
	workflowResult workflowmodel.WorkflowProcessResult
	workflowErr    error
	execute        func(context.Context, RecordTimerExecution, principalmodel.Principal) error
	executions     []RecordTimerExecution
}

func (f *recordTimerRuntimeFake) ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return f.workflowResult, f.workflowErr
}

func (f *recordTimerRuntimeFake) ExecuteRecordTimer(ctx context.Context, execution RecordTimerExecution, principal principalmodel.Principal) error {
	f.executions = append(f.executions, execution)
	if f.execute != nil {
		return f.execute(ctx, execution, principal)
	}
	return nil
}

type schedulerRepositoryWithoutWorkspace struct{ delegate *schedulerRepositoryFake }

type reportSnapshotRuntimeEdge struct {
	err   error
	delay time.Duration
}

func (r reportSnapshotRuntimeEdge) RefreshSnapshot(ctx context.Context, _, _ string, _ principalmodel.Principal) (reportmodel.ReportSnapshot, error) {
	if r.delay > 0 {
		// Snapshot persistence is allowed to complete after the lease heartbeat
		// cancels the work context; the caller must still surface the lost lease.
		time.Sleep(r.delay)
	}
	if r.err != nil {
		return reportmodel.ReportSnapshot{}, r.err
	}
	return reportmodel.ReportSnapshot{ID: "snapshot-1", Status: "succeeded"}, nil
}

func (r schedulerRepositoryWithoutWorkspace) GetRecord(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
	return r.delegate.GetRecord(ctx, workspaceID, object, id)
}
func (r schedulerRepositoryWithoutWorkspace) ListRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return r.delegate.ListRecords(ctx, workspaceID, object, query)
}
func (r schedulerRepositoryWithoutWorkspace) UpdateRecordWhere(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, conditions map[string]any) (bool, error) {
	return r.delegate.UpdateRecordWhere(ctx, workspaceID, object, record, conditions)
}
func (r schedulerRepositoryWithoutWorkspace) CommitRecordMutationBatch(ctx context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) error {
	return r.delegate.CommitRecordMutationBatch(ctx, workspaceID, commits)
}

func recordTimerCandidate(id string, now time.Time, payload string) recordmodel.Record {
	return recordmodel.Record{
		ID: id, CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), UpdatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano),
		Data: map[string]any{
			"status": "scheduled", "due_at": now.Add(-time.Minute).Format(time.RFC3339Nano),
			"lease_expires_at": "", "fencing_token": 0, "attempt": 0, "max_attempts": 3,
			"retry_delay_seconds": 1, "retry_max_delay_seconds": 60, "payload_json": payload,
			"object_key": "generic_object", "record_id": "record-1", "target_type": "action", "target_key": "action.run",
		},
	}
}

func recordTimerLease(now time.Time) RecordTimerLease {
	record := recordTimerCandidate("timer-1", now, "{}")
	record.Data["status"], record.Data["lease_owner"], record.Data["fencing_token"] = "leased", "worker-a", 7
	return RecordTimerLease{Record: record, Owner: "worker-a", Token: 7}
}

func TestSchedulerFinishAndBatchRecordTimerEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	lease := recordTimerLease(now)

	if err := recordTimerTestService(&schedulerRepositoryFake{}, now, true).FinishRecordTimer(t.Context(), "workspace", lease, now, principalmodel.SystemScope{}); err == nil {
		t.Fatal("finish without system scope accepted")
	}
	if err := recordTimerTestService(&schedulerRepositoryFake{}, now, false).FinishRecordTimer(t.Context(), "workspace", lease, now, schedulerRuntimeScope()); err == nil {
		t.Fatal("finish without timer object accepted")
	}

	var saved recordmodel.Record
	repository := &schedulerRepositoryFake{update: func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record, conditions map[string]any) (bool, error) {
		saved = record
		if conditions["status"] != "leased" || conditions["lease_owner"] != "worker-a" || conditions["fencing_token"] != 7 {
			t.Fatalf("finish conditions=%#v", conditions)
		}
		return true, nil
	}}
	if err := recordTimerTestService(repository, now, true).FinishRecordTimer(t.Context(), "workspace", lease, time.Time{}, schedulerRuntimeScope()); err != nil || saved.Data["status"] != "fired" || saved.Data["lease_owner"] != "" {
		t.Fatalf("saved=%#v err=%v", saved, err)
	}
	repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
		return false, errors.New("update")
	}
	if err := recordTimerTestService(repository, now, true).FinishRecordTimer(t.Context(), "workspace", lease, now, schedulerRuntimeScope()); err == nil {
		t.Fatal("finish update error lost")
	}
	repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
		return false, nil
	}
	if err := recordTimerTestService(repository, now, true).FinishRecordTimer(t.Context(), "workspace", lease, now, schedulerRuntimeScope()); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("finish conflict=%v", err)
	}

	service := recordTimerTestService(&schedulerRepositoryFake{}, now, true)
	if err := service.FinishRecordTimers(t.Context(), "workspace", nil, now, principalmodel.SystemScope{}); err == nil {
		t.Fatal("batch finish without scope accepted")
	}
	if err := service.FinishRecordTimers(t.Context(), "workspace", nil, now, schedulerRuntimeScope()); err != nil {
		t.Fatalf("empty batch=%v", err)
	}
	if err := recordTimerTestService(&schedulerRepositoryFake{}, now, false).FinishRecordTimers(t.Context(), "workspace", []RecordTimerLease{lease}, now, schedulerRuntimeScope()); err == nil {
		t.Fatal("batch finish without timer object accepted")
	}
	batchRepository := &schedulerRepositoryFake{}
	if err := recordTimerTestService(batchRepository, now, true).FinishRecordTimers(t.Context(), "workspace", []RecordTimerLease{lease, lease}, time.Time{}, schedulerRuntimeScope()); err != nil || len(batchRepository.committed) != 1 || len(batchRepository.committed[0]) != 2 {
		t.Fatalf("batch commits=%#v err=%v", batchRepository.committed, err)
	}
	batchRepository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error {
		return errors.New("commit")
	}
	if err := recordTimerTestService(batchRepository, now, true).FinishRecordTimers(t.Context(), "workspace", []RecordTimerLease{lease}, now, schedulerRuntimeScope()); err == nil {
		t.Fatal("batch commit error lost")
	}
}

func TestSchedulerFailRecordTimerRemainingEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 30, 0, 0, time.UTC)
	lease := recordTimerLease(now)
	if err := recordTimerTestService(&schedulerRepositoryFake{}, now, false).FailRecordTimer(t.Context(), "workspace", lease, errors.New("execute"), now, schedulerRuntimeScope()); err == nil {
		t.Fatal("failure without timer object accepted")
	}
	repository := &schedulerRepositoryFake{update: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
		return false, errors.New("update")
	}}
	if err := recordTimerTestService(repository, now, true).FailRecordTimer(t.Context(), "workspace", lease, errors.New("execute"), time.Time{}, schedulerRuntimeScope()); err == nil {
		t.Fatal("failure update error lost")
	}
}

func TestSchedulerProcessDueRecordTimerExecutionEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 13, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	if _, err := recordTimerTestService(&schedulerRepositoryFake{}, now, true).ProcessDueRecordTimers(t.Context(), "workspace", now, 1, principal, schedulerRuntimeScope()); err == nil {
		t.Fatal("missing target runtime accepted")
	}

	listErr := errors.New("list")
	repository := &schedulerRepositoryFake{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, listErr
	}}
	service := recordTimerTestService(repository, now, true)
	service.runtime = &recordTimerRuntimeFake{}
	if _, err := service.ProcessDueRecordTimers(t.Context(), "workspace", now, 1, principal, schedulerRuntimeScope()); err == nil {
		t.Fatal("claim list error lost")
	}

	tests := []struct {
		name       string
		payload    string
		executeErr error
		updateErr  error
		wantCount  int
		wantErr    bool
		wantCalls  int
	}{
		{name: "invalid payload is failed", payload: "{", wantErr: true},
		{name: "invalid payload failure persistence wins", payload: "{", updateErr: errors.New("fail update"), wantErr: true},
		{name: "execution failure is persisted", payload: "{}", executeErr: errors.New("execute"), wantErr: true, wantCalls: 1},
		{name: "execution failure persistence wins", payload: "{}", executeErr: errors.New("execute"), updateErr: errors.New("fail update"), wantErr: true, wantCalls: 1},
		{name: "finish failure", payload: "{}", updateErr: errors.New("finish update"), wantErr: true, wantCalls: 1},
		{name: "success", payload: "{\"key\":\"value\"}", wantCount: 1, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := recordTimerCandidate("timer-1", now, test.payload)
			repository := &schedulerRepositoryFake{
				list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
					return recordmodel.RecordPageResult{Items: []recordmodel.Record{candidate}, Total: 1}, nil
				},
				update: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
					if test.updateErr != nil {
						return false, test.updateErr
					}
					return true, nil
				},
			}
			runtime := &recordTimerRuntimeFake{execute: func(context.Context, RecordTimerExecution, principalmodel.Principal) error { return test.executeErr }}
			service := recordTimerTestService(repository, now, true)
			service.runtime = runtime
			count, err := service.ProcessDueRecordTimers(t.Context(), "workspace", now, 1, principal, schedulerRuntimeScope())
			if count != test.wantCount || (err != nil) != test.wantErr || len(runtime.executions) != test.wantCalls {
				t.Fatalf("count=%d err=%v executions=%#v", count, err, runtime.executions)
			}
			if test.wantCount == 1 && runtime.executions[0].Payload["key"] != "value" {
				t.Fatalf("execution=%#v", runtime.executions[0])
			}
		})
	}

	candidates := []recordmodel.Record{recordTimerCandidate("timer-1", now, "{"), recordTimerCandidate("timer-2", now, "{")}
	repository = &schedulerRepositoryFake{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: candidates, Total: len(candidates)}, nil
	}}
	service = recordTimerTestService(repository, now, true)
	service.runtime = &recordTimerRuntimeFake{}
	if count, err := service.ProcessDueRecordTimers(t.Context(), "workspace", now, 2, principal, schedulerRuntimeScope()); count != 0 || err == nil {
		t.Fatalf("multiple invalid payloads count=%d err=%v", count, err)
	}

	candidates = []recordmodel.Record{
		recordTimerCandidate("timer-1", now, "{"),
		recordTimerCandidate("timer-2", now, "{}"),
		recordTimerCandidate("timer-3", now, "{}"),
	}
	repository = &schedulerRepositoryFake{
		list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			return recordmodel.RecordPageResult{Items: candidates, Total: len(candidates)}, nil
		},
		update: func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record, _ map[string]any) (bool, error) {
			if record.ID == "timer-3" {
				return false, errors.New("finish")
			}
			return true, nil
		},
	}
	sequenceRuntime := &recordTimerRuntimeFake{execute: func(_ context.Context, execution RecordTimerExecution, _ principalmodel.Principal) error {
		if execution.TimerID == "timer-2" {
			return errors.New("execute")
		}
		return nil
	}}
	service = recordTimerTestService(repository, now, true)
	service.runtime = sequenceRuntime
	if count, err := service.ProcessDueRecordTimers(t.Context(), "workspace", now, 3, principal, schedulerRuntimeScope()); count != 0 || err == nil || len(sequenceRuntime.executions) != 2 {
		t.Fatalf("preserve first error count=%d err=%v executions=%#v", count, err, sequenceRuntime.executions)
	}

	repository = &schedulerRepositoryFake{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{recordTimerCandidate("timer-empty", now, " ")}, Total: 1}, nil
	}}
	service = recordTimerTestService(repository, now, true)
	service.runtime = &recordTimerRuntimeFake{}
	if count, err := service.ProcessDueRecordTimers(t.Context(), "workspace", now, 1, principal, schedulerRuntimeScope()); count != 1 || err != nil {
		t.Fatalf("empty payload count=%d err=%v", count, err)
	}
}

func TestSchedulerProcessDueRecordTimersForAllWorkspacesEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 14, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	runtime := &recordTimerRuntimeFake{}
	service := recordTimerTestService(&schedulerRepositoryFake{}, now, true)
	service.runtime = runtime
	if _, err := service.ProcessDueRecordTimersForAllWorkspaces(t.Context(), now, 1, principal, principalmodel.SystemScope{}); err == nil {
		t.Fatal("all-workspace processing without scope accepted")
	}

	plain := schedulerRepositoryWithoutWorkspace{delegate: &schedulerRepositoryFake{}}
	service = NewSchedulerApplicationServiceWithWorker(
		schedulerTestSchema(), runtime, plain, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}},
	)
	if _, err := service.ProcessDueRecordTimersForAllWorkspaces(t.Context(), now, 1, principal, schedulerRuntimeScope()); err == nil {
		t.Fatal("repository without workspace listing accepted")
	}

	repository := &schedulerRepositoryFake{workspaceList: func(context.Context, definitionmodel.ObjectSchema, time.Time) ([]string, error) {
		return nil, errors.New("workspaces")
	}}
	service = recordTimerTestService(repository, now, true)
	service.runtime = runtime
	if _, err := service.ProcessDueRecordTimersForAllWorkspaces(t.Context(), time.Time{}, 1, principal, schedulerRuntimeScope()); err == nil {
		t.Fatal("workspace list error lost")
	}
	service = recordTimerTestService(&schedulerRepositoryFake{}, now, false)
	service.runtime = runtime
	if _, err := service.ProcessDueRecordTimersForAllWorkspaces(t.Context(), now, 1, principal, schedulerRuntimeScope()); err == nil {
		t.Fatal("missing timer object accepted")
	}
	service = recordTimerTestService(&schedulerRepositoryFake{}, now, true)
	service.runtime = runtime
	if count, err := service.ProcessDueRecordTimersForAllWorkspaces(t.Context(), now, 1, principal, schedulerRuntimeScope()); err != nil || count != 0 {
		t.Fatalf("empty workspaces count=%d err=%v", count, err)
	}

	workspaces := []string{"workspace-a", "workspace-b", "workspace-c"}
	repository = &schedulerRepositoryFake{
		workspaceList: func(context.Context, definitionmodel.ObjectSchema, time.Time) ([]string, error) {
			return workspaces, nil
		},
		list: func(_ context.Context, workspaceID string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			if query.Filters["status"] == "leased" {
				return recordmodel.RecordPageResult{}, nil
			}
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{recordTimerCandidate("timer-"+workspaceID, now, "{}")}, Total: 1}, nil
		},
	}
	service = recordTimerTestService(repository, now, true)
	service.runtime = runtime
	if count, err := service.ProcessDueRecordTimersForAllWorkspaces(t.Context(), now, 2, principal, schedulerRuntimeScope()); err != nil || count != 2 {
		t.Fatalf("limited fair processing count=%d err=%v", count, err)
	}
	if count, err := service.ProcessDueRecordTimersForAllWorkspaces(t.Context(), now, 4, principal, schedulerRuntimeScope()); err != nil || count != 3 {
		t.Fatalf("base quota processing count=%d err=%v", count, err)
	}

	runtime.execute = func(context.Context, RecordTimerExecution, principalmodel.Principal) error {
		return errors.New("execute")
	}
	if count, err := service.ProcessDueRecordTimersForAllWorkspaces(t.Context(), now, 3, principal, schedulerRuntimeScope()); count != 0 || err == nil {
		t.Fatalf("workspace failures count=%d err=%v", count, err)
	}
}

func TestSchedulerDefinitionQueueLagEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 15, 0, 0, 0, time.UTC)
	definitions := []recordmodel.Record{
		{Data: map[string]any{"next_run_at": "invalid"}},
		{Data: map[string]any{"next_run_at": now.Add(-time.Minute).Format(time.RFC3339)}},
		{Data: map[string]any{"next_run_at": now.Add(-2 * time.Minute).Format(time.RFC3339)}},
	}
	if lag := schedulerDefinitionQueueLag(definitions, now); lag != 2*time.Minute {
		t.Fatalf("lag=%v", lag)
	}
	if lag := schedulerDefinitionQueueLag([]recordmodel.Record{{Data: map[string]any{"next_run_at": now.Add(time.Minute).Format(time.RFC3339)}}}, now); lag != 0 {
		t.Fatalf("future lag=%v", lag)
	}
	if lag := schedulerDefinitionQueueLag(nil, now); lag != 0 {
		t.Fatalf("empty lag=%v", lag)
	}
}

func TestSchedulerWorkerTickRecordTimerAndWorkflowEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 15, 30, 0, 0, time.UTC)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	recordTimerTestService(&schedulerRepositoryFake{}, now, true).processWorkerTick(cancelled, 1)

	workflowOnly := &schedulerRuntimeFake{processErr: errors.New("workflow")}
	service := recordTimerTestService(&schedulerRepositoryFake{}, now, true)
	service.runtime = workflowOnly
	service.processWorkerTick(t.Context(), 1)

	timerRuntime := &recordTimerRuntimeFake{}
	service = recordTimerTestService(&schedulerRepositoryFake{}, now, true)
	service.runtime = timerRuntime
	service.processWorkerTick(t.Context(), 1)

	repository := &schedulerRepositoryFake{workspaceList: func(context.Context, definitionmodel.ObjectSchema, time.Time) ([]string, error) {
		return nil, errors.New("workspace list")
	}}
	service = recordTimerTestService(repository, now, true)
	service.runtime = timerRuntime
	service.processWorkerTick(t.Context(), 1)

	repository = &schedulerRepositoryFake{
		workspaceList: func(context.Context, definitionmodel.ObjectSchema, time.Time) ([]string, error) {
			return []string{"workspace-a"}, nil
		},
		list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{recordTimerCandidate("timer-worker", now, "{}")}, Total: 1}, nil
		},
	}
	timerRuntime = &recordTimerRuntimeFake{workflowResult: workflowmodel.WorkflowProcessResult{Processed: 1}}
	service = recordTimerTestService(repository, now, true)
	service.runtime = timerRuntime
	service.processWorkerTick(t.Context(), 1)

	workerCtx, stopWorker := context.WithCancel(t.Context())
	timerRuntime = &recordTimerRuntimeFake{execute: func(context.Context, RecordTimerExecution, principalmodel.Principal) error {
		stopWorker()
		return nil
	}}
	service = recordTimerTestService(repository, now, true)
	service.runtime = timerRuntime
	service.processWorkerTick(workerCtx, 1)
}

func TestSchedulerProcessDueJobsLoopControlEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 15, 45, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	definitionA := recordmodel.Record{ID: "definition-a", Data: map[string]any{"key": "definition-a", "status": "enabled", "target_type": "workflow", "target_key": "scheduled:*"}}
	definitionB := recordmodel.Record{ID: "definition-b", Data: map[string]any{"key": "definition-b", "status": "enabled", "target_type": "workflow", "target_key": "scheduled:*"}}

	cancelled, cancel := context.WithCancel(t.Context())
	repository := &schedulerRepositoryFake{list: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		if object.Key == "job_definition" {
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{definitionA}}, nil
		}
		cancel()
		return recordmodel.RecordPageResult{}, nil
	}}
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{}, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
	if _, err := service.ProcessDueJobs(cancelled, 1, principal, "scheduler"); !errors.Is(err, context.Canceled) {
		t.Fatalf("loop cancellation=%v", err)
	}

	newService := func(runtime *schedulerRuntimeFake, get func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error), definitions []recordmodel.Record) *SchedulerApplicationService {
		repository := &schedulerRepositoryFake{
			list: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
				if object.Key == "job_definition" {
					return recordmodel.RecordPageResult{Items: definitions}, nil
				}
				return recordmodel.RecordPageResult{}, nil
			},
			get: get,
			update: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
				return true, nil
			},
		}
		service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
		service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
			return nil
		}
		return service
	}

	service = newService(&schedulerRuntimeFake{}, func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{ID: "finished", Data: map[string]any{"status": "succeeded"}}, true, nil
	}, []recordmodel.Record{definitionA})
	if result, err := service.ProcessDueJobs(t.Context(), 1, principal, "scheduler"); err != nil || result.Processed != 0 {
		t.Fatalf("unclaimed result=%#v err=%v", result, err)
	}

	service = newService(&schedulerRuntimeFake{}, func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, errors.New("get")
	}, []recordmodel.Record{definitionA})
	if _, err := service.ProcessDueJobs(t.Context(), 1, principal, "scheduler"); err == nil {
		t.Fatal("claim error lost")
	}

	leased := func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{ID: id, Data: map[string]any{"status": "queued", "attempt": 0, "fencing_token": 1}}, true, nil
	}
	service = newService(&schedulerRuntimeFake{processErr: errors.New("process")}, leased, []recordmodel.Record{definitionA})
	if _, err := service.ProcessDueJobs(t.Context(), 1, principal, "scheduler"); err == nil {
		t.Fatal("claimed process error lost")
	}

	runtime := &schedulerRuntimeFake{processResult: workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "execution", Status: "succeeded"}}}}
	service = newService(runtime, leased, []recordmodel.Record{definitionA, definitionB})
	if result, err := service.ProcessDueJobs(t.Context(), 1, principal, "scheduler"); err != nil || result.Processed != 1 || runtime.processCalls != 1 {
		t.Fatalf("limit result=%#v err=%v calls=%d", result, err, runtime.processCalls)
	}
}

func TestSchedulerProcessClaimedReportSnapshotEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 16, 0, 0, 0, time.UTC)
	principal := schedulerTestPrincipal("workspace.admin")
	definition := recordmodel.Record{ID: "snapshot-definition", Data: map[string]any{"target_type": "report_snapshot_refresh", "target_key": "operations"}}
	run := schedulerLeasedRun("snapshot-run")

	tests := []struct {
		name       string
		snapshot   reportSnapshotRuntimeEdge
		commitErr  error
		wantErr    bool
		wantStatus string
	}{
		{name: "success", wantStatus: "succeeded"},
		{name: "snapshot error", snapshot: reportSnapshotRuntimeEdge{err: errors.New("snapshot")}, wantErr: true, wantStatus: "retrying"},
		{name: "snapshot error finish failure", snapshot: reportSnapshotRuntimeEdge{err: errors.New("snapshot")}, commitErr: errors.New("commit"), wantErr: true},
		{name: "success finish failure", commitErr: errors.New("commit"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &schedulerRepositoryFake{commit: func(context.Context, string, []transactionmodel.RecordMutationCommit) error { return test.commitErr }}
			service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{}, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
			service.UseReportSnapshotRuntime(test.snapshot)
			_, err := service.ProcessClaimedRun(t.Context(), definition, run, 1, principal, now)
			if (err != nil) != test.wantErr {
				t.Fatalf("err=%v", err)
			}
			if test.wantStatus != "" && repository.committed[0][0].Record.Data["status"] != test.wantStatus {
				t.Fatalf("commit=%#v", repository.committed)
			}
		})
	}

	heartbeatRepository := &schedulerRepositoryFake{
		get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
			return run, true, nil
		},
		update: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
			return false, errors.New("heartbeat")
		},
	}
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), &schedulerRuntimeFake{}, heartbeatRepository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
	service.ConfigureWorker(WorkerConfig{LeaseTTL: 3 * time.Millisecond})
	service.UseReportSnapshotRuntime(reportSnapshotRuntimeEdge{delay: 20 * time.Millisecond})
	if _, err := service.ProcessClaimedRun(t.Context(), definition, run, 1, principal, now); err == nil {
		t.Fatal("heartbeat failure lost")
	}
}
