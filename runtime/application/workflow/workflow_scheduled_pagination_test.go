package workflow

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type scheduledKeysetReader struct {
	records []recordmodel.Record
	queries []recordmodel.RecordListQuery
}

func (r *scheduledKeysetReader) GetWorkflowRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return recordmodel.Record{}, false, nil
}

func (r *scheduledKeysetReader) ListWorkflowRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.queries = append(r.queries, query)
	start := 0
	if query.AfterID != "" {
		start, _ = slices.BinarySearchFunc(r.records, query.AfterID, func(record recordmodel.Record, id string) int {
			if record.ID < id {
				return -1
			}
			if record.ID > id {
				return 1
			}
			return 0
		})
		for start < len(r.records) && r.records[start].ID <= query.AfterID {
			start++
		}
	}
	end := min(len(r.records), start+query.PageSize)
	return recordmodel.RecordPageResult{Items: append([]recordmodel.Record(nil), r.records[start:end]...), HasNext: end < len(r.records)}, nil
}

func TestScheduledWorkflowWindowPageTraversesThousandsWithBoundedCheckpoint(t *testing.T) {
	now := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	workflow := definitionmodel.WorkflowSchema{
		Key: "activate", Enabled: true, IdempotencyKeys: []string{"record_id", "scheduled_at"},
		TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "scheduled", ObjectKey: "candidate"},
		Graph:           &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}},
	}
	worker := &workflowCommittedIntentWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, updateWhereResult: true}
	service := workflowRecordExecutionService(worker, workflow)
	reader := &scheduledKeysetReader{}
	for index := 0; index < 1001; index++ {
		reader.records = append(reader.records, recordmodel.Record{ID: fmt.Sprintf("candidate-%04d", index), Data: map[string]any{"eligible": true}})
	}
	service.recordReader = reader
	service.schemaMap = func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"candidate": {Key: "candidate"}}
	}
	principal := workflowExecutionPrincipal()
	for index := 0; index < len(reader.records); index++ {
		worker.claims = append(worker.claims, workflowmodel.WorkflowExecutionClaimResult{
			Decision: idempotency.DecisionAcquired,
			Receipt: workflowmodel.WorkflowExecutionReceipt{
				ID: fmt.Sprintf("receipt-%04d", index), WorkspaceID: principal.WorkspaceID,
				WorkflowKey: workflow.Key, LeaseOwner: "worker", FencingToken: 1,
			},
		})
	}
	checkpoint := ""
	counts := []int{}
	seen := map[string]bool{}
	for call := 0; call < 4; call++ {
		page, err := service.ProcessScheduledWorkflowWindowPage(t.Context(), "scheduled:activate", now, 500, checkpoint, principal)
		if err != nil {
			t.Fatal(err)
		}
		counts = append(counts, page.Scanned)
		for _, execution := range page.Executions {
			if seen[execution.RecordID] {
				t.Fatalf("duplicate record across checkpoints: %s", execution.RecordID)
			}
			seen[execution.RecordID] = true
		}
		checkpoint = page.Checkpoint
		if page.Complete {
			break
		}
	}
	if !slices.Equal(counts, []int{500, 500, 1}) || len(seen) != 1001 || checkpoint != "" {
		t.Fatalf("counts=%v seen=%d checkpoint=%q", counts, len(seen), checkpoint)
	}
	for _, query := range reader.queries {
		if query.PageSize < 1 || query.PageSize > 200 || len(query.Sort) != 1 || query.Sort[0].Field != "id" || query.Sort[0].Direction != "asc" {
			t.Fatalf("unbounded or unstable query: %+v", query)
		}
	}
}

func TestScheduledWorkflowWindowPageRejectsInvalidCheckpoint(t *testing.T) {
	service := &WorkflowApplicationService{}
	if _, err := service.ProcessScheduledWorkflowWindowPage(t.Context(), "scheduled:*", time.Now(), 10, "not-base64", workflowExecutionPrincipal()); err == nil {
		t.Fatal("invalid checkpoint accepted")
	}
}

func TestScheduledWorkflowWindowPageCrashReplayIsDeduplicatedBeforeCheckpointResume(t *testing.T) {
	now := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	principal := workflowExecutionPrincipal()
	workflow := definitionmodel.WorkflowSchema{
		Key: "activate", Enabled: true, IdempotencyKeys: []string{"record_id", "scheduled_at"},
		TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "scheduled", ObjectKey: "candidate"},
		Graph:           &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}},
	}
	worker := &workflowCommittedIntentWorkerEdgeStub{
		workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, updateWhereResult: true,
		claim: workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionAcquired, Receipt: workflowmodel.WorkflowExecutionReceipt{
			ID: "receipt-1", WorkspaceID: principal.WorkspaceID, WorkflowKey: workflow.Key, LeaseOwner: "worker", FencingToken: 1,
		}},
	}
	service := workflowRecordExecutionService(worker, workflow)
	service.recordReader = &scheduledKeysetReader{records: []recordmodel.Record{{ID: "candidate-1", Data: map[string]any{"eligible": true}}}}
	service.schemaMap = func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"candidate": {Key: "candidate"}}
	}
	first, err := service.ProcessScheduledWorkflowWindowPage(t.Context(), "scheduled:activate", now, 1, "", principal)
	if err != nil || first.Processed != 1 || first.Checkpoint == "" || len(worker.inserted) != 1 {
		t.Fatalf("first=%+v inserted=%d err=%v", first, len(worker.inserted), err)
	}
	executionID := worker.inserted[0].ID
	worker.claim = workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionReplay, Receipt: workflowmodel.WorkflowExecutionReceipt{
		ID: "receipt-1", WorkspaceID: principal.WorkspaceID, WorkflowKey: workflow.Key, ExecutionID: executionID,
	}}
	replayed, err := service.ProcessScheduledWorkflowWindowPage(t.Context(), "scheduled:activate", now, 1, "", principal)
	if err != nil || replayed.Processed != 0 || replayed.Checkpoint != first.Checkpoint || len(worker.inserted) != 1 {
		t.Fatalf("replayed=%+v inserted=%d err=%v", replayed, len(worker.inserted), err)
	}
	if len(worker.claimRequests) != 2 || worker.claimRequests[0].RequestFingerprint != worker.claimRequests[1].RequestFingerprint {
		t.Fatalf("crash replay fingerprints=%+v", worker.claimRequests)
	}
	resumed, err := service.ProcessScheduledWorkflowWindowPage(t.Context(), "scheduled:activate", now, 1, first.Checkpoint, principal)
	if err != nil || !resumed.Complete || resumed.Scanned != 0 || resumed.Checkpoint != "" {
		t.Fatalf("resumed=%+v err=%v", resumed, err)
	}
}
