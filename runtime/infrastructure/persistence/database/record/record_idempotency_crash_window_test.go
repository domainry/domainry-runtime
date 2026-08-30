package record

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/idempotency"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRecordIdempotencyMultipleRuntimeStoresSharingDatabaseHaveOneOwner(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "record-multi-runtime.db")
	stores := make([]*database.RuntimeStore, 2)
	for index := range stores {
		store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
		if err != nil {
			t.Fatal(err)
		}
		stores[index] = store
		defer store.Close()
	}
	if err := stores[0].EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repositories := []RecordStore{NewRecordStore(stores[0]), NewRecordStore(stores[1])}
	now := time.Date(2026, 7, 19, 19, 30, 0, 0, time.UTC)
	start := make(chan struct{})
	var acquired atomic.Int64
	errorsFound := make(chan error, 100)
	var wait sync.WaitGroup
	for worker := 0; worker < 100; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-start
			claim, err := repositories[worker%len(repositories)].TryBeginRecordMutation(t.Context(), recordmodel.RecordMutationClaimRequest{
				Execution:          recordmodel.RecordMutationExecution{WorkspaceID: "workspace-a", Operation: "create", ObjectKey: "customer", IdempotencyKey: "multi-runtime"},
				RequestFingerprint: "same-request", LeaseOwner: fmt.Sprintf("runtime-%d", worker), LeaseTTL: time.Minute, Now: now,
			})
			if err != nil {
				errorsFound <- err
				return
			}
			if claim.Decision == idempotency.DecisionAcquired {
				acquired.Add(1)
			} else if claim.Decision != idempotency.DecisionInProgress {
				errorsFound <- fmt.Errorf("unexpected claim decision %s", claim.Decision)
			}
		}(worker)
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	if acquired.Load() != 1 {
		t.Fatalf("acquired=%d want 1", acquired.Load())
	}
}

