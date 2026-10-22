package action

import (
	"fmt"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"strings"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/idempotency"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

func TestBookClassRollsBackEveryFactWhenAnyAtomicStageFails(t *testing.T) {
	for _, stage := range []string{
		"class_mutation",
		"booking_mutation",
		"class_audit",
		"booking_audit",
		"outbox",
		"action_audit",
		"receipt",
	} {
		t.Run(stage, func(t *testing.T) {
			testBookClassAtomicStageRollback(t, stage)
		})
	}
}

func testBookClassAtomicStageRollback(t *testing.T, stage string) {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE p8_fault_group_class (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace_id TEXT NOT NULL,
			p8_fault_group_class_id TEXT NOT NULL UNIQUE,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			remaining_capacity INTEGER NOT NULL,
			remaining_waitlist_capacity INTEGER NOT NULL,
			UNIQUE (workspace_id, p8_fault_group_class_id)
		)`,
		`CREATE TABLE p8_fault_class_booking (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workspace_id TEXT NOT NULL,
			p8_fault_class_booking_id TEXT NOT NULL UNIQUE,
			created_at BIGINT NOT NULL,
			updated_at BIGINT NOT NULL,
			class_id TEXT NOT NULL,
			member_id TEXT NOT NULL,
			status TEXT NOT NULL,
			UNIQUE (workspace_id, p8_fault_class_booking_id)
		)`,
	} {
		if _, err := store.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 7, 23, 16, 0, 0, 0, time.UTC)
	stamp := now.Format(time.RFC3339Nano)
	groupClass := definitionmodel.ObjectSchema{Key: "p8_fault_group_class", Fields: []definitionmodel.FieldSchema{
		{Key: "remaining_capacity", Type: "integer"},
		{Key: "remaining_waitlist_capacity", Type: "integer"},
	}}
	classBooking := definitionmodel.ObjectSchema{Key: "p8_fault_class_booking", Fields: []definitionmodel.FieldSchema{
		{Key: "class_id", Type: "relation"},
		{Key: "member_id", Type: "relation"},
		{Key: "status", Type: "select"},
	}}
	records := recordpersistence.NewRecordStore(store)
	if err := records.InsertRecord(t.Context(), "workspace-a", groupClass, recordmodel.Record{
		RecordID: "class-1", CreatedAt: stamp, UpdatedAt: stamp,
		Data: map[string]any{"remaining_capacity": int64(1), "remaining_waitlist_capacity": int64(1)},
	}); err != nil {
		t.Fatal(err)
	}

	repository := NewActionBusinessExecutionStore(store)
	claim, err := repository.TryBeginExecution(t.Context(), actionmodel.ActionExecutionClaimRequest{
		Execution: actionmodel.ActionBusinessExecution{
			WorkspaceID: "workspace-a", ObjectKey: groupClass.Key,
			ActionKey: "group_class.book_class", IdempotencyKey: "book-class-fault-" + stage,
			ActorID: "member-1", RoleKey: "member",
		},
		RequestFingerprint: "book-class-fault-fingerprint", LeaseOwner: "runtime-a",
		LeaseTTL: time.Minute, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	classAuditID := actionAuditTestID("p8-fault-class-audit")
	bookingAuditID := actionAuditTestID("p8-fault-booking-audit")
	actionAuditID := actionAuditTestID("p8-fault-action-audit")
	outboxID := "durable_intent:" + claim.Execution.OperationID + ":0"
	if _, err := store.DB().Exec(bookClassFailureTriggerSQL(
		stage,
		groupClass.Key,
		classBooking.Key,
		classAuditID,
		bookingAuditID,
		actionAuditID,
		outboxID,
	)); err != nil {
		t.Fatalf("install %s fault: %v", stage, err)
	}

	commits := []transactionmodel.RecordMutationCommit{
		{
			Operation: "update", Object: groupClass,
			Record: recordmodel.Record{
				RecordID: "class-1", CreatedAt: stamp, UpdatedAt: now.Add(time.Second).Format(time.RFC3339Nano),
				Data: map[string]any{"remaining_capacity": int64(0), "remaining_waitlist_capacity": int64(1)},
			},
			Predicates: []transactionmodel.MutationPredicate{{
				Field: "remaining_capacity", Operator: "gte", Value: int64(1), ErrorCode: "gym.class_capacity_full",
			}},
			Audit: &auditmodel.AuditEvent{
				AuditEventID: classAuditID, WorkspaceID: "workspace-a", Family: auditmodel.EventFamilyBusinessEntity, Event: "record_updated",
				ObjectKey: groupClass.Key, RecordID: "class-1", ActorID: "member-1", CreatedAt: stamp,
			},
		},
		{
			Operation: "create", Object: classBooking,
			Record: recordmodel.Record{
				RecordID: "booking-1", CreatedAt: stamp, UpdatedAt: stamp,
				Data: map[string]any{"class_id": "class-1", "member_id": "member-1", "status": "booked"},
			},
			Audit: &auditmodel.AuditEvent{
				AuditEventID: bookingAuditID, WorkspaceID: "workspace-a", Family: auditmodel.EventFamilyBusinessEntity, Event: "record_created",
				ObjectKey: classBooking.Key, RecordID: "booking-1", ActorID: "member-1", CreatedAt: stamp,
			},
			Outbox: []publicationmodel.Message{{
				MessageID: outboxID, WorkspaceID: "workspace-a",
				ConnectorKey: "member_center", ConnectionKey: "primary", Operation: "send_notice",
				RequestRef: claim.Execution.OperationID, DedupKey: claim.Execution.OperationID + ":intent:0",
				RequestFingerprint: strings.Repeat("c", 64),
				Payload:            map[string]any{"member_id": "member-1", "sequence": int64(1)},
			}},
		},
	}
	if _, err := commitBusinessActionExecution(t.Context(), repository, commits, actionmodel.ActionExecutionCompletion{
		Execution: claim.Execution, OperationID: claim.Execution.OperationID,
		LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: map[string]any{"booking_id": "booking-1", "status": "booked"}, ResponseStatus: 200,
		AuditEvents: []auditmodel.AuditEvent{{
			AuditEventID: actionAuditID, WorkspaceID: "workspace-a", Family: auditmodel.EventFamilyBusinessEntity, Event: "gym.class_booked",
			ObjectKey: groupClass.Key, ActorID: "member-1", CreatedAt: stamp,
		}},
		ExpiresAt: now.Add(24 * time.Hour), Now: now.Add(time.Second),
	}); err == nil {
		t.Fatalf("%s fault unexpectedly committed", stage)
	}

	storedClass, found, err := records.GetRecord(t.Context(), "workspace-a", groupClass, "class-1")
	if err != nil || !found ||
		storedClass.Data["remaining_capacity"] != int64(1) ||
		storedClass.Data["remaining_waitlist_capacity"] != int64(1) {
		t.Fatalf("%s stored class=%#v found=%v error=%v", stage, storedClass, found, err)
	}
	if _, found, err := records.GetRecord(t.Context(), "workspace-a", classBooking, "booking-1"); err != nil || found {
		t.Fatalf("%s booking found=%v error=%v", stage, found, err)
	}
	for table, ids := range map[string][]string{
		"_audit_events":       {classAuditID, bookingAuditID, actionAuditID},
		"_publication_outbox": {outboxID},
	} {
		for _, id := range ids {
			var count int
			idColumn := "id"
			if table == "_publication_outbox" {
				idColumn = "message_id"
			} else if table == "_audit_events" {
				idColumn = "audit_event_id"
			}
			if err := store.DB().QueryRow(
				"SELECT COUNT(*) FROM "+store.Identifier(table)+" WHERE "+store.Identifier(idColumn)+" = "+store.Placeholder(1),
				id,
			).Scan(&count); err != nil || count != 0 {
				t.Fatalf("%s leaked %s/%s count=%d error=%v", stage, table, id, count, err)
			}
		}
	}
	receipt, found, err := repository.findExecutionByScope(
		t.Context(), "workspace-a", groupClass.Key, "", "group_class.book_class", "book-class-fault-"+stage,
	)
	if err != nil || !found || receipt.Status != string(idempotency.StatusProcessing) ||
		receipt.ResponseStatus != 0 || receipt.ErrorCode != "" || len(receipt.Result) != 0 {
		t.Fatalf("%s receipt=%#v found=%v error=%v", stage, receipt, found, err)
	}
}

func bookClassFailureTriggerSQL(stage, groupClass, classBooking, classAuditID, bookingAuditID, actionAuditID, outboxID string) string {
	switch stage {
	case "class_mutation":
		return fmt.Sprintf(
			`CREATE TRIGGER p8_fail_class_mutation BEFORE UPDATE ON %s
			BEGIN SELECT RAISE(ABORT, 'injected class mutation failure'); END`,
			quoteSQLiteTestIdentifier(groupClass),
		)
	case "booking_mutation":
		return fmt.Sprintf(
			`CREATE TRIGGER p8_fail_booking_mutation BEFORE INSERT ON %s
			BEGIN SELECT RAISE(ABORT, 'injected booking mutation failure'); END`,
			quoteSQLiteTestIdentifier(classBooking),
		)
	case "class_audit":
		return bookClassIDFailureTrigger("p8_fail_class_audit", "_audit_events", classAuditID)
	case "booking_audit":
		return bookClassIDFailureTrigger("p8_fail_booking_audit", "_audit_events", bookingAuditID)
	case "outbox":
		return bookClassIDFailureTrigger("p8_fail_outbox", "_publication_outbox", outboxID)
	case "action_audit":
		return bookClassIDFailureTrigger("p8_fail_action_audit", "_audit_events", actionAuditID)
	case "receipt":
		return `CREATE TRIGGER p8_fail_receipt BEFORE UPDATE OF status ON _operations
			WHEN NEW.owner = 'action' AND NEW.status = 'succeeded'
			BEGIN SELECT RAISE(ABORT, 'injected receipt failure'); END`
	default:
		panic("unsupported book class failure stage: " + stage)
	}
}

func bookClassIDFailureTrigger(name, table, id string) string {
	idColumn := "id"
	if table == "_publication_outbox" {
		idColumn = "message_id"
	} else if table == "_audit_events" {
		idColumn = "audit_event_id"
	}
	return fmt.Sprintf(
		`CREATE TRIGGER %s BEFORE INSERT ON %s
		WHEN NEW.%s = %s
		BEGIN SELECT RAISE(ABORT, 'injected %s failure'); END`,
		quoteSQLiteTestIdentifier(name),
		quoteSQLiteTestIdentifier(table),
		quoteSQLiteTestIdentifier(idColumn),
		quoteSQLiteTestString(id),
		stageLabel(name),
	)
}

func quoteSQLiteTestIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteSQLiteTestString(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}

func stageLabel(triggerName string) string {
	return strings.TrimPrefix(triggerName, "p8_fail_")
}
