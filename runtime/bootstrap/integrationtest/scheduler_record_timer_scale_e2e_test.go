package integrationtest

import (
	"fmt"
	"os"
	"testing"
	"time"

	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

func TestRecordTimerHundredThousandRestartClockDriftAndMultiInstanceNoLoss(t *testing.T) {
	if os.Getenv("RUNTIME_SCALE_E2E") != "1" {
		t.Skip("set RUNTIME_SCALE_E2E=1 to run the 100k durable timer acceptance")
	}
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatal(err)
	}
	timerObject := objects[4]
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	const total = 100_000
	for start := 0; start < total; start += 1000 {
		commits := make([]transactionmodel.RecordMutationCommit, 0, 1000)
		for index := start; index < start+1000 && index < total; index++ {
			id := fmt.Sprintf("scale-timer-%06d", index)
			stamp := now.Add(-time.Hour).Format(time.RFC3339Nano)
			commits = append(commits, transactionmodel.RecordMutationCommit{Operation: "create", Object: timerObject, Record: recordmodel.Record{ID: id, CreatedAt: stamp, UpdatedAt: stamp, Data: map[string]any{
				"timer_key": id, "object_key": "scale_record", "record_id": fmt.Sprintf("record-%06d", index), "purpose": "scale",
				"status": "scheduled", "schedule_mode": "absolute", "due_at": stamp, "timezone": "UTC",
				"target_type": "action", "target_key": "scale.process", "payload_json": "{}", "priority": index % 7, "sequence": index,
				"lease_owner": "", "lease_expires_at": "", "fencing_token": 0, "attempt": 0,
				"max_attempts": 10, "retry_delay_seconds": 1, "retry_max_delay_seconds": 60,
			}}})
		}
		if err := recordLegacyStore(store).CommitRecordMutationBatch(t.Context(), "default", commits); err != nil {
			t.Fatalf("seed timer batch %d: %v", start, err)
		}
	}
	preRestart := newSchedulerRuntimeTestService(t, store, objects)
	preRestart.Applications().Scheduler.ConfigureWorker(schedulerapplication.WorkerConfig{Enabled: true, LeaseTTL: 5 * time.Minute, BatchSize: 500})
	lostLeases, err := preRestart.Applications().Scheduler.ClaimDueRecordTimers(t.Context(), "default", now.Add(2*time.Minute), 250, schedulerRuntimeSystemScope())
	if err != nil || len(lostLeases) != 250 {
		t.Fatalf("inject lease loss leases=%d err=%v", len(lostLeases), err)
	}
	// Drop the owning service without completion, then reconstruct every worker
	// to model a process restart. The replacement workers use a clock eight
	// minutes ahead of the lost owner and must reclaim those fenced leases.
	preRestart = nil
	instances := make([]*RuntimeServices, 4)
	for index := range instances {
		instances[index] = newSchedulerRuntimeTestService(t, store, objects)
		instances[index].Applications().Scheduler.ConfigureWorker(schedulerapplication.WorkerConfig{Enabled: true, LeaseTTL: 5 * time.Minute, BatchSize: 500})
	}
	seen := make(map[string]bool, total)
	batch := 0
	for len(seen) < total {
		instance := instances[batch%len(instances)]
		drift := time.Duration(batch%5-2) * time.Second
		leases, err := instance.Applications().Scheduler.ClaimDueRecordTimers(t.Context(), "default", now.Add(10*time.Minute).Add(drift), 500, schedulerRuntimeSystemScope())
		if err != nil {
			t.Fatalf("claim batch %d: %v", batch, err)
		}
		if len(leases) == 0 {
			t.Fatalf("timer drain stopped after %d/%d", len(seen), total)
		}
		for _, lease := range leases {
			if seen[lease.Record.ID] {
				t.Fatalf("duplicate business mutation for timer %s", lease.Record.ID)
			}
			seen[lease.Record.ID] = true
		}
		if err := instance.Applications().Scheduler.FinishRecordTimers(t.Context(), "default", leases, now.Add(10*time.Minute).Add(drift), schedulerRuntimeSystemScope()); err != nil {
			t.Fatalf("finish timer batch %d: %v", batch, err)
		}
		batch++
	}
	page, err := recordLegacyStore(store).ListRecords(t.Context(), "default", timerObject, recordmodel.RecordListQuery{Page: 1, PageSize: 1, Filters: map[string]any{"status": "fired"}})
	if err != nil || page.Total != total {
		t.Fatalf("fired timers=%d err=%v want=%d", page.Total, err, total)
	}
	if err := instances[0].Applications().Scheduler.FinishRecordTimer(t.Context(), "default", lostLeases[0], now.Add(-2*time.Minute), schedulerRuntimeSystemScope()); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("lost worker stale completion error=%v", err)
	}
}
