package action

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/audit"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
	"github.com/domainry/domainry-runtime/testsupport/notificationsdkfixture"
)

func commitBusinessActionExecution(ctx context.Context, repository ActionBusinessExecutionStore, commits []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	transaction, err := repository.BeginExecutionTransaction(ctx)
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	return transaction.Commit(ctx, commits, completion)
}

var _ actioncontract.ActionExecutionClaimStore = ActionBusinessExecutionStore{}
var _ actioncontract.ActionExecutionTransactionStore = ActionBusinessExecutionStore{}

func TestBusinessActionExecutionStoreAtomicClaimReplayConflictAndFencing(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	request := actionmodel.ActionExecutionClaimRequest{
		Execution:          actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: "customer", RecordID: "c-1", ActionKey: "activate", IdempotencyKey: "idem-1", ActorID: "admin", RoleKey: "admin"},
		RequestFingerprint: "fingerprint-a", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	}
	first, err := repository.TryBeginExecution(t.Context(), request)
	if err != nil || first.Decision != idempotency.DecisionAcquired || first.Execution.FencingToken != 1 {
		t.Fatalf("first claim=%#v err=%v", first, err)
	}
	second, err := repository.TryBeginExecution(t.Context(), request)
	if err != nil || second.Decision != idempotency.DecisionInProgress {
		t.Fatalf("second claim=%#v err=%v", second, err)
	}
	conflicting := request
	conflicting.RequestFingerprint = "fingerprint-b"
	conflict, err := repository.TryBeginExecution(t.Context(), conflicting)
	if err != nil || conflict.Decision != idempotency.DecisionFingerprintConflict {
		t.Fatalf("conflicting claim=%#v err=%v", conflict, err)
	}
	completion := actionmodel.ActionExecutionCompletion{ExecutionID: first.Execution.ID, LeaseOwner: "runtime-a", FencingToken: 99, Result: map[string]any{"status": "active"}, ResponseStatus: 200, ExpiresAt: now.Add(30 * 24 * time.Hour), Now: now.Add(time.Second)}
	if _, err := repository.CompleteExecution(t.Context(), completion); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("stale completion error=%v", err)
	}
	completion.FencingToken = first.Execution.FencingToken
	completed, err := repository.CompleteExecution(t.Context(), completion)
	if err != nil || completed.Status != string(idempotency.StatusSucceeded) || completed.Result["status"] != "active" {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	replay, err := repository.TryBeginExecution(t.Context(), request)
	if err != nil || replay.Decision != idempotency.DecisionReplay || replay.Execution.Result["status"] != "active" {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	metrics := store.IdempotencyMetrics(t.Context()).Snapshot()
	for outcome, expected := range map[idempotency.Outcome]int64{
		idempotency.OutcomeAcquired: 1, idempotency.OutcomeInProgress: 1, idempotency.OutcomeConflict: 1,
		idempotency.OutcomeLeaseLost: 1, idempotency.OutcomeReplayed: 1,
	} {
		if metrics.Totals[outcome] != expected {
			t.Fatalf("metric %s=%d want=%d snapshot=%#v", outcome, metrics.Totals[outcome], expected, metrics)
		}
	}
}

func TestBusinessActionExecutionStoreReplaysTerminalFailureAndReclaimsRetryableFailure(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	now := time.Date(2026, 7, 23, 15, 0, 0, 0, time.UTC)
	newRequest := func(key, owner string) actionmodel.ActionExecutionClaimRequest {
		return actionmodel.ActionExecutionClaimRequest{
			Execution: actionmodel.ActionBusinessExecution{
				WorkspaceID: "workspace-a", ObjectKey: "group_class", ActionKey: "book_class", IdempotencyKey: key,
			},
			RequestFingerprint: "fingerprint", LeaseOwner: owner, LeaseTTL: time.Minute, Now: now,
		}
	}

	terminalRequest := newRequest("terminal", "runtime-a")
	terminalClaim, err := repository.TryBeginExecution(t.Context(), terminalRequest)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := repository.CompleteExecution(t.Context(), actionmodel.ActionExecutionCompletion{
		ExecutionID: terminalClaim.Execution.ID, LeaseOwner: terminalClaim.Execution.LeaseOwner,
		FencingToken: terminalClaim.Execution.FencingToken, Result: map[string]any{"kind": "conflict"},
		ErrorCode: "gym.class_waitlist_full", ExpiresAt: now.Add(24 * time.Hour), Now: now,
	})
	if err != nil || terminal.Status != string(idempotency.StatusFailedTerminal) ||
		terminal.ErrorCode != "gym.class_waitlist_full" {
		t.Fatalf("terminal=%#v error=%v", terminal, err)
	}
	terminalReplay, err := repository.TryBeginExecution(t.Context(), terminalRequest)
	if err != nil || terminalReplay.Decision != idempotency.DecisionReplay {
		t.Fatalf("terminal replay=%#v error=%v", terminalReplay, err)
	}

	retryableRequest := newRequest("retryable", "runtime-a")
	retryableClaim, err := repository.TryBeginExecution(t.Context(), retryableRequest)
	if err != nil {
		t.Fatal(err)
	}
	retryable, err := repository.CompleteExecution(t.Context(), actionmodel.ActionExecutionCompletion{
		ExecutionID: retryableClaim.Execution.ID, LeaseOwner: retryableClaim.Execution.LeaseOwner,
		FencingToken: retryableClaim.Execution.FencingToken, Result: map[string]any{"kind": "unavailable"},
		ErrorCode: "backend.action.timeout", Retryable: true, ExpiresAt: now.Add(24 * time.Hour), Now: now,
	})
	if err != nil || retryable.Status != string(idempotency.StatusFailedRetryable) {
		t.Fatalf("retryable=%#v error=%v", retryable, err)
	}
	retryableRequest.LeaseOwner = "runtime-b"
	retryableRequest.Now = now.Add(time.Second)
	reclaimed, err := repository.TryBeginExecution(t.Context(), retryableRequest)
	if err != nil || reclaimed.Decision != idempotency.DecisionAcquired ||
		reclaimed.Execution.Status != string(idempotency.StatusProcessing) ||
		reclaimed.Execution.FencingToken != retryableClaim.Execution.FencingToken+1 {
		t.Fatalf("reclaimed=%#v error=%v", reclaimed, err)
	}
}

func TestBusinessActionExecutionStoreRecordScopeAndExpiredReclaim(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	now := time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)
	claim := func(recordID, owner string, at time.Time) actionmodel.ActionExecutionClaimResult {
		t.Helper()
		result, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
			Execution:          actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: "customer", RecordID: recordID, ActionKey: "activate", IdempotencyKey: "shared-key"},
			RequestFingerprint: "same", LeaseOwner: owner, LeaseTTL: time.Minute, Now: at,
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := claim("c-1", "runtime-a", now)
	otherRecord := claim("c-2", "runtime-b", now)
	if first.Decision != idempotency.DecisionAcquired || otherRecord.Decision != idempotency.DecisionAcquired || first.Execution.ID == otherRecord.Execution.ID {
		t.Fatalf("record scope collided: first=%#v other=%#v", first, otherRecord)
	}
	reclaimed := claim("c-1", "runtime-c", now.Add(2*time.Minute))
	if reclaimed.Decision != idempotency.DecisionAcquired || reclaimed.Execution.FencingToken != 2 || reclaimed.Execution.LeaseOwner != "runtime-c" {
		t.Fatalf("reclaimed=%#v", reclaimed)
	}
	if err := repository.HeartbeatExecution(t.Context(), first.Execution.ID, "runtime-a", first.Execution.FencingToken, now.Add(3*time.Minute), now.Add(2*time.Minute)); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("stale heartbeat error=%v", err)
	}
	metrics := store.IdempotencyMetrics(t.Context()).Snapshot()
	if metrics.Totals[idempotency.OutcomeReclaimed] != 1 || metrics.Totals[idempotency.OutcomeLeaseLost] != 1 {
		t.Fatalf("reclaim metrics=%#v", metrics)
	}
}

