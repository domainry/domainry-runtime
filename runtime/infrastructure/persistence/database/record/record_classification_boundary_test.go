package record

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRecordPersistenceRemainsClassifiedByResponsibility(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve record persistence source")
	}
	root := filepath.Dir(source)
	required := map[string]string{
		"record_batch_job_store.go":            "func (r RecordStore) EnqueueRecordBatchJob",
		"record_batch_job_claim_store.go":      "func (r RecordStore) ClaimRecordBatchJobs",
		"record_batch_job_transition_store.go": "func (r RecordStore) CommitRecordBatchJobPage",
		"record_batch_job_chunk_store.go":      "func (r RecordStore) ReplaceRecordBatchJobChunks",
		"record_batch_job_metrics_store.go":    "func (r RecordStore) RecordBatchJobQueueStats",
		"record_store.go":                      "type RecordStore struct",
		"record_scheduler_clock.go":            "func (r RecordStore) SchedulerNow",
		"record_query_store.go":                "func (r RecordStore) ListRecords",
		"record_crud_store.go":                 "func (r RecordStore) InsertRecord",
		"record_mutation_commit.go":            "func (r RecordStore) CommitRecordMutation",
		"record_mutation_invariants.go":        "func (r RecordStore) validateRelatedAggregateInvariantsTx",
		"record_query_values.go":               "func recordQueryDBValues",
		"record_row_write.go":                  "func appendRecordInsertMetadata",
		"record_mutation_effects.go":           "func (r RecordStore) insertWorkflowIntentTx",
	}
	for name, symbol := range required {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), symbol) {
			t.Errorf("record persistence file %s lost owned responsibility %q", name, symbol)
		}
		if lines := strings.Count(string(raw), "\n") + 1; lines > 400 {
			t.Errorf("record persistence file %s grew back into a catch-all: %d lines", name, lines)
		}
	}
}
