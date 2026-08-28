package record

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordBatchJobStoreAtomicallyReplaysCanonicalExportAcrossAuditIDs(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	const workers = 16
	jobs := make(chan recordmodel.RecordBatchJob, workers)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			job, _, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{
				WorkspaceID: "workspace-a", Kind: "report_export", ObjectKey: "ledger", IdempotencyKey: "canonical-export",
				PayloadJSON: `{"audit_id":"","report_key":"ledger","scope":{"purpose":"same"}}`, AuditID: fmt.Sprintf("audit-%d", index), ActorID: "requester",
			})
			if err != nil {
				errs <- err
				return
			}
			jobs <- job
		}(index)
	}
	wait.Wait()
	close(jobs)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for job := range jobs {
		ids[job.ID] = true
	}
	if len(ids) != 1 {
		t.Fatalf("canonical export created %d jobs: %#v", len(ids), ids)
	}
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM record_batch_jobs WHERE workspace_id = ? AND kind = ? AND object_key = ? AND idempotency_key = ?`, "workspace-a", "report_export", "ledger", "canonical-export").Scan(&count); err != nil || count != 1 {
		t.Fatalf("durable canonical jobs=%d err=%v", count, err)
	}
}

func TestRecordBatchJobStoreEnqueueClaimCheckpointChunksCancelAndFence(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	queued, replayed, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{WorkspaceID: "workspace-a", Kind: "export", ObjectKey: "customer", IdempotencyKey: "export-1", PayloadJSON: `{}`, ActorID: "admin", RoleKey: "admin"})
	if err != nil || replayed || queued.Status != "queued" {
		t.Fatalf("enqueue=%+v replayed=%v err=%v", queued, replayed, err)
	}
	replayedJob, replayed, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{WorkspaceID: "workspace-a", Kind: "export", ObjectKey: "customer", IdempotencyKey: "export-1", PayloadJSON: `{}`})
	if err != nil || !replayed || replayedJob.ID != queued.ID {
		t.Fatalf("idempotent replay=%+v replayed=%v err=%v", replayedJob, replayed, err)
	}
	if _, replayed, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{WorkspaceID: "workspace-a", Kind: "export", ObjectKey: "customer", IdempotencyKey: "export-1", PayloadJSON: `{different}`}); !errors.Is(err, recordcontract.ErrRecordBatchJobIdempotencyConflict) || replayed {
		t.Fatalf("expected fingerprint conflict, replayed=%v err=%v", replayed, err)
	}
	claimed, err := repository.ClaimRecordBatchJobs(t.Context(), 1, "worker-a", time.Minute, time.Now())
	if err != nil || len(claimed) != 1 || claimed[0].Status != "running" || claimed[0].FencingToken != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	job := claimed[0]
	job.Checkpoint, job.Total = 200, 500
	if err := repository.SaveRecordBatchJobCheckpoint(t.Context(), job, time.Now()); err != nil {
		t.Fatal(err)
	}
	chunks := []recordmodel.RecordBatchJobChunk{{Content: "id,name\n"}, {Content: "1,Acme\n"}}
	if err := repository.ReplaceRecordBatchJobChunks(t.Context(), job, chunks, time.Now()); err != nil {
		t.Fatal(err)
	}
	job.Checkpoint, job.ResultChunks, job.ResultFilename, job.ResultType = 500, len(chunks), "customer.csv", "text/csv"
	if err := repository.CompleteRecordBatchJob(t.Context(), job, time.Now()); err != nil {
		t.Fatal(err)
	}
	completed, found, err := repository.GetRecordBatchJob(t.Context(), "workspace-a", job.ID)
	if err != nil || !found || completed.Status != "completed" || completed.Checkpoint != 500 || completed.ResultChunks != 2 {
		t.Fatalf("completed=%+v found=%v err=%v", completed, found, err)
	}
	storedChunks, err := repository.ListRecordBatchJobChunks(t.Context(), "workspace-a", job.ID)
	if err != nil || len(storedChunks) != 2 || storedChunks[1].Content != "1,Acme\n" {
		t.Fatalf("chunks=%+v err=%v", storedChunks, err)
	}

	cancellable, _, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{WorkspaceID: "workspace-a", Kind: "import", ObjectKey: "customer", IdempotencyKey: "import-1", PayloadJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = repository.ClaimRecordBatchJobs(t.Context(), 1, "worker-b", time.Minute, time.Now())
	if err != nil || len(claimed) != 1 || claimed[0].ID != cancellable.ID {
		t.Fatalf("second claim=%+v err=%v", claimed, err)
	}
	cancelled, found, err := repository.CancelRecordBatchJob(t.Context(), "workspace-a", cancellable.ID)
	if err != nil || !found || cancelled.Status != "cancelled" {
		t.Fatalf("cancelled=%+v found=%v err=%v", cancelled, found, err)
	}
	if err := repository.CompleteRecordBatchJob(t.Context(), claimed[0], time.Now()); err == nil {
		t.Fatal("stale fenced worker completed a cancelled job")
	}
}

func TestRecordBatchJobStoreCommitsPagedCheckpointExactlyOnce(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	queued, _, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{WorkspaceID: "workspace-a", Kind: "report_export", ObjectKey: "ledger", IdempotencyKey: "export-pages", PayloadJSON: `{}`, AuditID: "audit-1", ActorID: "requester"})
	if err != nil {
		t.Fatal(err)
	}
	byFingerprint, found, err := repository.FindLatestRecordBatchJobByFingerprint(t.Context(), queued.WorkspaceID, queued.Kind, queued.ObjectKey, queued.Fingerprint)
	if err != nil || !found || byFingerprint.ID != queued.ID {
		t.Fatalf("fingerprint replay=%+v found=%v err=%v", byFingerprint, found, err)
	}
	claimed, err := repository.ClaimRecordBatchJobs(t.Context(), 1, "worker-a", time.Minute, time.Now())
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	job := claimed[0]
	first := recordmodel.RecordBatchJobChunk{WorkspaceID: job.WorkspaceID, JobID: job.ID, Sequence: 0, Content: "id\n1\n2\n"}
	if err := repository.CommitRecordBatchJobPage(t.Context(), job, "", first, "cursor-1", 2, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repository.CommitRecordBatchJobPage(t.Context(), job, "", first, "cursor-1", 2, 3, time.Now()); err == nil {
		t.Fatal("replayed page commit unexpectedly succeeded")
	}
	current, found, err := repository.GetRecordBatchJob(t.Context(), job.WorkspaceID, queued.ID)
	if err != nil || !found || current.CheckpointCursor != "cursor-1" || current.Checkpoint != 2 || current.ResultChunks != 1 {
		t.Fatalf("checkpoint=%+v found=%v err=%v", current, found, err)
	}
	second := recordmodel.RecordBatchJobChunk{WorkspaceID: job.WorkspaceID, JobID: job.ID, Sequence: 1, Content: "3\n"}
	current.LeaseOwner, current.FencingToken = job.LeaseOwner, job.FencingToken
	if err := repository.CommitRecordBatchJobPage(t.Context(), current, "cursor-1", second, "", 3, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	chunks, err := repository.ListRecordBatchJobChunks(t.Context(), job.WorkspaceID, job.ID)
	if err != nil || len(chunks) != 2 || chunks[0].Content+chunks[1].Content != "id\n1\n2\n3\n" {
		t.Fatalf("chunks=%+v err=%v", chunks, err)
	}
}

func TestRecordBatchJobStoreFairClaimHeartbeatRetryAndQueueStats(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	for index := 0; index < 4; index++ {
		if _, _, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{WorkspaceID: "workspace-hot", Kind: "export", ObjectKey: "customer", IdempotencyKey: fmt.Sprintf("hot-%d", index), PayloadJSON: `{}`}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{WorkspaceID: "workspace-other", Kind: "export", ObjectKey: "customer", IdempotencyKey: "other", PayloadJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test record batch queue stats")
	depth, oldest, err := repository.GlobalRecordBatchJobQueueStats(t.Context(), scope)
	if err != nil || depth != 5 || oldest < 0 {
		t.Fatalf("queue stats depth=%d oldest=%s err=%v", depth, oldest, err)
	}
	claimed, err := repository.ClaimRecordBatchJobs(t.Context(), 2, "worker-fair", time.Minute, time.Now())
	if err != nil || len(claimed) != 2 || claimed[0].WorkspaceID == claimed[1].WorkspaceID {
		t.Fatalf("fair claim=%+v err=%v", claimed, err)
	}
	job := claimed[0]
	if err := repository.HeartbeatRecordBatchJob(t.Context(), job, 2*time.Minute, time.Now()); err != nil {
		t.Fatal(err)
	}
	job.ErrorCode = "backend.transient"
	now := time.Now().UTC()
	if err := repository.RetryRecordBatchJob(t.Context(), job, now.Add(-time.Second), now); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repository.ClaimRecordBatchJobs(t.Context(), 1, "worker-retry", time.Minute, time.Now())
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != job.ID || reclaimed[0].AttemptCount != 2 {
		t.Fatalf("retry claim=%+v err=%v", reclaimed, err)
	}
}