func TestBusinessActionExecutionStoreHundredConcurrentClaimsHaveOneOwner(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	store.DB().SetMaxOpenConns(16)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	now := time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)
	var acquired atomic.Int64
	errorsFound := make(chan error, 100)
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
				Execution:          actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: "customer", RecordID: "c-1", ActionKey: "activate", IdempotencyKey: "concurrent-key"},
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
	if got := acquired.Load(); got != 1 {
		t.Fatalf("acquired claims=%d want 1", got)
	}
}

func TestBusinessActionExecutionStoreCommitsFactsAndReceiptInOneTransaction(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE action_atomic_record (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	now := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
		Execution:          actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: "action_atomic_record", ActionKey: "create", IdempotencyKey: "atomic-commit"},
		RequestFingerprint: "fingerprint", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "action_atomic_record", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	commit := transactionmodel.RecordMutationCommit{
		Operation: "create", Object: object,
		Record:          recordmodel.Record{ID: "record-1", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"status": "created"}},
		Audit:           &auditmodel.AuditEvent{ID: "audit-1", Event: "record_created", ObjectKey: object.Key, RecordID: "record-1", ActorID: "admin", RoleKey: "admin", Summary: "created", CreatedAt: now.Format(time.RFC3339Nano)},
		Outbox:          []integrationmodel.IntegrationOutboxMessage{{ID: "durable_intent:execution-1:0", WorkspaceID: "workspace-a", ConnectorKey: "webhook", ConnectionKey: "primary", Operation: "notify", RequestRef: "atomic-commit", DedupKey: "atomic-commit", RequestFingerprint: strings.Repeat("a", 64), Payload: map[string]any{"record_id": "record-1"}}},
		WorkflowIntents: []workflowmodel.WorkflowExecution{{ID: "workflow-1", WorkflowKey: "on_create", Trigger: "record_created:action_atomic_record", Status: "pending", ActionType: "record", ObjectKey: object.Key, RecordID: "record-1", ActorID: "admin", IdempotencyKey: "atomic-commit", MaxAttempts: 3, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}},
	}
	completed, err := commitBusinessActionExecution(t.Context(), repository, []transactionmodel.RecordMutationCommit{commit}, actionmodel.ActionExecutionCompletion{
		Execution:   claim.Execution,
		ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: map[string]any{"record_id": "record-1"}, ResponseStatus: 200,
		AuditEvents: []auditmodel.AuditEvent{{ID: "action-audit-1", Event: "business_action_executed", ObjectKey: object.Key, RecordID: "record-1", ActorID: "admin", Summary: "action completed", CreatedAt: now.Format(time.RFC3339Nano)}},
		ExpiresAt:   now.Add(24 * time.Hour), Now: now,
	})
	if err != nil || completed.Status != string(idempotency.StatusSucceeded) {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	for tableAndID, expected := range map[[2]string]int{{"action_atomic_record", "record-1"}: 1, {"_audit_events", "audit-1"}: 1, {"_audit_events", "action-audit-1"}: 1, {"integration_outbox_messages", "durable_intent:execution-1:0"}: 1, {"_workflow_executions", "workflow-1"}: 1} {
		table, id := tableAndID[0], tableAndID[1]
		var count int
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM "+store.Identifier(table)+" WHERE id = "+store.Placeholder(1), id).Scan(&count); err != nil || count != expected {
			t.Fatalf("table=%s count=%d err=%v", table, count, err)
		}
	}
	var connectorKey, connectionKey, operationKey, contractSHA256 string
	if err := store.DB().QueryRow("SELECT connector_key, connection_key, operation, request_fingerprint FROM "+store.Identifier("integration_outbox_messages")+" WHERE id = "+store.Placeholder(1), "durable_intent:execution-1:0").Scan(&connectorKey, &connectionKey, &operationKey, &contractSHA256); err != nil {
		t.Fatal(err)
	}
	if connectorKey != "webhook" || connectionKey != "primary" || operationKey != "notify" || contractSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("persisted Action intent identity=%q/%q/%q hash=%q", connectorKey, connectionKey, operationKey, contractSHA256)
	}
	replay, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{Execution: claim.Execution, RequestFingerprint: "fingerprint", LeaseOwner: "runtime-b", LeaseTTL: time.Minute, Now: now.Add(time.Second)})
	if err != nil || replay.Decision != idempotency.DecisionReplay || replay.Execution.Result["record_id"] != "record-1" {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
}

