package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func schedulerRuntimeScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "scheduler test")
}

func schedulerLeasedRun(id string) recordmodel.Record {
	return recordmodel.Record{ID: id, Data: map[string]any{
		"status": "leased", "lease_owner": "worker-a", "fencing_token": 2,
		"attempt": 1, "max_attempts": 3, "timeout_seconds": 60,
		"retry_delay_seconds": 10, "retry_max_delay_seconds": 100,
	}}
}

func TestSchedulerFinishRunStateMatrix(t *testing.T) {
	started := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	now := started.Add(10 * time.Second)

	tests := []struct {
		name       string
		mutate     func(*recordmodel.Record)
		executions []workflowmodel.WorkflowExecution
		processErr error
		wantStatus string
		wantEvent  string
		wantCat    string
	}{
		{name: "success", wantStatus: "succeeded", wantEvent: "state_changed"},
		{name: "process error retries", processErr: errors.New("provider failed"), wantStatus: "retrying", wantEvent: "retry_scheduled", wantCat: "runtime_error"},
		{name: "timeout retries", mutate: func(run *recordmodel.Record) { run.Data["timeout_seconds"] = 5 }, wantStatus: "retrying", wantEvent: "retry_scheduled", wantCat: "timeout"},
		{name: "workflow failure retries", executions: []workflowmodel.WorkflowExecution{{ID: "execution-1", Status: "failed"}}, wantStatus: "retrying", wantEvent: "retry_scheduled", wantCat: "workflow_failure"},
		{name: "dead letter", mutate: func(run *recordmodel.Record) { run.Data["attempt"] = 3 }, processErr: errors.New("terminal"), wantStatus: "dead_letter", wantEvent: "dead_lettered", wantCat: "runtime_error"},
		{name: "unlimited attempts retry", mutate: func(run *recordmodel.Record) { run.Data["max_attempts"] = 0 }, processErr: errors.New("retry"), wantStatus: "retrying", wantEvent: "retry_scheduled", wantCat: "runtime_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := schedulerLeasedRun("run-" + strings.ReplaceAll(test.name, " ", "-"))
			if test.mutate != nil {
				test.mutate(&run)
			}
			repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
				if object.Key == "job_dead_letter" {
					return recordmodel.Record{}, false, nil
				}
				return recordmodel.Record{}, false, nil
			}}
			service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, repository, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
			if err := service.FinishRun(t.Context(), run, test.executions, test.processErr, started, schedulerRuntimeScope()); err != nil {
				t.Fatal(err)
			}
			if len(repository.committed) != 1 || len(repository.committed[0]) < 2 {
				t.Fatalf("commits = %+v", repository.committed)
			}
			finished := repository.committed[0][0].Record
			if finished.Data["status"] != test.wantStatus || finished.Data["error_category"] != test.wantCat {
				t.Fatalf("finished = %+v, want status=%q category=%q", finished.Data, test.wantStatus, test.wantCat)
			}
			firstEvent := repository.committed[0][len(repository.committed[0])-len(schedulerFinishEvents(finished, test.executions, nil, test.wantStatus, "", now))]
			if firstEvent.Object.Key != "job_run_event" || firstEvent.Record.Data["event_type"] != test.wantEvent {
				t.Fatalf("finish event = %+v", firstEvent)
			}
			if test.wantStatus == "retrying" && strings.TrimSpace(finished.Data["next_retry_at"].(string)) == "" {
				t.Fatal("retry did not schedule next attempt")
			}
			if test.wantStatus == "dead_letter" && repository.committed[0][1].Object.Key != "job_dead_letter" {
				t.Fatalf("dead-letter commit missing: %+v", repository.committed[0])
			}
		})
	}
}

func TestSchedulerFinishRunScopeAndCommitFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)
	run := schedulerLeasedRun("run-1")
	service := NewSchedulerApplicationServiceWithWorker(schedulerTestSchema(), nil, &schedulerRepositoryFake{}, nil, workerplatform.Dependencies{Clock: schedulerFixedClock{now: now}})
	if err := service.FinishRun(t.Context(), run, nil, nil, now, principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("scope error = %v", err)
	}

	wantErr := errors.New("commit failed")
	repository := &schedulerRepositoryFake{commit: func(context.Context, string, []transactionmodel.RecordMutationCommit) error { return wantErr }}
	service.repository = repository
	if err := service.FinishRun(t.Context(), schedulerLeasedRun("run-2"), nil, nil, now, schedulerRuntimeScope()); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("commit error = %v", err)
	}

	repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error {
		return mutation.MutationConflict("job_run", "run-3", mutation.MutationConflictOptimistic, wantErr)
	}
	if err := service.FinishRun(t.Context(), schedulerLeasedRun("run-3"), nil, nil, now, schedulerRuntimeScope()); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("optimistic conflict = %v", err)
	}
}

