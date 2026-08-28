package record

import (
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
	"github.com/domainry/domainry-runtime/testsupport/notificationsdkfixture"
)

func TestRecordMutationExecutionClaimCommitReplayConflictAndRollback(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "record-idempotency.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE idempotent_create_record (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	now := time.Date(2026, 7, 19, 17, 0, 0, 0, time.UTC)
	request := recordmodel.RecordMutationClaimRequest{
		Execution:          recordmodel.RecordMutationExecution{WorkspaceID: "workspace-a", Operation: "create", ObjectKey: "idempotent_create_record", IdempotencyKey: "create-1", ActorID: "admin"},
		RequestFingerprint: "fingerprint-a", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	}
	claim, err := repository.TryBeginRecordMutation(t.Context(), request)
	if err != nil || claim.Decision != idempotency.DecisionAcquired {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	inProgress, err := repository.TryBeginRecordMutation(t.Context(), request)
	if err != nil || inProgress.Decision != idempotency.DecisionInProgress {
		t.Fatalf("in progress=%#v err=%v", inProgress, err)
	}
	conflicting := request
	conflicting.RequestFingerprint = "fingerprint-b"
	conflict, err := repository.TryBeginRecordMutation(t.Context(), conflicting)
	if err != nil || conflict.Decision != idempotency.DecisionFingerprintConflict {
		t.Fatalf("conflict=%#v err=%v", conflict, err)
	}
	object := definitionmodel.ObjectSchema{Key: "idempotent_create_record", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	record := recordmodel.Record{ID: "record-1", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"name": "Acme"}}
	commit := transactionmodel.RecordMutationCommit{
		Operation: "create", Object: object, Record: record,
		Audit:              &auditmodel.AuditEvent{ID: "create-audit-1", Event: "record_created", ObjectKey: object.Key, RecordID: record.ID, CreatedAt: now.Format(time.RFC3339Nano)},
		Outbox:             []integrationmodel.IntegrationOutboxMessage{{ID: "create-outbox-1", WorkspaceID: "workspace-a", ConnectorKey: "webhook", Operation: "record.created", DedupKey: "record-1-created"}},
		WorkflowIntents:    []workflowmodel.WorkflowExecution{{ID: "create-workflow-1", WorkflowKey: "customer-created", Trigger: "record_created", Status: "pending", ObjectKey: object.Key, RecordID: record.ID, ActorID: "admin", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}},
		NotificationEvents: []notificationmodel.NotificationEvent{{ID: "create-notification-1", WorkspaceID: "workspace-a", Source: "record", SourceEventID: "record-1-created", EventType: "record.created", Category: "business", Severity: "info", Surface: "business_workspace", RecipientUserIDs: []string{"admin"}, ActionState: "none", OccurredAt: now.Format(time.RFC3339Nano), Snapshot: notificationmodel.NotificationInboxSnapshot{Title: "Record created", Body: "The record was created."}, Status: "queued", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}},
	}
	completed, err := repository.CommitRecordMutationExecution(t.Context(), commit, recordmodel.RecordMutationCompletion{WorkspaceID: claim.Execution.WorkspaceID, ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken, Result: record, ExpiresAt: now.Add(24 * time.Hour), Now: now})
	if err != nil || completed.Status != string(idempotency.StatusSucceeded) || completed.Result.ID != record.ID {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	if found, ok, err := repository.FindRecordMutationExecution(t.Context(), request.Execution); err != nil || !ok || found.ID != claim.Execution.ID {
		t.Fatalf("find execution=%#v ok=%v err=%v", found, ok, err)
	}
	for table, id := range map[string]string{"_audit_events": "create-audit-1", "integration_outbox_messages": "create-outbox-1", "_workflow_executions": "create-workflow-1", "notification_events": "create-notification-1", "record_mutation_executions": claim.Execution.ID} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.Identifier(table)+" WHERE "+store.Identifier("id")+" = ?", id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("atomic fact %s/%s count=%d err=%v", table, id, count, err)
		}
	}
	replay, err := repository.TryBeginRecordMutation(t.Context(), request)
	if err != nil || replay.Decision != idempotency.DecisionReplay || replay.Execution.Result.ID != record.ID {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	staleDelete := transactionmodel.RecordMutationCommit{Operation: "delete", Object: object, RecordID: record.ID, ExpectedUpdatedAt: "stale-version"}
	if err := repository.CommitRecordMutation(t.Context(), request.Execution.WorkspaceID, staleDelete); !mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
		t.Fatalf("stale delete error=%v", err)
	}
	if _, found, err := repository.GetRecord(t.Context(), request.Execution.WorkspaceID, object, record.ID); err != nil || !found {
		t.Fatalf("stale delete removed record found=%v err=%v", found, err)
	}

	rollbackRequest := request
	rollbackRequest.Execution.IdempotencyKey = "create-rollback"
	rollbackRequest.LeaseOwner = "runtime-b"
	rollbackClaim, err := repository.TryBeginRecordMutation(t.Context(), rollbackRequest)
	if err != nil {
		t.Fatal(err)
	}
	badCommit := transactionmodel.RecordMutationCommit{Operation: "unsupported", Object: object, Record: recordmodel.Record{ID: "must-not-exist"}}
	if _, err := repository.CommitRecordMutationExecution(t.Context(), badCommit, recordmodel.RecordMutationCompletion{WorkspaceID: rollbackClaim.Execution.WorkspaceID, ExecutionID: rollbackClaim.Execution.ID, LeaseOwner: rollbackClaim.Execution.LeaseOwner, FencingToken: rollbackClaim.Execution.FencingToken, ExpiresAt: now.Add(time.Hour), Now: now}); err == nil {
		t.Fatal("expected commit failure")
	}
	current, found, err := repository.findRecordMutationExecution(t.Context(), rollbackClaim.Execution)
	if err != nil || !found || current.Status != string(idempotency.StatusProcessing) {
		t.Fatalf("rollback receipt=%#v found=%v err=%v", current, found, err)
	}
}

