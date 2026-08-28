package record

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRecordBatchLeaseContractAcrossDialects(t *testing.T) {
	cases := []struct{ name, driver, dsnEnv string }{
		{name: "sqlite", driver: "sqlite"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: test.driver, DatabaseDSN: os.Getenv(test.dsnEnv)}
			if test.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "record-batch-lease.db")
			} else if cfg.DatabaseDSN == "" {
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			store, err := database.OpenContext(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertRecordBatchEnqueueReplayContract(t, store, "record-dialect-enqueue-"+test.name+fmt.Sprint(time.Now().UnixNano()))
			assertRecordBatchLeaseContract(t, store, "record-dialect-"+test.name+fmt.Sprint(time.Now().UnixNano()))
		})
	}
}

func assertRecordBatchEnqueueReplayContract(t *testing.T, store *database.RuntimeStore, key string) {
	t.Helper()
	repository := NewRecordStore(store)
	const workers = 16
	start := make(chan struct{})
	jobs := make(chan recordmodel.RecordBatchJob, workers)
	errorsFound := make(chan error, workers)
	var group sync.WaitGroup
	for index := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			job, _, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{
				WorkspaceID: key, Kind: "report_export", ObjectKey: "ledger", IdempotencyKey: "canonical-export",
				PayloadJSON: `{"audit_id":"","report_key":"ledger","scope":{"purpose":"same"}}`, AuditID: fmt.Sprintf("audit-%d", index), ActorID: "requester",
			})
			if err != nil {
				errorsFound <- err
				return
			}
			jobs <- job
		}()
	}
	close(start)
	group.Wait()
	close(jobs)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for job := range jobs {
		ids[job.ID] = true
	}
	if len(ids) != 1 {
		t.Fatalf("canonical export created %d jobs: %#v", len(ids), ids)
	}
	if _, err := store.DB().ExecContext(t.Context(), "DELETE FROM "+store.TableIdentifier("record_batch_jobs")+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), key); err != nil {
		t.Fatal(err)
	}
}

func assertRecordBatchLeaseContract(t *testing.T, store *database.RuntimeStore, key string) {
	t.Helper()
	repository := NewRecordStore(store)
	workspace := key
	queued, _, err := repository.EnqueueRecordBatchJob(t.Context(), recordmodel.RecordBatchJob{
		WorkspaceID: workspace, Kind: "report_export", ObjectKey: "customer", IdempotencyKey: key,
		PayloadJSON: `{"audit_id":"","artifact_idempotency_key":"report-export-artifact:test"}`,
		AuditID:     "report_export_audit_formal", ActorID: "requester",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(t.Context(), "DELETE FROM "+store.TableIdentifier("record_batch_jobs")+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), workspace)
	})
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	winners := make(chan recordmodel.RecordBatchJob, 100)
	errorsFound := make(chan error, 100)
	var group sync.WaitGroup
	for index := range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			claimed, claimErr := repository.ClaimRecordBatchJobs(t.Context(), 1, fmt.Sprintf("runtime-%d", index), time.Minute, now)
			if claimErr != nil {
				errorsFound <- claimErr
			} else if len(claimed) == 1 && claimed[0].ID == queued.ID {
				winners <- claimed[0]
			}
		}()
	}
	close(start)
	group.Wait()
	close(winners)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	var first recordmodel.RecordBatchJob
	count := 0
	for winner := range winners {
		first, count = winner, count+1
	}
	if count != 1 || first.FencingToken != 1 || first.AuditID != queued.AuditID || !strings.Contains(first.PayloadJSON, `"audit_id":""`) {
		t.Fatalf("record batch winners=%d first=%#v", count, first)
	}
	if err := repository.CommitRecordBatchJobPage(t.Context(), first, "", recordmodel.RecordBatchJobChunk{Content: "id\n1\n"}, "offset:1", 1, 2, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reclaimAt := now.Add(2 * time.Minute)
	reclaimed, err := repository.ClaimRecordBatchJobs(t.Context(), 1, "runtime-restarted", time.Minute, reclaimAt)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].FencingToken != 2 || reclaimed[0].AuditID != queued.AuditID || reclaimed[0].Checkpoint != 1 || reclaimed[0].ResultChunks != 1 || !strings.Contains(reclaimed[0].PayloadJSON, `"audit_id":""`) {
		t.Fatalf("record batch reclaim=%#v err=%v", reclaimed, err)
	}
	if err := repository.CompleteRecordBatchJob(t.Context(), first, reclaimAt); err == nil {
		t.Fatal("stale record batch worker completed reclaimed job")
	}
	if err := repository.CompleteRecordBatchJob(t.Context(), reclaimed[0], reclaimAt); err != nil {
		t.Fatalf("current record batch worker complete: %v", err)
	}
}