func TestBookClassPersistsClassBookingAuditsOutboxAndReceiptInOneTransaction(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE p8_group_class (
		workspace_id TEXT NOT NULL,
		id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		remaining_capacity INTEGER NOT NULL,
		remaining_waitlist_capacity INTEGER NOT NULL,
		UNIQUE (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE p8_class_booking (
		workspace_id TEXT NOT NULL,
		id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		class_id TEXT NOT NULL,
		member_id TEXT NOT NULL,
		status TEXT NOT NULL,
		UNIQUE (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	stamp := now.Format(time.RFC3339Nano)
	groupClass := definitionmodel.ObjectSchema{Key: "p8_group_class", Fields: []definitionmodel.FieldSchema{
		{Key: "remaining_capacity", Type: "integer"},
		{Key: "remaining_waitlist_capacity", Type: "integer"},
	}}
	classBooking := definitionmodel.ObjectSchema{Key: "p8_class_booking", Fields: []definitionmodel.FieldSchema{
		{Key: "class_id", Type: "relation"},
		{Key: "member_id", Type: "relation"},
		{Key: "status", Type: "select"},
	}}
	records := recordpersistence.NewRecordStore(store)
	if err := records.InsertRecord(t.Context(), "workspace-a", groupClass, recordmodel.Record{
		ID: "class-1", CreatedAt: stamp, UpdatedAt: stamp,
		Data: map[string]any{"remaining_capacity": int64(1), "remaining_waitlist_capacity": int64(1)},
	}); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
		Execution: actionmodel.ActionBusinessExecution{
			WorkspaceID: "workspace-a", ObjectKey: groupClass.Key,
			ActionKey: "group_class.book_class", IdempotencyKey: "book-class-uow-1",
			ActorID: "member-1", RoleKey: "member",
		},
		RequestFingerprint: "book-class-fingerprint", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	classAuditID := "book-class-class-audit-1"
	bookingAuditID := "book-class-booking-audit-1"
	actionAuditID := "book-class-action-audit-1"
	outboxID := "durable_intent:" + claim.Execution.ID + ":0"
	notificationID := "notification:" + claim.Execution.ID
	notificationWakeups := store.WorkerWakeups().Subscribe("notification_inbox", 1)
	commits := []transactionmodel.RecordMutationCommit{
		{
			Operation: "update", Object: groupClass,
			Record: recordmodel.Record{
				ID: "class-1", CreatedAt: stamp, UpdatedAt: now.Add(time.Second).Format(time.RFC3339Nano),
				Data: map[string]any{"remaining_capacity": int64(0), "remaining_waitlist_capacity": int64(1)},
			},
			Predicates: []transactionmodel.MutationPredicate{{
				Field: "remaining_capacity", Operator: "gte", Value: int64(1), ErrorCode: "gym.class_capacity_full",
			}},
			Audit: &auditmodel.AuditEvent{
				ID: classAuditID, WorkspaceID: "workspace-a", Event: "record_updated",
				ObjectKey: groupClass.Key, RecordID: "class-1", ActorID: "member-1", CreatedAt: stamp,
			},
		},
		{
			Operation: "create", Object: classBooking,
			Record: recordmodel.Record{
				ID: "booking-1", CreatedAt: stamp, UpdatedAt: stamp,
				Data: map[string]any{"class_id": "class-1", "member_id": "member-1", "status": "booked"},
			},
			Audit: &auditmodel.AuditEvent{
				ID: bookingAuditID, WorkspaceID: "workspace-a", Event: "record_created",
				ObjectKey: classBooking.Key, RecordID: "booking-1", ActorID: "member-1", CreatedAt: stamp,
			},
			Outbox: []integrationmodel.IntegrationOutboxMessage{{
				ID: outboxID, WorkspaceID: "workspace-a",
				ConnectorKey: "member_center", ConnectionKey: "primary", Operation: "send_notice",
				RequestRef: claim.Execution.ID, DedupKey: claim.Execution.ID + ":intent:0",
				RequestFingerprint: strings.Repeat("c", 64),
				Payload:            map[string]any{"member_id": "member-1", "sequence": int64(1)},
			}},
			NotificationEvents: []notificationmodel.NotificationEvent{{
				ID: notificationID, WorkspaceID: "workspace-a", Source: "action", SourceEventID: claim.Execution.ID,
				EventType: "gym.class_booked", Status: "queued", CreatedAt: stamp, UpdatedAt: stamp,
			}},
		},
	}
	completed, err := commitBusinessActionExecution(t.Context(), repository, commits, actionmodel.ActionExecutionCompletion{
		Execution: claim.Execution, ExecutionID: claim.Execution.ID,
		LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: map[string]any{"booking_id": "booking-1", "status": "booked"}, ResponseStatus: 200,
		AuditEvents: []auditmodel.AuditEvent{{
			ID: actionAuditID, WorkspaceID: "workspace-a", Event: "gym.class_booked",
			ObjectKey: groupClass.Key, ActorID: "member-1", CreatedAt: stamp,
		}},
		ExpiresAt: now.Add(24 * time.Hour), Now: now.Add(time.Second),
	})
	if err != nil || completed.Status != string(idempotency.StatusSucceeded) ||
		completed.Result["booking_id"] != "booking-1" || completed.Result["status"] != "booked" {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	select {
	case locator := <-notificationWakeups:
		if locator != (workerplatform.DurableTaskLocator{QueueKind: "notification_inbox", WorkspaceID: "workspace-a", TaskID: notificationID}) {
			t.Fatalf("notification locator=%+v", locator)
		}
	default:
		t.Fatal("committed Action notification did not wake Inbox worker")
	}
	storedClass, found, err := records.GetRecord(t.Context(), "workspace-a", groupClass, "class-1")
	if err != nil || !found || storedClass.Data["remaining_capacity"] != int64(0) ||
		storedClass.Data["remaining_waitlist_capacity"] != int64(1) {
		t.Fatalf("stored class=%#v found=%v err=%v", storedClass, found, err)
	}
	storedBooking, found, err := records.GetRecord(t.Context(), "workspace-a", classBooking, "booking-1")
	if err != nil || !found || storedBooking.Data["class_id"] != "class-1" ||
		storedBooking.Data["member_id"] != "member-1" || storedBooking.Data["status"] != "booked" {
		t.Fatalf("stored booking=%#v found=%v err=%v", storedBooking, found, err)
	}
	for _, auditID := range []string{classAuditID, bookingAuditID, actionAuditID} {
		var count int
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM "+store.Identifier("_audit_events")+" WHERE id = "+store.Placeholder(1), auditID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("audit=%s count=%d err=%v", auditID, count, err)
		}
	}
	var connectorKey, connectionKey, operationKey, requestRef string
	if err := store.DB().QueryRow(
		"SELECT connector_key, connection_key, operation, request_ref FROM "+store.Identifier("integration_outbox_messages")+" WHERE id = "+store.Placeholder(1),
		outboxID,
	).Scan(&connectorKey, &connectionKey, &operationKey, &requestRef); err != nil {
		t.Fatal(err)
	}
	if connectorKey != "member_center" || connectionKey != "primary" ||
		operationKey != "send_notice" || requestRef != claim.Execution.ID {
		t.Fatalf("outbox=%q/%q/%q request_ref=%q", connectorKey, connectionKey, operationKey, requestRef)
	}
	receipt, found, err := repository.findExecutionByScope(
		t.Context(), "workspace-a", groupClass.Key, "", "group_class.book_class", "book-class-uow-1",
	)
	if err != nil || !found || receipt.Status != string(idempotency.StatusSucceeded) ||
		receipt.Result["booking_id"] != "booking-1" || receipt.Result["status"] != "booked" {
		t.Fatalf("receipt=%#v found=%v err=%v", receipt, found, err)
	}
}

func TestBusinessActionExecutionTransactionOwnsLockingReadAndFinalCommit(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE action_lazy_uow_record (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	object := definitionmodel.ObjectSchema{Key: "action_lazy_uow_record", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	records := recordpersistence.NewRecordStore(store)
	if err := records.InsertRecord(t.Context(), "workspace-a", object, recordmodel.Record{ID: "record-1", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"status": "pending"}}); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
		Execution:          actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: object.Key, RecordID: "record-1", ActionKey: "approve", IdempotencyKey: "lazy-uow"},
		RequestFingerprint: "fingerprint", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := repository.BeginExecutionTransaction(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	page, err := records.ListRecords(transaction.Context(t.Context()), "workspace-a", object, recordmodel.RecordListQuery{
		Page: 1, PageSize: 1, Filters: map[string]any{"id__in": []any{"record-1"}}, LockIntent: recordmodel.RecordQueryLockForUpdate,
	})
	if err != nil || len(page.Items) != 1 || page.Items[0].Data["status"] != "pending" {
		_ = transaction.RollBack(t.Context())
		t.Fatalf("locking page=%+v error=%v", page, err)
	}
	updated := page.Items[0]
	updated.Data["status"] = "approved"
	updated.UpdatedAt = now.Add(time.Second).Format(time.RFC3339Nano)
	completed, err := transaction.Commit(t.Context(), []transactionmodel.RecordMutationCommit{{
		Operation: "update", Object: object, Record: updated, Optimistic: transactionmodel.OptimisticPrecondition{ExpectedUpdatedAt: page.Items[0].UpdatedAt},
	}}, actionmodel.ActionExecutionCompletion{
		Execution: claim.Execution, ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: map[string]any{"status": "approved"}, ResponseStatus: 200, ExpiresAt: now.Add(time.Hour), Now: now.Add(time.Second),
	})
	if err != nil || completed.Status != string(idempotency.StatusSucceeded) {
		t.Fatalf("completion=%+v error=%v", completed, err)
	}
	stored, found, err := records.GetRecord(t.Context(), "workspace-a", object, "record-1")
	if err != nil || !found || stored.Data["status"] != "approved" {
		t.Fatalf("stored=%+v found=%v error=%v", stored, found, err)
	}
}

func TestBusinessActionExecutionTransactionReservesSQLiteWriterAtBegin(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE action_sqlite_lock_record (workspace_id TEXT NOT NULL, id TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO action_sqlite_lock_record (workspace_id, id, updated_at) VALUES ('workspace-a', 'record-1', 'before')`); err != nil {
		t.Fatal(err)
	}
	var sequence int
	var name, databasePath string
	if err := store.DB().QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &databasePath); err != nil {
		t.Fatal(err)
	}
	competing, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer competing.Close()
	if _, err := competing.Exec(`PRAGMA busy_timeout = 0`); err != nil {
		t.Fatal(err)
	}

	transaction, err := NewActionBusinessExecutionStore(store).BeginExecutionTransaction(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := competing.Exec(`UPDATE action_sqlite_lock_record SET updated_at = 'competing' WHERE workspace_id = 'workspace-a' AND id = 'record-1'`); err == nil {
		_ = transaction.RollBack(t.Context())
		t.Fatal("competing SQLite writer was not blocked by Action BEGIN IMMEDIATE")
	}
	if err := transaction.RollBack(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := competing.Exec(`UPDATE action_sqlite_lock_record SET updated_at = 'after' WHERE workspace_id = 'workspace-a' AND id = 'record-1'`); err != nil {
		t.Fatalf("competing writer remained blocked after rollback: %v", err)
	}
}

func TestBusinessActionExecutionStoreCommitsFailureReceiptAndDenialAuditAtomically(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	now := time.Date(2026, 7, 24, 19, 0, 0, 0, time.UTC)
	claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
		Execution: actionmodel.ActionBusinessExecution{
			WorkspaceID: "workspace-a", ObjectKey: "booking", RecordID: "booking-south",
			ActionKey: "booking.lock", IdempotencyKey: "scope-denied",
		},
		RequestFingerprint: "fingerprint", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	audit := auditmodel.AuditEvent{
		ID: "scope-denial-audit", WorkspaceID: "workspace-a", Event: "record_scope_access_denied",
		ObjectKey: "booking", RecordID: "booking-south", ActorID: "operator-a", RoleKey: "operator",
		Summary: "Record scope access denied", CreatedAt: now.Format(time.RFC3339Nano),
	}
	completed, err := commitBusinessActionExecution(t.Context(), repository, nil, actionmodel.ActionExecutionCompletion{
		Execution: claim.Execution, ExecutionID: claim.Execution.ID,
		LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: map[string]any{"status": "failed"}, ResponseStatus: 404,
		ErrorCode: "backend.record.not_found", AuditEvents: []auditmodel.AuditEvent{audit},
		ExpiresAt: now.Add(24 * time.Hour), Now: now.Add(time.Second),
	})
	if err != nil || completed.Status != string(idempotency.StatusFailedTerminal) ||
		completed.ErrorCode != "backend.record.not_found" || completed.ResponseStatus != 404 {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	var count int
	if err := store.DB().QueryRow(
		"SELECT COUNT(*) FROM "+store.Identifier("_audit_events")+" WHERE id = "+store.Placeholder(1),
		audit.ID,
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit count=%d err=%v", count, err)
	}
	replay, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
		Execution: claim.Execution, RequestFingerprint: "fingerprint", LeaseOwner: "runtime-b",
		LeaseTTL: time.Minute, Now: now.Add(2 * time.Second),
	})
	if err != nil || replay.Decision != idempotency.DecisionReplay ||
		replay.Execution.Status != string(idempotency.StatusFailedTerminal) ||
		replay.Execution.ErrorCode != "backend.record.not_found" {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
}

func TestBusinessActionExecutionStoreRollsBackFactsWhenAtomicCommitFails(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE action_atomic_rollback (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{Execution: actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: "action_atomic_rollback", ActionKey: "create", IdempotencyKey: "rollback"}, RequestFingerprint: "fingerprint", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "action_atomic_rollback", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	commits := []transactionmodel.RecordMutationCommit{
		{Operation: "create", Object: object, Record: recordmodel.Record{ID: "must-rollback", CreatedAt: "created", UpdatedAt: "created", Data: map[string]any{"status": "created"}}},
		{Operation: "unsupported", Object: object, Record: recordmodel.Record{ID: "invalid"}},
	}
	if _, err := commitBusinessActionExecution(t.Context(), repository, commits, actionmodel.ActionExecutionCompletion{Execution: claim.Execution, ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken, Result: map[string]any{"status": "should-not-commit"}, ResponseStatus: 200, ExpiresAt: now.Add(time.Hour), Now: now}); err == nil {
		t.Fatal("expected atomic commit failure")
	}
	var recordCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM action_atomic_rollback WHERE id = ?`, "must-rollback").Scan(&recordCount); err != nil || recordCount != 0 {
		t.Fatalf("record count=%d err=%v", recordCount, err)
	}
	current, found, err := repository.findExecutionByScope(t.Context(), "workspace-a", "action_atomic_rollback", "", "create", "rollback")
	if err != nil || !found || current.Status != string(idempotency.StatusProcessing) {
		t.Fatalf("receipt=%#v found=%v err=%v", current, found, err)
	}
}

func TestBusinessActionConditionalMutationCommitsPredicateFactsAndReceiptInOneUoW(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE action_capacity (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, reserved REAL, status TEXT, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO action_capacity (workspace_id, id, created_at, updated_at, reserved, status) VALUES (?, ?, ?, ?, ?, ?)`, "workspace-a", "class-1", "v1", "v1", 19, "open"); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	now := time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC)
	claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{Execution: actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: "action_capacity", RecordID: "class-1", ActionKey: "reserve", IdempotencyKey: "reserve-20"}, RequestFingerprint: "reserve-20", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "action_capacity", Fields: []definitionmodel.FieldSchema{{Key: "reserved", Type: "number"}, {Key: "status", Type: "text"}}}
	commit := transactionmodel.RecordMutationCommit{
		Operation: "update", Object: object, Record: recordmodel.Record{ID: "class-1", CreatedAt: "v1", UpdatedAt: "v2", Data: map[string]any{"reserved": 20.0, "status": "open"}},
		Predicates:      []transactionmodel.MutationPredicate{{Field: "reserved", Operator: "lt", Value: 20.0, ErrorCode: "capacity_full"}, {Field: "status", Operator: "eq", Value: "open", ErrorCode: "capacity_closed"}},
		Audit:           &auditmodel.AuditEvent{ID: "capacity-audit-success", Event: "capacity_reserved", ObjectKey: object.Key, RecordID: "class-1", CreatedAt: now.Format(time.RFC3339Nano)},
		Outbox:          []integrationmodel.IntegrationOutboxMessage{{ID: "capacity-outbox-success", WorkspaceID: "workspace-a", ConnectorKey: "test", Operation: "notify", DedupKey: "reserve-20", Payload: map[string]any{"record_id": "class-1"}}},
		WorkflowIntents: []workflowmodel.WorkflowExecution{{ID: "capacity-workflow-success", WorkflowKey: "capacity_reserved", Trigger: "record_updated:action_capacity", Status: "pending", ActionType: "workflow_graph", ObjectKey: object.Key, RecordID: "class-1", MaxAttempts: 3, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}},
	}
	completed, err := commitBusinessActionExecution(t.Context(), repository, []transactionmodel.RecordMutationCommit{commit}, actionmodel.ActionExecutionCompletion{Execution: claim.Execution, ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken, Result: map[string]any{"reserved": 20}, ResponseStatus: 200, ExpiresAt: now.Add(time.Hour), Now: now})
	if err != nil || completed.Status != string(idempotency.StatusSucceeded) {
		t.Fatalf("conditional completion=%#v err=%v", completed, err)
	}

	conflictClaim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{Execution: actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: object.Key, RecordID: "class-1", ActionKey: "reserve", IdempotencyKey: "reserve-21"}, RequestFingerprint: "reserve-21", LeaseOwner: "runtime-b", LeaseTTL: time.Minute, Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	commit.Record.UpdatedAt = "v3"
	commit.Record.Data["reserved"] = 21.0
	commit.Audit.ID = "capacity-audit-conflict"
	commit.Outbox[0].ID, commit.Outbox[0].DedupKey = "capacity-outbox-conflict", "reserve-21"
	commit.WorkflowIntents[0].ID = "capacity-workflow-conflict"
	_, err = commitBusinessActionExecution(t.Context(), repository, []transactionmodel.RecordMutationCommit{commit}, actionmodel.ActionExecutionCompletion{Execution: conflictClaim.Execution, ExecutionID: conflictClaim.Execution.ID, LeaseOwner: conflictClaim.Execution.LeaseOwner, FencingToken: conflictClaim.Execution.FencingToken, Result: map[string]any{"reserved": 21}, ResponseStatus: 200, ExpiresAt: now.Add(time.Hour), Now: now.Add(time.Second)})
	var businessConflict *mutation.PolicyConflictError
	if !errors.As(err, &businessConflict) || businessConflict.Code != "capacity_full" {
		t.Fatalf("conditional conflict=%#v err=%v", businessConflict, err)
	}
	var reserved float64
	if err := store.DB().QueryRow(`SELECT reserved FROM action_capacity WHERE workspace_id = ? AND id = ?`, "workspace-a", "class-1").Scan(&reserved); err != nil || reserved != 20 {
		t.Fatalf("reserved=%v err=%v", reserved, err)
	}
	for tableAndID := range map[[2]string]bool{{"_audit_events", "capacity-audit-conflict"}: true, {"integration_outbox_messages", "capacity-outbox-conflict"}: true, {"_workflow_executions", "capacity-workflow-conflict"}: true} {
		var count int
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM "+store.Identifier(tableAndID[0])+" WHERE id = ?", tableAndID[1]).Scan(&count); err != nil || count != 0 {
			t.Fatalf("predicate conflict leaked %s/%s count=%d err=%v", tableAndID[0], tableAndID[1], count, err)
		}
	}
	receipt, found, err := repository.findExecutionByScope(t.Context(), "workspace-a", object.Key, "class-1", "reserve", "reserve-21")
	if err != nil || !found || receipt.Status != string(idempotency.StatusProcessing) {
		t.Fatalf("conflict receipt=%#v found=%v err=%v", receipt, found, err)
	}
}

func TestBusinessActionExecutionStoreRollsBackAuditIntentOutboxAndReceiptOnCompletionFailure(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE action_completion_rollback (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	now := time.Date(2026, 7, 19, 16, 30, 0, 0, time.UTC)
	claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
		Execution:          actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: "action_completion_rollback", ActionKey: "create", IdempotencyKey: "completion-rollback"},
		RequestFingerprint: "fingerprint", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TRIGGER fail_action_receipt_completion BEFORE UPDATE OF status ON business_action_executions WHEN NEW.status = 'succeeded' BEGIN SELECT RAISE(ABORT, 'injected action receipt completion failure'); END`); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "action_completion_rollback", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	commit := transactionmodel.RecordMutationCommit{
		Operation: "create", Object: object,
		Record:          recordmodel.Record{ID: "record-rollback", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"status": "created"}},
		Audit:           &auditmodel.AuditEvent{ID: "audit-rollback", Event: "record_created", ObjectKey: object.Key, RecordID: "record-rollback", CreatedAt: now.Format(time.RFC3339Nano)},
		Outbox:          []integrationmodel.IntegrationOutboxMessage{{ID: "outbox-rollback", WorkspaceID: "workspace-a", ConnectorKey: "webhook", ConnectionKey: "primary", Operation: "notify", RequestRef: "completion-rollback", DedupKey: "completion-rollback", RequestFingerprint: "fingerprint", Payload: map[string]any{"record_id": "record-rollback"}}},
		WorkflowIntents: []workflowmodel.WorkflowExecution{{ID: "workflow-rollback", WorkflowKey: "on_create", Trigger: "record_created:action_completion_rollback", Status: "pending", ActionType: "record", ObjectKey: object.Key, RecordID: "record-rollback", IdempotencyKey: "completion-rollback", MaxAttempts: 3, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}},
	}
	if _, err := commitBusinessActionExecution(t.Context(), repository, []transactionmodel.RecordMutationCommit{commit}, actionmodel.ActionExecutionCompletion{
		Execution:   claim.Execution,
		ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken, Result: map[string]any{"record_id": "record-rollback"}, ResponseStatus: 200,
		AuditEvents: []auditmodel.AuditEvent{{ID: "action-audit-rollback", Event: "business_action_executed", ObjectKey: object.Key, RecordID: "record-rollback", CreatedAt: now.Format(time.RFC3339Nano)}},
		ExpiresAt:   now.Add(time.Hour), Now: now,
	}); err == nil {
		t.Fatal("expected injected receipt completion failure")
	}
	for tableAndID := range map[[2]string]bool{{"action_completion_rollback", "record-rollback"}: true, {"_audit_events", "audit-rollback"}: true, {"_audit_events", "action-audit-rollback"}: true, {"integration_outbox_messages", "outbox-rollback"}: true, {"_workflow_executions", "workflow-rollback"}: true} {
		table, id := tableAndID[0], tableAndID[1]
		var count int
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM "+store.Identifier(table)+" WHERE id = "+store.Placeholder(1), id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("half commit table=%s count=%d err=%v", table, count, err)
		}
	}
	receipt, found, err := repository.findExecutionByScope(t.Context(), "workspace-a", object.Key, "", "create", "completion-rollback")
	if err != nil || !found || receipt.Status != string(idempotency.StatusProcessing) {
		t.Fatalf("receipt=%+v found=%v err=%v", receipt, found, err)
	}
}

func TestBusinessActionExecutionStoreRollsBackMutationAndDurableIntentWhenActionAuditFails(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE action_audit_rollback (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	repository := NewActionBusinessExecutionStore(store)
	now := time.Date(2026, 7, 22, 15, 0, 0, 0, time.UTC)
	claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
		Execution:          actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: "action_audit_rollback", ActionKey: "create", IdempotencyKey: "audit-rollback"},
		RequestFingerprint: "fingerprint", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicateAudit := auditmodel.AuditEvent{ID: "action-audit-duplicate", WorkspaceID: "workspace-a", Event: "existing_event", CreatedAt: now.Format(time.RFC3339Nano)}
	if err := auditpersistence.NewAuditStore(store).InsertAuditEvent(t.Context(), "workspace-a", duplicateAudit); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "action_audit_rollback", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	commit := transactionmodel.RecordMutationCommit{
		Operation: "create", Object: object,
		Record: recordmodel.Record{ID: "record-audit-rollback", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano), Data: map[string]any{"status": "created"}},
		Audit:  &auditmodel.AuditEvent{ID: "mutation-audit-rollback", Event: "record_created", ObjectKey: object.Key, RecordID: "record-audit-rollback", CreatedAt: now.Format(time.RFC3339Nano)},
		Outbox: []integrationmodel.IntegrationOutboxMessage{{ID: "durable-intent-rollback", WorkspaceID: "workspace-a", ConnectorKey: "email", ConnectionKey: "primary", Operation: "send", RequestRef: "audit-rollback", DedupKey: "audit-rollback", RequestFingerprint: "fingerprint", Payload: map[string]any{"record_id": "record-audit-rollback"}}},
	}
	if _, err := commitBusinessActionExecution(t.Context(), repository, []transactionmodel.RecordMutationCommit{commit}, actionmodel.ActionExecutionCompletion{
		Execution: claim.Execution, ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: map[string]any{"record_id": "record-audit-rollback"}, ResponseStatus: 200,
		AuditEvents: []auditmodel.AuditEvent{{ID: duplicateAudit.ID, WorkspaceID: "workspace-a", Event: "business_action_executed", ObjectKey: object.Key, RecordID: "record-audit-rollback", CreatedAt: now.Format(time.RFC3339Nano)}},
		ExpiresAt:   now.Add(time.Hour), Now: now,
	}); err == nil {
		t.Fatal("expected duplicate Action audit to fail the transaction")
	}
	for tableAndID := range map[[2]string]bool{{"action_audit_rollback", "record-audit-rollback"}: true, {"_audit_events", "mutation-audit-rollback"}: true, {"integration_outbox_messages", "durable-intent-rollback"}: true} {
		var count int
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM "+store.Identifier(tableAndID[0])+" WHERE id = "+store.Placeholder(1), tableAndID[1]).Scan(&count); err != nil || count != 0 {
			t.Fatalf("Action audit failure leaked %s/%s count=%d err=%v", tableAndID[0], tableAndID[1], count, err)
		}
	}
	var duplicateCount int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM "+store.Identifier("_audit_events")+" WHERE id = "+store.Placeholder(1), duplicateAudit.ID).Scan(&duplicateCount); err != nil || duplicateCount != 1 {
		t.Fatalf("pre-existing duplicate audit count=%d err=%v", duplicateCount, err)
	}
	receipt, found, err := repository.findExecutionByScope(t.Context(), "workspace-a", object.Key, "", "create", "audit-rollback")
	if err != nil || !found || receipt.Status != string(idempotency.StatusProcessing) {
		t.Fatalf("receipt=%+v found=%v err=%v", receipt, found, err)
	}
}
