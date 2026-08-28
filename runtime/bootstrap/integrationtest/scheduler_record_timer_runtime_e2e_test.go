package integrationtest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type recordTimerWorkspaceSchema struct {
	snapshot metadatamodel.MetadataSchemaSnapshot
}

func (s recordTimerWorkspaceSchema) SchemaForPrincipal(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
	return s.snapshot
}

type recordTimerWorkspaceRuntime struct {
	mu         sync.Mutex
	workspaces []string
	failures   map[string]error
}

func (*recordTimerWorkspaceRuntime) ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return workflowmodel.WorkflowProcessResult{}, nil
}

func (r *recordTimerWorkspaceRuntime) ExecuteRecordTimer(_ context.Context, execution schedulerapplication.RecordTimerExecution, _ principalmodel.Principal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workspaces = append(r.workspaces, execution.WorkspaceID)
	if err := r.failures[execution.WorkspaceID]; err != nil {
		return err
	}
	return nil
}

func TestRecordTimerCommitsAtomicallyAndClaimsInStableOrderWithFencing(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := append(schedulerRuntimeTestObjects(), definitionmodel.ObjectSchema{Key: "service_request", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "select"}}})
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatal(err)
	}
	service := newSchedulerRuntimeTestService(t, store, objects).Applications().Scheduler
	service.ConfigureWorker(schedulerapplication.WorkerConfig{Enabled: true, LeaseTTL: time.Minute, BatchSize: 25})
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	source := objects[len(objects)-1]
	sourceRecord := recordmodel.Record{ID: "request-1", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"status": "open"}}
	timerCommit, err := service.BuildRecordTimerMutation(t.Context(), "default", schedulerapplication.RecordTimerSchedule{
		TimerKey: "response-deadline", ObjectKey: source.Key, RecordID: sourceRecord.ID, Purpose: "response", DueAt: now.Add(time.Minute), Timezone: "UTC", TargetType: "action", TargetKey: "request.escalate", Priority: 10, Sequence: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordLegacyStore(store).CommitRecordMutationBatch(t.Context(), "default", []transactionmodel.RecordMutationCommit{{Operation: "create", Object: source, Record: sourceRecord}, timerCommit}); err != nil {
		t.Fatalf("atomic source and timer commit: %v", err)
	}
	if _, found, err := recordLegacyStore(store).GetRecord(t.Context(), "default", timerCommit.Object, timerCommit.Record.ID); err != nil || !found {
		t.Fatalf("durable timer missing: found=%v err=%v", found, err)
	}
	supersede, err := service.BuildRecordTimerSupersedeMutations(t.Context(), "default", timerCommit.Record, schedulerapplication.RecordTimerSchedule{
		TimerKey: "response-deadline-v2", ObjectKey: source.Key, RecordID: sourceRecord.ID, Purpose: "response", DueAt: now.Add(2 * time.Minute), TargetType: "action", TargetKey: "request.escalate", Priority: 10, Sequence: 2,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := recordLegacyStore(store).CommitRecordMutationBatch(t.Context(), "default", supersede); err != nil {
		t.Fatalf("supersede timer atomically: %v", err)
	}
	oldTimer, _, _ := recordLegacyStore(store).GetRecord(t.Context(), "default", timerCommit.Object, timerCommit.Record.ID)
	newTimer, found, _ := recordLegacyStore(store).GetRecord(t.Context(), "default", supersede[1].Object, supersede[1].Record.ID)
	if oldTimer.Data["status"] != "superseded" || !found || newTimer.Data["supersedes_timer_id"] != oldTimer.ID {
		t.Fatalf("timer supersede graph old=%#v new=%#v", oldTimer, newTimer)
	}
	cancelled, err := service.CancelRecordTimers(t.Context(), "default", source.Key, sourceRecord.ID, "response", now.Add(2*time.Second), schedulerRuntimeSystemScope())
	if err != nil || cancelled != 1 {
		t.Fatalf("cancel future source timers count=%d err=%v", cancelled, err)
	}
	newTimer, _, _ = recordLegacyStore(store).GetRecord(t.Context(), "default", supersede[1].Object, supersede[1].Record.ID)
	if newTimer.Data["status"] != "cancelled" {
		t.Fatalf("future timer not cancelled after source completion: %#v", newTimer)
	}

	failedSource := sourceRecord
	failedSource.Data = map[string]any{"status": "changed"}
	failedSource.UpdatedAt = now.Add(time.Second).Format(time.RFC3339Nano)
	if err := recordLegacyStore(store).CommitRecordMutationBatch(t.Context(), "default", []transactionmodel.RecordMutationCommit{{Operation: "update", Object: source, Record: failedSource, ExpectedUpdatedAt: sourceRecord.UpdatedAt}, timerCommit}); err == nil {
		t.Fatal("duplicate timer should fail the whole source mutation batch")
	}
	current, _, _ := recordLegacyStore(store).GetRecord(t.Context(), "default", source, sourceRecord.ID)
	if current.Data["status"] != "open" {
		t.Fatalf("source mutation survived timer failure: %#v", current)
	}

	for _, request := range []schedulerapplication.RecordTimerSchedule{
		{TimerKey: "low", ObjectKey: source.Key, RecordID: "request-low", Purpose: "notify", DueAt: now, TargetType: "workflow", TargetKey: "notify", Priority: 1, Sequence: 1},
		{TimerKey: "high-later", ObjectKey: source.Key, RecordID: "request-high-later", Purpose: "notify", DueAt: now, TargetType: "workflow", TargetKey: "notify", Priority: 20, Sequence: 2},
		{TimerKey: "high-first", ObjectKey: source.Key, RecordID: "request-high-first", Purpose: "notify", DueAt: now, TargetType: "workflow", TargetKey: "notify", Priority: 20, Sequence: 1},
	} {
		commit, buildErr := service.BuildRecordTimerMutation(t.Context(), "default", request, now)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		if err := recordLegacyStore(store).CommitRecordMutationBatch(t.Context(), "default", []transactionmodel.RecordMutationCommit{commit}); err != nil {
			t.Fatal(err)
		}
	}
	leases, err := service.ClaimDueRecordTimers(t.Context(), "default", now, 3, schedulerRuntimeSystemScope())
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 3 || leases[0].Record.Data["timer_key"] != "high-first" || leases[1].Record.Data["timer_key"] != "high-later" || leases[2].Record.Data["timer_key"] != "low" {
		t.Fatalf("ordered timer leases = %#v", leases)
	}
	if err := service.FinishRecordTimer(t.Context(), "default", leases[0], now.Add(time.Second), schedulerRuntimeSystemScope()); err != nil {
		t.Fatal(err)
	}

	reclaimed, err := newSchedulerRuntimeTestService(t, store, objects).Applications().Scheduler.ClaimDueRecordTimers(t.Context(), "default", now.Add(2*time.Minute), 1, schedulerRuntimeSystemScope())
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("expired timer reclaim = %#v err=%v", reclaimed, err)
	}
	if err := service.FinishRecordTimer(t.Context(), "default", leases[1], now.Add(2*time.Minute), schedulerRuntimeSystemScope()); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("stale timer completion error = %v", err)
	}
	if err := service.FinishRecordTimer(t.Context(), "default", reclaimed[0], now.Add(2*time.Minute), schedulerRuntimeSystemScope()); err != nil {
		t.Fatalf("reclaimed timer completion: %v", err)
	}
}

func TestRecordTimerConcurrentWorkersClaimOneTimerOnce(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	seed := newSchedulerRuntimeTestService(t, store, objects).Applications().Scheduler
	commit, err := seed.BuildRecordTimerMutation(t.Context(), "default", schedulerapplication.RecordTimerSchedule{TimerKey: "once", ObjectKey: "record", RecordID: "one", Purpose: "fire", DueAt: now, TargetType: "action", TargetKey: "record.fire"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordLegacyStore(store).CommitRecordMutationBatch(t.Context(), "default", []transactionmodel.RecordMutationCommit{commit}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan int, 100)
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			service := newSchedulerRuntimeTestService(t, store, objects).Applications().Scheduler
			leases, claimErr := service.ClaimDueRecordTimers(t.Context(), "default", now, 1, schedulerRuntimeSystemScope())
			if claimErr != nil {
				t.Errorf("claim timer: %v", claimErr)
				return
			}
			results <- len(leases)
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	claimed := 0
	for count := range results {
		claimed += count
	}
	if claimed != 1 {
		t.Fatalf("timer claimed %d times", claimed)
	}
}

func TestRecordTimerCancellationDrainsEveryPage(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	service := newSchedulerRuntimeTestService(t, store, objects).Applications().Scheduler
	commits := make([]transactionmodel.RecordMutationCommit, 0, 501)
	for index := range 501 {
		commit, err := service.BuildRecordTimerMutation(t.Context(), "default", schedulerapplication.RecordTimerSchedule{
			TimerKey: fmt.Sprintf("future-%03d", index), ObjectKey: "source", RecordID: "source-1", Purpose: "future",
			DueAt: now.Add(time.Duration(index+1) * time.Minute), TargetType: "action", TargetKey: "source.remind", Sequence: int64(index),
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		commits = append(commits, commit)
	}
	if err := recordLegacyStore(store).CommitRecordMutationBatch(t.Context(), "default", commits); err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.CancelRecordTimers(t.Context(), "default", "source", "source-1", "future", now, schedulerRuntimeSystemScope())
	if err != nil || cancelled != 501 {
		t.Fatalf("cancelled=%d err=%v", cancelled, err)
	}
	page, err := recordLegacyStore(store).ListRecords(t.Context(), "default", objects[4], recordmodel.RecordListQuery{Page: 1, PageSize: 1, Filters: map[string]any{"object_key": "source", "record_id": "source-1", "status": "scheduled"}})
	if err != nil || page.Total != 0 {
		t.Fatalf("scheduled timers after cancellation=%d err=%v", page.Total, err)
	}
}

func TestRecordTimerFailureReleasesEntireClaimBatchAndStopsAtAttemptLimit(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	service := newSchedulerRuntimeTestService(t, store, objects).Applications().Scheduler
	for index, maxAttempts := range []int{1, 2} {
		if _, err := service.ScheduleRecordTimer(t.Context(), "default", schedulerapplication.RecordTimerSchedule{
			TimerKey: fmt.Sprintf("failure-%d", index), ObjectKey: "service_request", RecordID: fmt.Sprintf("request-%d", index), Purpose: "failure_injection",
			DueAt: now, TargetType: "action", TargetKey: "missing.action", Sequence: int64(index), MaxAttempts: maxAttempts,
		}, recordmodel.Record{}, nil, now, schedulerRuntimeSystemScope()); err != nil {
			t.Fatal(err)
		}
	}
	if processed, err := service.ProcessDueRecordTimers(t.Context(), "default", now, 10, schedulerRuntimePrincipal(), schedulerRuntimeSystemScope()); err == nil || processed != 0 {
		t.Fatalf("first failed batch processed=%d err=%v", processed, err)
	}
	page, err := recordLegacyStore(store).ListRecords(t.Context(), "default", objects[4], recordmodel.RecordListQuery{Page: 1, PageSize: 10, Sort: []recordmodel.RecordSortRule{{Field: "sequence", Direction: "asc"}}})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("failed timers=%#v err=%v", page, err)
	}
	terminalLeaseOwner, terminalHasLeaseOwner := page.Items[0].Data["lease_owner"]
	if page.Items[0].Data["status"] != "failed" || fmt.Sprint(page.Items[0].Data["failed_at"]) == "" || terminalHasLeaseOwner && fmt.Sprint(terminalLeaseOwner) != "" {
		t.Fatalf("terminal failure timer=%#v", page.Items[0])
	}
	retryLeaseOwner, retryHasLeaseOwner := page.Items[1].Data["lease_owner"]
	if page.Items[1].Data["status"] != "scheduled" || fmt.Sprint(page.Items[1].Data["last_error"]) == "" || retryHasLeaseOwner && fmt.Sprint(retryLeaseOwner) != "" {
		t.Fatalf("retryable failure timer=%#v", page.Items[1])
	}
	if processed, err := service.ProcessDueRecordTimers(t.Context(), "default", now.Add(2*time.Second), 10, schedulerRuntimePrincipal(), schedulerRuntimeSystemScope()); err == nil || processed != 0 {
		t.Fatalf("second failed attempt processed=%d err=%v", processed, err)
	}
	retried, found, err := recordLegacyStore(store).GetRecord(t.Context(), "default", objects[4], page.Items[1].ID)
	if err != nil || !found || retried.Data["status"] != "failed" || fmt.Sprint(retried.Data["attempt"]) != "2" {
		t.Fatalf("exhausted retry timer=%#v found=%v err=%v", retried, found, err)
	}
}

func TestRecordTimerWorkerProcessesEveryWorkspace(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	runtime := &recordTimerWorkspaceRuntime{}
	service := schedulerapplication.NewSchedulerApplicationServiceWithWorker(
		recordTimerWorkspaceSchema{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: objects}}, runtime, recordLegacyStore(store), nil,
		workerplatform.Dependencies{WorkerID: workerplatform.WorkerID("record-timer-workspace-worker")},
	)
	for _, workspaceID := range []string{"tenant-a", "tenant-b"} {
		if _, err := service.ScheduleRecordTimer(t.Context(), workspaceID, schedulerapplication.RecordTimerSchedule{
			TimerKey: "workspace-timer", ObjectKey: "service_request", RecordID: "request-1", Purpose: "notify",
			DueAt: now, TargetType: "action", TargetKey: "request.notify",
		}, recordmodel.Record{}, nil, now, schedulerRuntimeSystemScope()); err != nil {
			t.Fatal(err)
		}
	}
	processed, err := service.ProcessDueRecordTimersForAllWorkspaces(t.Context(), now, 10, schedulerRuntimePrincipal(), schedulerRuntimeSystemScope())
	if err != nil || processed != 2 {
		t.Fatalf("multi-workspace timers processed=%d err=%v", processed, err)
	}
	runtime.mu.Lock()
	workspaces := append([]string(nil), runtime.workspaces...)
	runtime.mu.Unlock()
	if fmt.Sprint(workspaces) != "[tenant-a tenant-b]" {
		t.Fatalf("timer workspaces=%v", workspaces)
	}
}

func TestRecordTimerWorkspaceFailureDoesNotStarveOtherTenantOrExceedGlobalBatch(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	runtime := &recordTimerWorkspaceRuntime{failures: map[string]error{"tenant-a": fmt.Errorf("tenant-a injected failure")}}
	service := schedulerapplication.NewSchedulerApplicationServiceWithWorker(
		recordTimerWorkspaceSchema{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: objects}}, runtime, recordLegacyStore(store), nil,
		workerplatform.Dependencies{WorkerID: workerplatform.WorkerID("record-timer-fair-worker")},
	)
	for _, workspaceID := range []string{"tenant-a", "tenant-b"} {
		for index := range 5 {
			if _, err := service.ScheduleRecordTimer(t.Context(), workspaceID, schedulerapplication.RecordTimerSchedule{
				TimerKey: fmt.Sprintf("workspace-timer-%d", index), ObjectKey: "service_request", RecordID: fmt.Sprintf("request-%d", index), Purpose: "notify",
				DueAt: now, TargetType: "action", TargetKey: "request.notify", Sequence: int64(index),
			}, recordmodel.Record{}, nil, now, schedulerRuntimeSystemScope()); err != nil {
				t.Fatal(err)
			}
		}
	}
	processed, err := service.ProcessDueRecordTimersForAllWorkspaces(t.Context(), now, 3, schedulerRuntimePrincipal(), schedulerRuntimeSystemScope())
	if err == nil || processed != 1 {
		t.Fatalf("fair failed workspace batch processed=%d err=%v", processed, err)
	}
	runtime.mu.Lock()
	workspaces := append([]string(nil), runtime.workspaces...)
	runtime.mu.Unlock()
	if len(workspaces) != 3 || workspaces[0] != "tenant-a" || workspaces[1] != "tenant-a" || workspaces[2] != "tenant-b" {
		t.Fatalf("global batch/fair workspace calls=%v", workspaces)
	}
}