func TestSchedulerFinishEventAndRetryHelpers(t *testing.T) {
	now := time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)
	executions := []workflowmodel.WorkflowExecution{
		{},
		{ID: "duplicate", Status: "duplicate"},
		{ID: "one", WorkflowKey: "wf", Status: "succeeded"},
		{ID: "two", WorkflowKey: "wf", Status: "blocked", ActionType: "record.update"},
	}
	if !schedulerExecutionsFailed(executions) || schedulerExecutionsFailed([]workflowmodel.WorkflowExecution{{Status: "succeeded"}}) {
		t.Fatal("execution failure classification mismatch")
	}
	if got := schedulerFirstExecutionID(executions); got != "duplicate" || schedulerFirstExecutionID(nil) != "" {
		t.Fatalf("first execution = %q", got)
	}
	if got := schedulerRunResultJSON(executions, []schedulerBusinessEvidence{{}}, "failed"); !strings.Contains(got, `"workflow_execution_count":4`) || !strings.Contains(got, `"business_evidence_count":1`) {
		t.Fatalf("result JSON = %q", got)
	}

	run := recordmodel.Record{Data: map[string]any{}}
	if got := schedulerNextRetryAt(run, 1, now); !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("default retry = %v", got)
	}
	run.Data["retry_backoff_mode"], run.Data["retry_delay_seconds"], run.Data["retry_max_delay_seconds"] = "exponential", 10, 25
	if got := schedulerNextRetryAt(run, 0, now); !got.Equal(now.Add(10 * time.Second)) {
		t.Fatalf("negative exponent retry = %v", got)
	}
	if got := schedulerNextRetryAt(run, 4, now); !got.Equal(now.Add(25 * time.Second)) {
		t.Fatalf("capped exponential retry = %v", got)
	}
	run.Data["retry_backoff"], run.Data["retry_delay_seconds"], run.Data["retry_max_delay_seconds"] = "capped_exponential", -1, -1
	if got := schedulerNextRetryAt(run, 8, now); !got.Equal(now.Add(3600 * time.Second)) {
		t.Fatalf("normalized capped retry = %v", got)
	}
	run.Data["retry_backoff"] = "unknown"
	if got := schedulerNextRetryAt(run, 2, now); !got.Equal(now.Add(time.Minute)) {
		t.Fatalf("unknown backoff retry = %v", got)
	}

	if schedulerErrorCategory(nil) != "" || schedulerErrorCategory(errors.New("deadline exceeded")) != "timeout" || schedulerErrorCategory(errors.New("plain")) != "runtime_error" {
		t.Fatal("plain scheduler error category mismatch")
	}
	for _, test := range []struct{ code, want string }{
		{"provider.timeout", "timeout"}, {"backend.permission.denied", "permission"}, {"backend.forbidden", "permission"},
		{"backend.validation.failed", "validation"}, {"backend.bad_request", "validation"}, {"provider.failed", "provider.failed"},
	} {
		if got := schedulerErrorCategory(&apperror.AppError{Code: test.code}); got != test.want {
			t.Fatalf("category(%q) = %q, want %q", test.code, got, test.want)
		}
	}

	events := schedulerFinishEvents(recordmodel.Record{ID: "run-1", Data: map[string]any{}}, executions, []schedulerBusinessEvidence{{ObjectKey: "report", RecordID: "report-1"}, {Kind: "export_created"}}, "retrying", "retry", now)
	if len(events) != 6 || events[0].Data["event_type"] != "retry_scheduled" || events[len(events)-1].Data["event_type"] != "export_created" {
		t.Fatalf("events = %+v", events)
	}
	if got := schedulerFinishEvents(recordmodel.Record{ID: "run", Data: map[string]any{}}, nil, nil, "dead_letter", "dead", now); got[0].Data["event_type"] != "dead_lettered" {
		t.Fatalf("dead-letter event = %+v", got)
	}
	dead := schedulerDeadLetterRecord(recordmodel.Record{ID: "run", Data: map[string]any{"scheduler_definition_key": "definition", "error_message": "failed"}}, "dead", "", now)
	if dead.Data["reason"] != "backend.scheduler.dead_letter" || dead.Data["last_error"] != "failed" {
		t.Fatalf("dead letter = %+v", dead)
	}
}

