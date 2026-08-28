package scheduler

import (
	"context"
	"testing"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type schedulerCheckpointRuntime struct {
	schedulerRuntimeFake
	checkpoints []string
	pages       []workflowmodel.WorkflowScheduledPage
}

func (r *schedulerCheckpointRuntime) ProcessScheduledWorkflowWindowPage(_ context.Context, _ string, _ time.Time, _ int, checkpoint string, _ principalmodel.Principal) (workflowmodel.WorkflowScheduledPage, error) {
	r.checkpoints = append(r.checkpoints, checkpoint)
	page := r.pages[0]
	r.pages = r.pages[1:]
	return page, nil
}

func TestSchedulerPersistsAndResumesBoundedWorkflowCheckpointBeforeAdvancing(t *testing.T) {
	now := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	runtime := &schedulerCheckpointRuntime{pages: []workflowmodel.WorkflowScheduledPage{
		{WorkflowProcessResult: workflowmodel.WorkflowProcessResult{Processed: 2, Executions: []workflowmodel.WorkflowExecution{{ID: "one"}, {ID: "two"}}}, Scanned: 2, Checkpoint: "opaque-next"},
		{WorkflowProcessResult: workflowmodel.WorkflowProcessResult{Processed: 1, Executions: []workflowmodel.WorkflowExecution{{ID: "three"}}}, Scanned: 1, Complete: true},
	}}
	repository := &schedulerRepositoryFake{}
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), runtime, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}, WorkerID: workerplatform.WorkerID("worker-a")})
	definition := recordmodel.Record{ID: "scheduled", Data: map[string]any{"target_type": "workflow", "target_key": "scheduled:activate"}}
	run := schedulerLeasedRun("run-1")
	run.Data["triggered_by"] = "scheduler"
	run.Data["scheduled_for"] = now.Format(time.RFC3339)
	first, err := service.ProcessClaimedRun(t.Context(), definition, run, 2, schedulerTestPrincipal("workspace.admin"), now)
	if err != nil || first.Processed != 2 || len(repository.committed) != 1 {
		t.Fatalf("first=%+v commits=%+v err=%v", first, repository.committed, err)
	}
	checkpointed := repository.committed[0][0].Record
	if checkpointed.Data["status"] != "queued" || checkpointed.Data["checkpoint_cursor"] != "opaque-next" || checkpointed.Data["checkpoint_processed"] != 2 {
		t.Fatalf("checkpointed run=%+v", checkpointed.Data)
	}
	checkpointed.Data["status"] = "leased"
	checkpointed.Data["lease_owner"] = "worker-a"
	checkpointed.Data["fencing_token"] = 2
	checkpointed.Data["lease_expires_at"] = now.Add(time.Minute).Format(time.RFC3339)
	repository.committed = nil
	second, err := service.ProcessClaimedRun(t.Context(), definition, checkpointed, 2, schedulerTestPrincipal("workspace.admin"), now)
	if err != nil || second.Processed != 1 || len(repository.committed) != 1 {
		t.Fatalf("second=%+v commits=%+v err=%v", second, repository.committed, err)
	}
	finished := repository.committed[0][0].Record
	if finished.Data["status"] != "succeeded" || finished.Data["checkpoint_cursor"] != "" || finished.Data["checkpoint_processed"] != 3 {
		t.Fatalf("finished run=%+v", finished.Data)
	}
	if len(runtime.checkpoints) != 2 || runtime.checkpoints[0] != "" || runtime.checkpoints[1] != "opaque-next" {
		t.Fatalf("resume checkpoints=%v", runtime.checkpoints)
	}
}