func TestRecordIdempotencyCrashWindowBeforeAndAfterClaim(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "record-claim-crash-window.db")
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	now := time.Date(2026, 7, 19, 20, 0, 0, 0, time.UTC)
	request := recordmodel.RecordMutationClaimRequest{
		Execution:          recordmodel.RecordMutationExecution{WorkspaceID: "workspace-a", Operation: "create", ObjectKey: "customer", IdempotencyKey: "claim-crash-window"},
		RequestFingerprint: "same-request", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.TryBeginRecordMutation(cancelled, request); err == nil {
		t.Fatal("claim before crash injection: expected cancelled context error")
	}
	var receiptCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _record_mutation_executions`).Scan(&receiptCount); err != nil || receiptCount != 0 {
		t.Fatalf("claim before crash injection persisted receipt: count=%d err=%v", receiptCount, err)
	}

	claim, err := repository.TryBeginRecordMutation(t.Context(), request)
	if err != nil || claim.Decision != idempotency.DecisionAcquired {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	resumed, err := NewRecordStore(restarted).TryBeginRecordMutation(t.Context(), request)
	if err != nil || resumed.Decision != idempotency.DecisionInProgress || resumed.Execution.ID != claim.Execution.ID {
		t.Fatalf("claim after restart=%+v err=%v", resumed, err)
	}
}

func TestRecordIdempotencyProcessRestartReplaysSuccessAndReclaimsExpiredProcessing(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "record-restart-replay-reclaim.db")
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE restart_record (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	now := time.Date(2026, 7, 19, 22, 0, 0, 0, time.UTC)
	successRequest := recordmodel.RecordMutationClaimRequest{
		Execution:          recordmodel.RecordMutationExecution{WorkspaceID: "workspace-a", Operation: "create", ObjectKey: "restart_record", IdempotencyKey: "restart-success"},
		RequestFingerprint: "success-request", LeaseOwner: "runtime-before-restart", LeaseTTL: time.Minute, Now: now,
	}
	successClaim, err := repository.TryBeginRecordMutation(t.Context(), successRequest)
	if err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "restart_record", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	record := recordmodel.Record{ID: "record-success", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"name": "durable"}}
	if _, err := repository.CommitRecordMutationExecution(t.Context(), transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: record}, recordmodel.RecordMutationCompletion{
		WorkspaceID: successClaim.Execution.WorkspaceID,
		ExecutionID: successClaim.Execution.ID, LeaseOwner: successClaim.Execution.LeaseOwner, FencingToken: successClaim.Execution.FencingToken, Result: record, ExpiresAt: now.Add(time.Hour), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	expiredRequest := recordmodel.RecordMutationClaimRequest{
		Execution:          recordmodel.RecordMutationExecution{WorkspaceID: "workspace-a", Operation: "update", ObjectKey: "restart_record", TargetID: record.ID, IdempotencyKey: "restart-expired"},
		RequestFingerprint: "expired-request", LeaseOwner: "runtime-before-restart", LeaseTTL: time.Minute, Now: now,
	}
	expiredClaim, err := repository.TryBeginRecordMutation(t.Context(), expiredRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restartedRepository := NewRecordStore(restarted)
	replay, err := restartedRepository.TryBeginRecordMutation(t.Context(), successRequest)
	if err != nil || replay.Decision != idempotency.DecisionReplay || replay.Execution.Result.ID != record.ID {
		t.Fatalf("restart replay=%+v err=%v", replay, err)
	}
	expiredRequest.LeaseOwner = "runtime-after-restart"
	expiredRequest.Now = now.Add(2 * time.Minute)
	reclaimed, err := restartedRepository.TryBeginRecordMutation(t.Context(), expiredRequest)
	if err != nil || reclaimed.Decision != idempotency.DecisionAcquired || reclaimed.Execution.FencingToken != expiredClaim.Execution.FencingToken+1 {
		t.Fatalf("restart reclaim=%+v err=%v", reclaimed, err)
	}
}

func TestRecordIdempotencyCrashWindowRollsBackBusinessWriteBeforeReceiptCompletion(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "record-commit-crash-window.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE crash_window_record (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	now := time.Date(2026, 7, 19, 21, 0, 0, 0, time.UTC)
	claim, err := repository.TryBeginRecordMutation(t.Context(), recordmodel.RecordMutationClaimRequest{
		Execution:          recordmodel.RecordMutationExecution{WorkspaceID: "workspace-a", Operation: "create", ObjectKey: "crash_window_record", IdempotencyKey: "commit-crash-window"},
		RequestFingerprint: "same-request", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil || claim.Decision != idempotency.DecisionAcquired {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if _, err := store.DB().Exec(`CREATE TRIGGER fail_receipt_completion BEFORE UPDATE OF status ON _record_mutation_executions WHEN NEW.status = 'succeeded' BEGIN SELECT RAISE(ABORT, 'injected crash before receipt completion'); END`); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "crash_window_record", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	record := recordmodel.Record{ID: "record-1", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"name": "Acme"}}
	commit := transactionmodel.RecordMutationCommit{
		Operation: "create", Object: object, Record: record,
		Audit:           &auditmodel.AuditEvent{ID: "crash-audit-1", Event: "record_created", ObjectKey: object.Key, RecordID: record.ID, CreatedAt: now.Format(time.RFC3339Nano)},
		Outbox:          []integrationmodel.IntegrationOutboxMessage{{ID: "crash-outbox-1", WorkspaceID: "workspace-a", ConnectorKey: "webhook", Operation: "record.created", DedupKey: "crash-record-created"}},
		WorkflowIntents: []workflowmodel.WorkflowExecution{{ID: "crash-workflow-1", WorkflowKey: "crash-created", Trigger: "record_created", Status: "pending", ObjectKey: object.Key, RecordID: record.ID, ActorID: "admin", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}},
	}
	if _, err := repository.CommitRecordMutationExecution(t.Context(), commit, recordmodel.RecordMutationCompletion{
		WorkspaceID: claim.Execution.WorkspaceID,
		ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken, Result: record, ExpiresAt: now.Add(time.Hour), Now: now,
	}); err == nil {
		t.Fatal("expected injected receipt completion failure")
	}
	var businessCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM crash_window_record WHERE id = ?`, record.ID).Scan(&businessCount); err != nil || businessCount != 0 {
		t.Fatalf("business write escaped failed transaction: count=%d err=%v", businessCount, err)
	}
	for table, id := range map[string]string{"_audit_events": "crash-audit-1", "_publication_outbox": "crash-outbox-1", "_workflow_executions": "crash-workflow-1"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.Identifier(table)+" WHERE "+store.Identifier("id")+" = ?", id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("atomic side fact escaped failed transaction: %s/%s count=%d err=%v", table, id, count, err)
		}
	}
	receipt, found, err := repository.findRecordMutationExecution(t.Context(), claim.Execution)
	if err != nil || !found || receipt.Status != string(idempotency.StatusProcessing) {
		t.Fatalf("receipt after injected crash=%+v found=%v err=%v", receipt, found, err)
	}
}