func TestSchedulerLegacyEventAppendHelpers(t *testing.T) {
	now := time.Date(2026, 7, 20, 4, 0, 0, 0, time.UTC)
	service := schedulerPersistenceService(&schedulerRepositoryFake{}, now)
	inserted := []recordmodel.Record{}
	service.insertRecord = func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, record recordmodel.Record, _ string) error {
		inserted = append(inserted, record)
		return nil
	}
	run := recordmodel.Record{ID: "run-1", Data: map[string]any{}}
	for _, status := range []string{"succeeded", "retrying", "dead_letter"} {
		if err := service.appendStateEvent(t.Context(), run, nil, status, "message", now); err != nil {
			t.Fatal(err)
		}
	}
	executions := []workflowmodel.WorkflowExecution{{}, {ID: "dup", Status: "duplicate"}, {ID: "one", Status: "succeeded"}, {ID: "two", Status: "succeeded", ActionType: "record.update"}}
	if err := service.appendWorkflowEvents(t.Context(), run, executions, now); err != nil {
		t.Fatal(err)
	}
	if err := service.appendBusinessEvidenceEvents(t.Context(), run, []schedulerBusinessEvidence{{ObjectKey: "report", RecordID: "one"}, {Kind: "export_created"}}, now); err != nil {
		t.Fatal(err)
	}
	if len(inserted) != 8 {
		t.Fatalf("inserted events = %d, %+v", len(inserted), inserted)
	}

	wantErr := errors.New("event insert failed")
	calls := 0
	service.insertRecord = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error {
		calls++
		if calls == 2 {
			return wantErr
		}
		return nil
	}
	if err := service.appendWorkflowEvents(t.Context(), run, []workflowmodel.WorkflowExecution{{ID: "one"}, {ID: "two"}}, now); !errors.Is(err, wantErr) {
		t.Fatalf("workflow event error = %v", err)
	}
	calls = 0
	if err := service.appendBusinessEvidenceEvents(t.Context(), run, []schedulerBusinessEvidence{{}, {}}, now); !errors.Is(err, wantErr) {
		t.Fatalf("business event error = %v", err)
	}
}

func TestSchedulerPrepareDefinitionCursorCommitMatrix(t *testing.T) {
	now := time.Date(2026, 7, 20, 5, 0, 0, 0, time.UTC)
	run := recordmodel.Record{ID: "run-1", Data: map[string]any{"scheduler_definition_key": "definition-1", "scheduled_for": now.Add(-time.Minute).Format(time.RFC3339)}}
	wantErr := errors.New("definition read failed")
	repository := &schedulerRepositoryFake{get: func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, wantErr
	}}
	service := schedulerPersistenceService(repository, now)
	if _, err := service.prepareDefinitionCursorCommits(t.Context(), "workspace-a", run, "succeeded", now); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("definition error = %v", err)
	}
	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, nil
	}
	if commits, err := service.prepareDefinitionCursorCommits(t.Context(), "workspace-a", run, "succeeded", now); err != nil || commits != nil {
		t.Fatalf("missing definition commits = %+v, %v", commits, err)
	}
	definition := recordmodel.Record{ID: "definition-1", UpdatedAt: "old", Data: map[string]any{"schedule_type": "interval", "interval_seconds": 60, "next_run_at": now.Add(-time.Minute).Format(time.RFC3339), "missed_window_policy": "catch_up_bounded"}}
	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return definition, true, nil
	}
	commits, err := service.prepareDefinitionCursorCommits(t.Context(), "workspace-a", run, "succeeded", now)
	if err != nil || len(commits) != 2 || commits[0].Optimistic.ExpectedUpdatedAt != "old" || commits[0].Record.Data["last_run_status"] != "succeeded" || commits[1].Object.Key != "job_run_event" {
		t.Fatalf("cursor commits = %+v, %v", commits, err)
	}

	partial := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "job_definition"}}}}
	service.schema = partial
	if _, err := service.prepareDefinitionCursorCommits(t.Context(), "workspace-a", run, "succeeded", now); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
		t.Fatalf("missing event object error = %v", err)
	}
}

func TestSchedulerCommitFinishedRunFailureWindows(t *testing.T) {
	now := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	run := schedulerLeasedRun("run-1")
	run.Data["status"] = "dead_letter"
	wantErr := errors.New("read failed")
	repository := &schedulerRepositoryFake{get: func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
		if object.Key == "job_dead_letter" {
			return recordmodel.Record{}, false, wantErr
		}
		return recordmodel.Record{}, false, nil
	}}
	service := schedulerPersistenceService(repository, now)
	if err := service.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, run, nil, nil, "dead_letter", "failed", now, nil); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("dead-letter read error = %v", err)
	}

	repository.get = func(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ string) (recordmodel.Record, bool, error) {
		if object.Key == "job_dead_letter" {
			return recordmodel.Record{ID: "existing"}, true, nil
		}
		return recordmodel.Record{}, false, nil
	}
	if err := service.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, run, nil, nil, "dead_letter", "failed", now, nil); err != nil {
		t.Fatalf("existing dead-letter commit = %v", err)
	}
	if len(repository.committed) != 1 || len(repository.committed[0]) != 2 {
		t.Fatalf("existing dead-letter commits = %+v", repository.committed)
	}

	partial := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "job_definition"}}}}
	service.schema = partial
	if err := service.commitFinishedRun(t.Context(), "workspace-a", definitionmodel.ObjectSchema{Key: "job_run"}, schedulerLeasedRun("run-2"), nil, nil, "succeeded", "done", now, nil); apperror.CodeOf(err) != "backend.scheduler.runtime_object_not_found" {
		t.Fatalf("missing event object error = %v", err)
	}
}