func TestRecordMutationExecutionHundredConcurrentClaimsHaveOneOwner(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "record-idempotency-concurrent.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.DB().SetMaxOpenConns(16)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	now := time.Date(2026, 7, 19, 18, 0, 0, 0, time.UTC)
	var acquired atomic.Int64
	errorsFound := make(chan error, 100)
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			claim, err := repository.TryBeginRecordMutation(t.Context(), recordmodel.RecordMutationClaimRequest{
				Execution:          recordmodel.RecordMutationExecution{WorkspaceID: "workspace-a", Operation: "create", ObjectKey: "customer", IdempotencyKey: "same-key"},
				RequestFingerprint: "same", LeaseOwner: fmt.Sprintf("runtime-%d", worker), LeaseTTL: time.Minute, Now: now,
			})
			if err != nil {
				errorsFound <- err
				return
			}
			if claim.Decision == idempotency.DecisionAcquired {
				acquired.Add(1)
			} else if claim.Decision != idempotency.DecisionInProgress {
				errorsFound <- fmt.Errorf("unexpected decision %s", claim.Decision)
			}
		}(index)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	if acquired.Load() != 1 {
		t.Fatalf("acquired=%d want 1", acquired.Load())
	}
}

func TestRecordMutationHundredConcurrentUpdatesHaveNoLostUpdateOrPartialCommit(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "record-update-concurrent.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.DB().SetMaxOpenConns(16)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE concurrent_record (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "concurrent_record", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	initial := recordmodel.Record{ID: "shared", CreatedAt: "version-0", UpdatedAt: "version-1", Data: map[string]any{"name": "initial"}}
	if err := repository.CommitRecordMutation(t.Context(), "default", transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: initial}); err != nil {
		t.Fatal(err)
	}

	var succeeded atomic.Int64
	var conflicted atomic.Int64
	var transientlyRejected atomic.Int64
	errorsFound := make(chan error, 100)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-start
			identity := fmt.Sprintf("worker-%03d", worker)
			commit := transactionmodel.RecordMutationCommit{
				Operation: "update", Object: object,
				Record:            recordmodel.Record{ID: initial.ID, CreatedAt: initial.CreatedAt, UpdatedAt: "version-" + identity, Data: map[string]any{"name": identity}},
				ExpectedUpdatedAt: initial.UpdatedAt,
				Audit:             &auditmodel.AuditEvent{ID: "update-audit-" + identity, Event: "record_updated", ObjectKey: object.Key, RecordID: initial.ID, CreatedAt: "2026-07-19T00:00:00Z"},
				Outbox:            []integrationmodel.IntegrationOutboxMessage{{ID: "update-outbox-" + identity, WorkspaceID: "default", ConnectorKey: "webhook", Operation: "concurrent.record.updated", DedupKey: identity}},
				WorkflowIntents:   []workflowmodel.WorkflowExecution{{ID: "update-workflow-" + identity, WorkflowKey: "concurrent-record-updated", Trigger: "record_updated", Status: "pending", ObjectKey: object.Key, RecordID: initial.ID, CreatedAt: "2026-07-19T00:00:00Z", UpdatedAt: "2026-07-19T00:00:00Z"}},
			}
			err := repository.CommitRecordMutation(t.Context(), "default", commit)
			switch {
			case err == nil:
				succeeded.Add(1)
			case mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic):
				conflicted.Add(1)
			case mutation.IsTransactionTransient(err, ""):
				transientlyRejected.Add(1)
			default:
				errorsFound <- err
			}
		}(index)
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("unexpected concurrent update error: %v", err)
	}
	if succeeded.Load() != 1 || conflicted.Load()+transientlyRejected.Load() != 99 {
		t.Fatalf("succeeded=%d conflicted=%d transiently_rejected=%d", succeeded.Load(), conflicted.Load(), transientlyRejected.Load())
	}
	current, found, err := repository.GetRecord(t.Context(), "default", object, initial.ID)
	if err != nil || !found || current.UpdatedAt == initial.UpdatedAt || current.Data["name"] == "initial" {
		t.Fatalf("current=%#v found=%v err=%v", current, found, err)
	}
	winner := current.Data["name"].(string)
	for table, prefix := range map[string]string{
		"_audit_events":               "update-audit-",
		"integration_outbox_messages": "update-outbox-",
		"_workflow_executions":        "update-workflow-",
	} {
		var count, winnerCount int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.Identifier(table)+" WHERE "+store.Identifier("id")+" LIKE ?", prefix+"%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.Identifier(table)+" WHERE "+store.Identifier("id")+" = ?", prefix+winner).Scan(&winnerCount); err != nil {
			t.Fatal(err)
		}
		if count != 1 || winnerCount != 1 {
			t.Fatalf("partial commit in %s: count=%d winner_count=%d winner=%s", table, count, winnerCount, winner)
		}
	}
}

