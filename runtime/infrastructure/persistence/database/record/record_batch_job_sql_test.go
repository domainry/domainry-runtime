package record

import (
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func recordBatchJobFixture() recordmodel.RecordBatchJob {
	return recordmodel.RecordBatchJob{ID: "job", WorkspaceID: "workspace", Kind: "export", ObjectKey: "customer", Status: "running", IdempotencyKey: "key", Fingerprint: "fingerprint", PayloadJSON: `{}`, LeaseOwner: "worker", LeaseExpiresAt: "expires", FencingToken: 1, CreatedAt: "created", UpdatedAt: "updated"}
}

func recordBatchJobQueryStep(job recordmodel.RecordBatchJob) recordSQLQueryStep {
	values := recordBatchJobValues(job)
	row := make([]driver.Value, len(values))
	for index, value := range values {
		row[index] = value
	}
	return recordSQLQueryStep{columns: recordBatchJobColumns, rows: [][]driver.Value{row}}
}

func recordBatchWorkspaceQueryStep(workspaces ...string) recordSQLQueryStep {
	rows := make([][]driver.Value, 0, len(workspaces))
	for _, workspace := range workspaces {
		rows = append(rows, []driver.Value{workspace})
	}
	return recordSQLQueryStep{columns: []string{"scope_key"}, rows: rows}
}

func TestRecordBatchJobSQLFailureStages(t *testing.T) {
	job, now := recordBatchJobFixture(), time.Now().UTC()
	store := scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}, querySteps: []recordSQLQueryStep{{}}})
	if _, _, err := store.EnqueueRecordBatchJob(t.Context(), job); !errors.Is(err, errRecordSQL) {
		t.Fatalf("enqueue error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}, querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, _, err := store.EnqueueRecordBatchJob(t.Context(), job); !errors.Is(err, errRecordSQL) {
		t.Fatalf("enqueue conflict read error=%v", err)
	}
	for _, invalid := range []recordmodel.RecordBatchJob{{WorkspaceID: "workspace", ObjectKey: "customer", IdempotencyKey: "key"}, {WorkspaceID: "workspace", Kind: "export", IdempotencyKey: "key"}} {
		if _, _, err := scriptedRecordStore(t, &recordSQLState{}).EnqueueRecordBatchJob(t.Context(), invalid); err == nil {
			t.Fatalf("invalid identity accepted: %+v", invalid)
		}
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{"short"}}}}})
	if _, _, err := store.GetRecordBatchJob(t.Context(), job.WorkspaceID, job.ID); err == nil {
		t.Fatal("corrupt job accepted")
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, _, err := store.GetRecordBatchJob(t.Context(), job.WorkspaceID, job.ID); !errors.Is(err, errRecordSQL) {
		t.Fatalf("get error=%v", err)
	}

	for name, state := range map[string]recordSQLState{
		"query":      {querySteps: []recordSQLQueryStep{{err: errRecordSQL}}},
		"scope-scan": {querySteps: []recordSQLQueryStep{{columns: []string{"scope_key", "extra"}, rows: [][]driver.Value{{"workspace", "extra"}}}}},
		"next":       {querySteps: []recordSQLQueryStep{recordBatchWorkspaceQueryStep("workspace"), {columns: recordBatchJobColumns, nextErr: errRecordSQL}}},
	} {
		t.Run("claim-"+name, func(t *testing.T) {
			store := scriptedRecordStore(t, &state)
			if _, err := store.ClaimRecordBatchJobs(t.Context(), 1, "worker", time.Minute, now); err == nil {
				t.Fatal("claim failure swallowed")
			}
		})
	}
	for name, state := range map[string]recordSQLState{
		"update": {querySteps: []recordSQLQueryStep{recordBatchWorkspaceQueryStep("workspace"), recordBatchJobQueryStep(job)}, execSteps: []recordSQLExecStep{{err: errRecordSQL}}},
		"rows":   {querySteps: []recordSQLQueryStep{recordBatchWorkspaceQueryStep("workspace"), recordBatchJobQueryStep(job)}, execSteps: []recordSQLExecStep{{rowsErr: errRecordSQL}}},
		"lost":   {querySteps: []recordSQLQueryStep{recordBatchWorkspaceQueryStep("workspace"), recordBatchJobQueryStep(job)}, execSteps: []recordSQLExecStep{{rows: 0}}},
	} {
		t.Run("claim-candidate-"+name, func(t *testing.T) {
			store := scriptedRecordStore(t, &state)
			claimed, err := store.ClaimRecordBatchJobs(t.Context(), 1, "worker", time.Minute, now)
			if name == "lost" {
				if err != nil || len(claimed) != 0 {
					t.Fatalf("lost claimed=%v err=%v", claimed, err)
				}
			} else if err == nil {
				t.Fatal("candidate failure swallowed")
			}
		})
	}

	for name, call := range map[string]func(RecordStore) error{
		"heartbeat": func(store RecordStore) error {
			return store.HeartbeatRecordBatchJob(t.Context(), job, time.Minute, now)
		},
		"retry":  func(store RecordStore) error { return store.RetryRecordBatchJob(t.Context(), job, now, now) },
		"update": func(store RecordStore) error { return store.CompleteRecordBatchJob(t.Context(), job, now) },
	} {
		t.Run(name+"-exec", func(t *testing.T) {
			store := scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}})
			if err := call(store); !errors.Is(err, errRecordSQL) {
				t.Fatalf("error=%v", err)
			}
		})
		t.Run(name+"-rows", func(t *testing.T) {
			store := scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rowsErr: errRecordSQL}}})
			if err := call(store); !errors.Is(err, errRecordSQL) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}})
	if _, _, err := store.RequeueRecordBatchJob(t.Context(), job.WorkspaceID, job.ID, now); !errors.Is(err, errRecordSQL) {
		t.Fatalf("requeue exec=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rowsErr: errRecordSQL}}})
	if _, _, err := store.RequeueRecordBatchJob(t.Context(), job.WorkspaceID, job.ID, now); !errors.Is(err, errRecordSQL) {
		t.Fatalf("requeue rows=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, _, err := store.RequeueRecordBatchJob(t.Context(), job.WorkspaceID, job.ID, now); !errors.Is(err, errRecordSQL) {
		t.Fatalf("requeue get=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}})
	if _, _, err := store.CancelRecordBatchJob(t.Context(), job.WorkspaceID, job.ID); !errors.Is(err, errRecordSQL) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestRecordBatchChunkAndStatsSQLFailureStages(t *testing.T) {
	job, now := recordBatchJobFixture(), time.Now().UTC()
	chunk := recordmodel.RecordBatchJobChunk{Content: "content"}
	for name, state := range map[string]recordSQLState{
		"begin":  {beginErr: errRecordSQL},
		"guard":  {querySteps: []recordSQLQueryStep{{err: errRecordSQL}}},
		"delete": {querySteps: []recordSQLQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{job.ID}}}}, execSteps: []recordSQLExecStep{{err: errRecordSQL}}},
		"insert": {querySteps: []recordSQLQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{job.ID}}}}, execSteps: []recordSQLExecStep{{rows: 1}, {err: errRecordSQL}}},
		"commit": {querySteps: []recordSQLQueryStep{{columns: []string{"id"}, rows: [][]driver.Value{{job.ID}}}}, commitErr: errRecordSQL},
	} {
		t.Run(name, func(t *testing.T) {
			store := scriptedRecordStore(t, &state)
			chunks := []recordmodel.RecordBatchJobChunk(nil)
			if name == "insert" {
				chunks = []recordmodel.RecordBatchJobChunk{chunk}
			}
			if err := store.ReplaceRecordBatchJobChunks(t.Context(), job, chunks, now); err == nil {
				t.Fatal("chunk failure swallowed")
			}
		})
	}
	for name, step := range map[string]recordSQLQueryStep{
		"query": {err: errRecordSQL},
		"scan":  {columns: []string{"workspace_id"}, rows: [][]driver.Value{{"short"}}},
		"next":  {columns: []string{"workspace_id", "job_id", "sequence_no", "content"}, nextErr: errRecordSQL},
	} {
		t.Run("list-"+name, func(t *testing.T) {
			store := scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{step}})
			if _, err := store.ListRecordBatchJobChunks(t.Context(), job.WorkspaceID, job.ID); err == nil {
				t.Fatal("list failure swallowed")
			}
		})
	}
	store := scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, _, err := store.FindRecordBatchJobByIdempotency(t.Context(), job.WorkspaceID, job.Kind, job.ObjectKey, job.IdempotencyKey); !errors.Is(err, errRecordSQL) {
		t.Fatalf("find error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, _, err := store.RecordBatchJobQueueStats(t.Context(), job.WorkspaceID); !errors.Is(err, errRecordSQL) {
		t.Fatalf("stats error=%v", err)
	}
	future := now.Add(time.Hour).Format(time.RFC3339Nano)
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"depth", "oldest"}, rows: [][]driver.Value{{int64(1), future}}}}})
	if depth, age, err := store.RecordBatchJobQueueStats(t.Context(), job.WorkspaceID); err != nil || depth != 1 || age != 0 {
		t.Fatalf("future stats depth=%d age=%v err=%v", depth, age, err)
	}
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{{columns: []string{"depth", "oldest"}, rows: [][]driver.Value{{int64(0), now.Format(time.RFC3339Nano)}}}}})
	if depth, age, err := store.RecordBatchJobQueueStats(t.Context(), job.WorkspaceID); err != nil || depth != 0 || age != 0 {
		t.Fatalf("zero stats depth=%d age=%v err=%v", depth, age, err)
	}
}
