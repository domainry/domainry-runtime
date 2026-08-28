package record

import (
	"context"
	"errors"
	"testing"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordBatchJobFailureQuarantineAndRequeueLifecycle(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	now := time.Now().UTC()
	enqueue := func(key string) recordmodel.RecordBatchJob {
		job, replayed, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{WorkspaceID: " workspace-a ", Kind: " export ", ObjectKey: " customer ", IdempotencyKey: " " + key + " ", PayloadJSON: `{}`})
		if err != nil || replayed {
			t.Fatalf("enqueue %s job=%#v replayed=%v error=%v", key, job, replayed, err)
		}
		return job
	}
	failed := enqueue("failed")
	claimed, err := repository.ClaimRecordBatchJobs(t.Context(), 0, " worker-a ", 0, now)
	if err != nil || len(claimed) != 1 || claimed[0].ID != failed.ID {
		t.Fatalf("claim=%#v error=%v", claimed, err)
	}
	failed = claimed[0]
	failed.ErrorCode = "backend.failed"
	if err := repository.FailRecordBatchJob(t.Context(), failed, now); err != nil {
		t.Fatal(err)
	}
	requeued, changed, err := repository.RequeueRecordBatchJob(t.Context(), " workspace-a ", " "+failed.ID+" ", now.Add(time.Second))
	if err != nil || !changed || requeued.Status != "queued" || requeued.FencingToken != failed.FencingToken+1 {
		t.Fatalf("requeued=%#v changed=%v error=%v", requeued, changed, err)
	}
	claimed, err = repository.ClaimRecordBatchJobs(t.Context(), 101, "worker-b", time.Second, now.Add(2*time.Second))
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim=%#v error=%v", claimed, err)
	}
	quarantined := claimed[0]
	quarantined.ErrorCode = "backend.poison"
	if err := repository.QuarantineRecordBatchJob(t.Context(), quarantined, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if value, found, err := repository.GetRecordBatchJob(t.Context(), "workspace-a", quarantined.ID); err != nil || !found || value.Status != "quarantined" || value.LeaseOwner != "" {
		t.Fatalf("quarantined=%#v found=%v error=%v", value, found, err)
	}
	if _, changed, err := repository.RequeueRecordBatchJob(t.Context(), "workspace-a", quarantined.ID, now.Add(4*time.Second)); err != nil || !changed {
		t.Fatalf("quarantine requeue changed=%v error=%v", changed, err)
	}

	queued := enqueue("queued")
	if value, changed, err := repository.RequeueRecordBatchJob(t.Context(), "workspace-a", queued.ID, now); err != nil || changed || value.ID != queued.ID {
		t.Fatalf("queued requeue value=%#v changed=%v error=%v", value, changed, err)
	}
	if _, changed, err := repository.RequeueRecordBatchJob(t.Context(), "workspace-a", "missing", now); err != nil || changed {
		t.Fatalf("missing requeue changed=%v error=%v", changed, err)
	}
	if _, found, err := repository.CancelRecordBatchJob(t.Context(), "workspace-a", "missing"); err != nil || found {
		t.Fatalf("missing cancel found=%v error=%v", found, err)
	}
}

func TestRecordBatchJobInputLeaseChunkAndStatsEdges(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := repository.EnqueueRecordBatchJob(canceled, recordmodel.RecordBatchJob{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled enqueue = %v", err)
	}
	for _, job := range []recordmodel.RecordBatchJob{
		{Kind: "export", ObjectKey: "customer", IdempotencyKey: "key"},
		{WorkspaceID: "workspace-a", Kind: "export", ObjectKey: "customer"},
	} {
		if _, _, err := repository.EnqueueRecordBatchJob(t.Context(), job); err == nil {
			t.Fatalf("invalid job accepted: %#v", job)
		}
	}
	for _, test := range []struct {
		workspace string
		id        string
		now       time.Time
	}{
		{workspace: "", id: "id", now: time.Now()},
		{workspace: "workspace", id: "", now: time.Now()},
		{workspace: "workspace", id: "id"},
	} {
		if _, _, err := repository.RequeueRecordBatchJob(t.Context(), test.workspace, test.id, test.now); err == nil {
			t.Fatalf("invalid requeue accepted: %#v", test)
		}
	}
	if _, err := repository.ClaimRecordBatchJobs(t.Context(), 1, "", time.Minute, time.Now()); err == nil {
		t.Fatal("blank worker accepted")
	}
	if _, err := repository.ClaimRecordBatchJobs(t.Context(), 1, "worker", time.Minute, time.Time{}); err == nil {
		t.Fatal("zero clock accepted")
	}
	if _, _, err := repository.GetRecordBatchJob(t.Context(), "workspace-a", "missing"); err != nil {
		t.Fatalf("missing get: %v", err)
	}
	if _, found, err := repository.FindRecordBatchJobByIdempotency(t.Context(), "workspace-a", "export", "customer", "missing"); err != nil || found {
		t.Fatalf("missing find found=%v error=%v", found, err)
	}
	if _, _, err := repository.RecordBatchJobQueueStats(t.Context(), " "); err == nil {
		t.Fatal("blank workspace stats accepted")
	}
	if _, _, err := repository.GlobalRecordBatchJobQueueStats(t.Context(), principalmodel.SystemScope{}); err == nil {
		t.Fatal("untrusted global scope accepted")
	}
	if depth, age, err := repository.RecordBatchJobQueueStats(t.Context(), "workspace-empty"); err != nil || depth != 0 || age != 0 {
		t.Fatalf("empty stats depth=%d age=%v error=%v", depth, age, err)
	}

	job, _, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{ID: "explicit-job", WorkspaceID: "workspace-a", Kind: "export", ObjectKey: "customer", IdempotencyKey: "lease", PayloadJSON: `{}`})
	if err != nil || job.ID != "explicit-job" {
		t.Fatalf("explicit job=%#v error=%v", job, err)
	}
	claimed, err := repository.ClaimRecordBatchJobs(t.Context(), 1, "worker", time.Minute, time.Now())
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%#v error=%v", claimed, err)
	}
	job = claimed[0]
	stale := job
	stale.FencingToken++
	if err := repository.HeartbeatRecordBatchJob(t.Context(), stale, 0, time.Now()); err == nil {
		t.Fatal("stale heartbeat accepted")
	}
	if err := repository.RetryRecordBatchJob(t.Context(), stale, time.Now(), time.Now()); err == nil {
		t.Fatal("stale retry accepted")
	}
	if err := repository.SaveRecordBatchJobCheckpoint(t.Context(), stale, time.Now()); err == nil {
		t.Fatal("stale checkpoint accepted")
	}
	if err := repository.ReplaceRecordBatchJobChunks(t.Context(), stale, nil, time.Now()); err == nil {
		t.Fatal("stale chunk replacement accepted")
	}
	if err := repository.ReplaceRecordBatchJobChunks(t.Context(), job, nil, time.Now()); err != nil {
		t.Fatalf("empty chunk replacement: %v", err)
	}
	if chunks, err := repository.ListRecordBatchJobChunks(t.Context(), "workspace-a", job.ID); err != nil || len(chunks) != 0 {
		t.Fatalf("chunks=%#v error=%v", chunks, err)
	}
}