func TestRecordMutationOperationCompletionIsFencedAndReplayable(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "record-operation.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	now := time.Date(2026, 7, 19, 19, 0, 0, 0, time.UTC)
	request := recordmodel.RecordMutationClaimRequest{Execution: recordmodel.RecordMutationExecution{WorkspaceID: "workspace-a", Operation: "import", ObjectKey: "customer", IdempotencyKey: "import-1"}, RequestFingerprint: "csv-fingerprint", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now}
	claim, err := repository.TryBeginRecordMutation(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	completion := recordmodel.RecordMutationCompletion{WorkspaceID: claim.Execution.WorkspaceID, ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: 99, Result: recordmodel.RecordImportApplyResult{ObjectKey: "customer", Created: 2}, ExpiresAt: now.Add(24 * time.Hour), Now: now}
	if _, err := repository.CompleteRecordMutationExecution(t.Context(), completion); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("stale operation completion=%v", err)
	}
	completion.FencingToken = claim.Execution.FencingToken
	if _, err := repository.CompleteRecordMutationExecution(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	replay, err := repository.TryBeginRecordMutation(t.Context(), request)
	if err != nil || replay.Decision != idempotency.DecisionReplay || replay.Execution.OperationResult["created"] != float64(2) {
		t.Fatalf("operation replay=%#v err=%v", replay, err)
	}
}

func TestRecordMutationExecutionWorkspaceScopeDoesNotConflictOrLeakReplay(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "record-workspace-isolation.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	now := time.Date(2026, 7, 19, 20, 0, 0, 0, time.UTC)
	claim := func(workspace, owner string) recordmodel.RecordMutationClaimResult {
		result, err := repository.TryBeginRecordMutation(t.Context(), recordmodel.RecordMutationClaimRequest{
			Execution:          recordmodel.RecordMutationExecution{WorkspaceID: workspace, Operation: "import", ObjectKey: "customer", IdempotencyKey: "shared-key"},
			RequestFingerprint: "same-request", LeaseOwner: owner, LeaseTTL: time.Minute, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	workspaceA := claim("workspace-a", "runtime-a")
	if _, err := repository.CompleteRecordMutationExecution(t.Context(), recordmodel.RecordMutationCompletion{
		WorkspaceID: workspaceA.Execution.WorkspaceID,
		ExecutionID: workspaceA.Execution.ID, LeaseOwner: workspaceA.Execution.LeaseOwner, FencingToken: workspaceA.Execution.FencingToken,
		Result: map[string]any{"private": "workspace-a-only"}, ExpiresAt: now.Add(time.Hour), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	workspaceB := claim("workspace-b", "runtime-b")
	if workspaceB.Decision != idempotency.DecisionAcquired || workspaceB.Execution.ID == workspaceA.Execution.ID || len(workspaceB.Execution.OperationResult) != 0 {
		t.Fatalf("workspace result leaked: workspace-a=%+v workspace-b=%+v", workspaceA, workspaceB)
	}
	replayA := claim("workspace-a", "runtime-c")
	if replayA.Decision != idempotency.DecisionReplay || replayA.Execution.OperationResult["private"] != "workspace-a-only" {
		t.Fatalf("workspace-a replay=%+v", replayA)
	}
}
