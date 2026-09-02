package recordtimer

import (
	"context"
	"testing"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestRecordTimerFailureRecoveryOwnsStateAndEvidence(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	failed := recordmodel.Record{ID: "timer-1", UpdatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), Data: map[string]any{
		"status": "failed", "object_key": "order", "record_id": "order-1", "purpose": "payment_timeout",
		"attempt": 3, "max_attempts": 3, "fencing_token": 7, "last_error": "backend.payment.failed",
	}}
	var committed []transactionmodel.RecordMutationCommit
	repository := &recordTimerRepositoryFake{
		get: func(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
			if workspaceID != "workspace-a" || object.Key != "record_timer" || id != failed.ID {
				t.Fatalf("get %q %q %q", workspaceID, object.Key, id)
			}
			return failed, true, nil
		},
		commit: func(_ context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) error {
			if workspaceID != "workspace-a" {
				t.Fatalf("workspace=%q", workspaceID)
			}
			committed = append([]transactionmodel.RecordMutationCommit(nil), commits...)
			return nil
		},
	}
	service := NewRecordTimerApplicationServiceWithWorker(recordTimerTestSchema(), nil, repository, workerplatform.Dependencies{Clock: recordTimerFixedClock{now: now}})
	inspected, err := service.InspectFailure(t.Context(), failed.ID, recordTimerTestPrincipal(operationscontract.ActionInspectDeadLetter))
	if err != nil || inspected.ID != failed.ID {
		t.Fatalf("inspect=%#v err=%v", inspected, err)
	}
	requeued, err := service.RetryFailure(t.Context(), failed.ID, "dependency restored", recordTimerTestPrincipal(operationscontract.ActionRetryDeadLetter))
	if err != nil {
		t.Fatal(err)
	}
	if requeued.Data["status"] != "scheduled" || requeued.Data["attempt"] != 0 || len(committed) != 2 || committed[1].Record.Data["event_type"] != "requeued" {
		t.Fatalf("requeued=%#v commits=%#v", requeued, committed)
	}
	resolved, err := service.ResolveFailure(t.Context(), failed.ID, "obsolete", recordTimerTestPrincipal(operationscontract.ActionRetryDeadLetter))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Data["status"] != "cancelled" || committed[1].Record.Data["event_type"] != "resolved" {
		t.Fatalf("resolved=%#v commits=%#v", resolved, committed)
	}
}
