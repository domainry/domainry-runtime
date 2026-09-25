package record_test

import (
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"github.com/domainry/domainry-foundation/mutation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"

	"testing"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/testsupport/auditmodulefixture"
)

func TestContextRecordMutationDialectContracts(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			store := openStoreForGeneratedListTest(t)
			defer store.Close()
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatalf("ensure runtime schema: %v", err)
			}
			auditmodulefixture.Bind(t, t.Context(), store)
			if err := store.SetEngineForTesting(driver); err != nil {
				t.Fatal(err)
			}
			if options := (&sql.TxOptions{Isolation: sql.LevelSerializable}); options.Isolation != sql.LevelSerializable || options.ReadOnly {
				t.Fatalf("unexpected record UoW transaction options: %#v", options)
			}
			object := definitionmodel.ObjectSchema{Key: "dialect_record", Name: "Dialect Record", Fields: []definitionmodel.FieldSchema{{Key: "status", Name: "Status", Type: "text"}}}
			if _, err := store.DB().Exec(`CREATE TABLE dialect_record (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT, UNIQUE (workspace_id, id))`); err != nil {
				t.Fatalf("create table: %v", err)
			}
			repository := store
			for _, id := range []string{"first", "second"} {
				if err := recordStore(repository).InsertRecord(t.Context(), "workspace-primary", object, recordmodel.Record{ID: id, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"status": "pending"}}); err != nil {
					t.Fatalf("insert %s: %v", id, err)
				}
			}

			conflicting := []transactionmodel.RecordMutationCommit{
				{Operation: "update", Object: object, Record: recordmodel.Record{ID: "first", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"status": "approved"}}, ExpectedUpdatedAt: "2026-01-01T00:00:00Z", Audit: dialectAudit(driver, "first-conflict"), Outbox: []publicationmodel.Message{dialectOutbox(driver, "conflict")}, WorkflowIntents: []workflowmodel.WorkflowExecution{dialectWorkflowIntent(driver, "conflict")}},
				{Operation: "update", Object: object, Record: recordmodel.Record{ID: "second", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"status": "approved"}}, ExpectedUpdatedAt: "2025-12-31T00:00:00Z", Audit: dialectAudit(driver, "second-conflict")},
			}
			if err := recordStore(repository).CommitRecordMutationBatch(t.Context(), "workspace-primary", conflicting); !mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
				t.Fatalf("expected typed optimistic concurrency conflict, got %v", err)
			}
			assertDialectRecords(t, recordStore(repository), object, "pending", "2026-01-01T00:00:00Z")
			assertDialectAuditCount(t, store, driver, 0)
			assertDialectWorkflowIntentCount(t, store, driver, 0)
			assertDialectOutboxCount(t, store, driver, 0)

			successful := []transactionmodel.RecordMutationCommit{
				{Operation: "update", Object: object, Record: recordmodel.Record{ID: "first", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"status": "approved"}}, ExpectedUpdatedAt: "2026-01-01T00:00:00Z", Audit: dialectAudit(driver, "first-success"), Audits: []auditmodel.AuditEvent{*dialectAudit(driver, "first-mandatory-domain-event")}, Outbox: []publicationmodel.Message{dialectOutbox(driver, "success")}, WorkflowIntents: []workflowmodel.WorkflowExecution{dialectWorkflowIntent(driver, "success")}},
				{Operation: "update", Object: object, Record: recordmodel.Record{ID: "second", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"status": "approved"}}, ExpectedUpdatedAt: "2026-01-01T00:00:00Z", Audit: dialectAudit(driver, "second-success")},
			}
			if err := recordStore(repository).CommitRecordMutationBatch(t.Context(), "workspace-primary", successful); err != nil {
				t.Fatalf("commit successful batch: %v", err)
			}
			assertDialectRecords(t, recordStore(repository), object, "approved", "2026-01-02T00:00:00Z")
			assertDialectAuditCount(t, store, driver, 3)
			assertDialectWorkflowIntentCount(t, store, driver, 1)
			assertDialectOutboxCount(t, store, driver, 1)

			duplicateRecord := transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: recordmodel.Record{ID: "first", CreatedAt: "2026-01-03T00:00:00Z", UpdatedAt: "2026-01-03T00:00:00Z", Data: map[string]any{"status": "duplicate"}}}
			if err := recordStore(repository).CommitRecordMutation(t.Context(), "workspace-primary", duplicateRecord); !mutation.IsMutationConflict(err, mutation.MutationConflictUnique) {
				t.Fatalf("expected typed unique conflict, got %v", err)
			}
			duplicateIntent := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: recordmodel.Record{ID: "first", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-03T00:00:00Z", Data: map[string]any{"status": "approved"}}, ExpectedUpdatedAt: "2026-01-02T00:00:00Z", WorkflowIntents: []workflowmodel.WorkflowExecution{dialectWorkflowIntent(driver, "success")}}
			if err := recordStore(repository).CommitRecordMutation(t.Context(), "workspace-primary", duplicateIntent); !mutation.IsMutationConflict(err, mutation.MutationConflictIdempotency) {
				t.Fatalf("expected typed idempotency conflict, got %v", err)
			}
			record, found, err := recordStore(repository).GetRecord(t.Context(), "workspace-primary", object, "first")
			if err != nil || !found || record.UpdatedAt != "2026-01-02T00:00:00Z" {
				t.Fatalf("idempotency conflict did not roll back record update: %#v found=%v err=%v", record, found, err)
			}
		})
	}
}

func dialectAudit(driver, suffix string) *auditmodel.AuditEvent {
	return &auditmodel.AuditEvent{ID: fmt.Sprintf("audit_%s_%s", driver, suffix), Family: auditmodel.EventFamilyBusinessRecord, Event: "record_updated", ObjectKey: "dialect_record", RecordID: suffix, CreatedAt: "2026-01-02T00:00:00Z"}
}

func dialectWorkflowIntent(driver, suffix string) workflowmodel.WorkflowExecution {
	return workflowmodel.WorkflowExecution{ID: fmt.Sprintf("workflow_intent_%s_%s", driver, suffix), WorkflowKey: "notify", Name: "Notify", Trigger: "record_updated:dialect_record", Status: "pending", ActionType: "workflow_graph", Action: map[string]any{}, Payload: map[string]any{"record_id": "first"}, Result: map[string]any{"transactional_intent": true}, ObjectKey: "dialect_record", RecordID: "first", ActorID: "tester", Attempt: 0, MaxAttempts: 3, Message: "workflow.message.queued", CreatedAt: "2026-01-02T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z"}
}

func dialectOutbox(driver, suffix string) publicationmodel.Message {
	return publicationmodel.Message{ID: fmt.Sprintf("outbox_%s_%s", driver, suffix), WorkspaceID: "workspace-primary", ConnectorKey: "test", Operation: "notify", DedupKey: "dialect:" + driver + ":" + suffix, Status: "queued", Payload: map[string]any{"record_id": "first"}, CreatedBy: "tester"}
}

func assertDialectRecords(t *testing.T, repository interface {
	GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error)
}, object definitionmodel.ObjectSchema, status, updatedAt string) {
	t.Helper()
	for _, id := range []string{"first", "second"} {
		record, found, err := repository.GetRecord(t.Context(), "workspace-primary", object, id)
		if err != nil || !found {
			t.Fatalf("get %s: found=%v err=%v", id, found, err)
		}
		if record.Data["status"] != status || record.UpdatedAt != updatedAt {
			t.Fatalf("unexpected %s after mutation: %#v", id, record)
		}
	}
}

func assertDialectAuditCount(t *testing.T, store *RuntimeStore, driver string, expected int) {
	t.Helper()
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM _audit_events WHERE id LIKE ?`, "audit_"+driver+"_%").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("expected %d committed audit(s), got %d", expected, count)
	}
}

func assertDialectWorkflowIntentCount(t *testing.T, store *RuntimeStore, driver string, expected int) {
	t.Helper()
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM _workflow_executions WHERE id LIKE ?`, "workflow_intent_"+driver+"_%").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("expected %d committed workflow intent(s), got %d", expected, count)
	}
}

func assertDialectOutboxCount(t *testing.T, store *RuntimeStore, driver string, expected int) {
	t.Helper()
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM _publication_outbox WHERE id LIKE ?`, "outbox_"+driver+"_%").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("expected %d committed outbox message(s), got %d", expected, count)
	}
}

func TestCommitRecordMutationBatchRollsBackEveryRecordOnConflict(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure runtime schema: %v", err)
	}
	auditmodulefixture.Bind(t, t.Context(), store)
	object := definitionmodel.ObjectSchema{Key: "atomic_record", Name: "Atomic Record", Fields: []definitionmodel.FieldSchema{{Key: "status", Name: "Status", Type: "text"}}}
	if _, err := store.DB().Exec(`CREATE TABLE atomic_record (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for _, id := range []string{"first", "second"} {
		if err := recordStore(store).InsertRecord(t.Context(), "workspace-primary", object, recordmodel.Record{ID: id, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"status": "pending"}}); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	commits := []transactionmodel.RecordMutationCommit{
		{Operation: "update", Object: object, Record: recordmodel.Record{ID: "first", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"status": "approved"}}, ExpectedUpdatedAt: "2026-01-01T00:00:00Z"},
		{Operation: "update", Object: object, Record: recordmodel.Record{ID: "second", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"status": "approved"}}, ExpectedUpdatedAt: "2025-12-31T00:00:00Z"},
	}
	if err := recordStore(store).CommitRecordMutationBatch(t.Context(), "workspace-primary", commits); !mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
		t.Fatalf("expected typed optimistic concurrency conflict, got %v", err)
	}
	for _, id := range []string{"first", "second"} {
		record, ok, err := recordStore(store).GetRecord(t.Context(), "workspace-primary", object, id)
		if err != nil || !ok {
			t.Fatalf("get %s: ok=%v err=%v", id, ok, err)
		}
		if record.Data["status"] != "pending" || record.UpdatedAt != "2026-01-01T00:00:00Z" {
			t.Fatalf("expected full rollback for %s, got %#v", id, record)
		}
	}
}

func TestRecordMutationAuthorizationScopeGuardsUpdateDeleteAndWholeBatch(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	auditmodulefixture.Bind(t, t.Context(), store)
	object := definitionmodel.ObjectSchema{Key: "scoped_mutation", Fields: []definitionmodel.FieldSchema{
		{Key: "owner_user_id", Type: "text"},
		{Key: "status", Type: "text"},
	}}
	if _, err := store.DB().Exec(`CREATE TABLE scoped_mutation (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, owner_user_id TEXT, status TEXT, PRIMARY KEY (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	repository := recordStore(store)
	for _, candidate := range []recordmodel.Record{
		{ID: "owned", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"owner_user_id": "user-a", "status": "pending"}},
		{ID: "foreign", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"owner_user_id": "user-b", "status": "pending"}},
	} {
		if err := repository.InsertRecord(t.Context(), "workspace-primary", object, candidate); err != nil {
			t.Fatal(err)
		}
	}
	scope := &recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "owner_user_id", Values: []string{"user-a"}}
	update := func(id, version, status string) transactionmodel.RecordMutationCommit {
		return transactionmodel.RecordMutationCommit{
			Operation: "update", Object: object, AuthorizationScope: scope,
			Record:            recordmodel.Record{ID: id, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: version, Data: map[string]any{"owner_user_id": map[string]string{"owned": "user-a", "foreign": "user-b"}[id], "status": status}},
			ExpectedUpdatedAt: "2026-01-01T00:00:00Z",
		}
	}

	assertOutsideScope := func(err error) {
		t.Helper()
		var conflict *mutation.PolicyConflictError
		if !errors.As(err, &conflict) || conflict.Code != "backend.record.outside_scope" || conflict.Field != "authorization_scope" {
			t.Fatalf("expected typed authorization-scope conflict, conflict=%#v err=%v", conflict, err)
		}
	}

	assertOutsideScope(repository.CommitRecordMutation(t.Context(), "workspace-primary", update("foreign", "2026-01-02T00:00:00Z", "forged-update")))
	foreign, found, err := repository.GetRecord(t.Context(), "workspace-primary", object, "foreign")
	if err != nil || !found || foreign.Data["status"] != "pending" || foreign.UpdatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("out-of-scope update changed row: record=%#v found=%v err=%v", foreign, found, err)
	}

	assertOutsideScope(repository.CommitRecordMutation(t.Context(), "workspace-primary", transactionmodel.RecordMutationCommit{
		Operation: "delete", Object: object, RecordID: "foreign", Record: foreign, ExpectedUpdatedAt: "2026-01-01T00:00:00Z", AuthorizationScope: scope,
	}))
	if _, found, err := repository.GetRecord(t.Context(), "workspace-primary", object, "foreign"); err != nil || !found {
		t.Fatalf("out-of-scope delete removed row: found=%v err=%v", found, err)
	}

	assertOutsideScope(repository.CommitRecordMutationBatch(t.Context(), "workspace-primary", []transactionmodel.RecordMutationCommit{
		update("owned", "2026-01-02T00:00:00Z", "approved"),
		update("foreign", "2026-01-02T00:00:00Z", "forged-batch-update"),
	}))
	owned, found, err := repository.GetRecord(t.Context(), "workspace-primary", object, "owned")
	if err != nil || !found || owned.Data["status"] != "pending" || owned.UpdatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("authorization failure did not roll back whole batch: record=%#v found=%v err=%v", owned, found, err)
	}
}

func TestConditionalMutationPredicateIsAtomicAcrossDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			store := openStoreForGeneratedListTest(t)
			defer store.Close()
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			auditmodulefixture.Bind(t, t.Context(), store)
			if err := store.SetEngineForTesting(driver); err != nil {
				t.Fatal(err)
			}
			object := definitionmodel.ObjectSchema{Key: "capacity", Fields: []definitionmodel.FieldSchema{{Key: "reserved", Type: "number"}, {Key: "status", Type: "text"}}}
			if _, err := store.DB().Exec(`CREATE TABLE capacity (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, reserved REAL, status TEXT)`); err != nil {
				t.Fatal(err)
			}
			initial := recordmodel.Record{ID: "class-1", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"reserved": 19.0, "status": "open"}}
			if err := recordStore(store).InsertRecord(t.Context(), "workspace-primary", object, initial); err != nil {
				t.Fatal(err)
			}
			commit := transactionmodel.RecordMutationCommit{
				Operation: "update", Object: object,
				Record:     recordmodel.Record{ID: initial.ID, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"reserved": 20.0, "status": "open"}},
				Predicates: []transactionmodel.MutationPredicate{{Field: "reserved", Operator: "lt", Value: 20.0, ErrorCode: "capacity_full"}, {Field: "status", Operator: "eq", Value: "open", ErrorCode: "capacity_closed"}},
			}
			if err := recordStore(store).CommitRecordMutation(t.Context(), "workspace-primary", commit); err != nil {
				t.Fatalf("first conditional mutation: %v", err)
			}
			commit.Record.UpdatedAt = "2026-01-03T00:00:00Z"
			commit.Record.Data["reserved"] = 21.0
			err := recordStore(store).CommitRecordMutation(t.Context(), "workspace-primary", commit)
			var conflict *mutation.PolicyConflictError
			if !errors.As(err, &conflict) || conflict.Code != "capacity_full" || conflict.Field != "reserved" {
				t.Fatalf("business conflict=%#v err=%v", conflict, err)
			}
			persisted, found, err := recordStore(store).GetRecord(t.Context(), "workspace-primary", object, initial.ID)
			if err != nil || !found || fmt.Sprint(persisted.Data["reserved"]) != "20" || persisted.UpdatedAt != "2026-01-02T00:00:00Z" {
				t.Fatalf("predicate failure leaked mutation: record=%#v found=%v err=%v", persisted, found, err)
			}
		})
	}
}

func TestMutationSideFactFailureWindowsRollbackRecordAuditOutboxAndWorkflowIntent(t *testing.T) {
	for _, failure := range []struct {
		name, table string
	}{
		{name: "audit", table: "_audit_events"},
		{name: "outbox", table: "_publication_outbox"},
		{name: "workflow_intent", table: "_workflow_executions"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			store := openStoreForGeneratedListTest(t)
			defer store.Close()
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			auditmodulefixture.Bind(t, t.Context(), store)
			object := definitionmodel.ObjectSchema{Key: "failure_window_record", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
			if _, err := store.DB().Exec(`CREATE TABLE failure_window_record (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT)`); err != nil {
				t.Fatal(err)
			}
			if err := recordStore(store).InsertRecord(t.Context(), "workspace-primary", object, recordmodel.Record{ID: "record-1", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: map[string]any{"status": "pending"}}); err != nil {
				t.Fatal(err)
			}
			trigger := "fail_" + failure.name
			if _, err := store.DB().Exec("CREATE TRIGGER " + store.Identifier(trigger) + " BEFORE INSERT ON " + store.Identifier(failure.table) + " BEGIN SELECT RAISE(ABORT, 'injected side fact failure'); END"); err != nil {
				t.Fatal(err)
			}
			commit := transactionmodel.RecordMutationCommit{
				Operation: "update", Object: object, Record: recordmodel.Record{ID: "record-1", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"status": "approved"}}, ExpectedUpdatedAt: "2026-01-01T00:00:00Z",
				Audit:           dialectAudit(failure.name, "rollback"),
				Outbox:          []publicationmodel.Message{dialectOutbox(failure.name, "rollback")},
				WorkflowIntents: []workflowmodel.WorkflowExecution{dialectWorkflowIntent(failure.name, "rollback")},
			}
			if err := recordStore(store).CommitRecordMutation(t.Context(), "workspace-primary", commit); err == nil {
				t.Fatal("expected injected side fact failure")
			}
			record, found, err := recordStore(store).GetRecord(t.Context(), "workspace-primary", object, "record-1")
			if err != nil || !found || record.Data["status"] != "pending" || record.UpdatedAt != "2026-01-01T00:00:00Z" {
				t.Fatalf("record leaked through %s failure: record=%#v found=%v err=%v", failure.name, record, found, err)
			}
			assertDialectAuditCount(t, store, failure.name, 0)
			assertDialectOutboxCount(t, store, failure.name, 0)
			assertDialectWorkflowIntentCount(t, store, failure.name, 0)
		})
	}
}

func TestTemporalExclusionIsEnforcedInsideMutationTransaction(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	auditmodulefixture.Bind(t, t.Context(), store)
	if _, err := store.DB().Exec(`CREATE TABLE booking_slot (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, owner TEXT, starts_at TEXT, ends_at TEXT, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "booking_slot", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "relation"}, {Key: "starts_at", Type: "datetime"}, {Key: "ends_at", Type: "datetime"}, {Key: "status", Type: "select"}}, Validations: []definitionmodel.ValidationSchema{{
		Key: "owner_schedule", Type: "temporal_exclusion", Message: "booking.owner_busy", Config: map[string]any{"start_field": "starts_at", "end_field": "ends_at", "scope_fields": []any{"owner"}, "status_field": "status", "excluded_statuses": []any{"cancelled"}},
	}}}
	commit := func(id, owner, start, end, status string) transactionmodel.RecordMutationCommit {
		return transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: recordmodel.Record{ID: id, CreatedAt: "now", UpdatedAt: "now", Data: map[string]any{"owner": owner, "starts_at": start, "ends_at": end, "status": status}}}
	}
	if err := recordStore(store).CommitRecordMutation(t.Context(), "workspace-primary", commit("one", "coach-1", "2026-07-21T10:00:00Z", "2026-07-21T11:00:00Z", "confirmed")); err != nil {
		t.Fatal(err)
	}
	err := recordStore(store).CommitRecordMutation(t.Context(), "workspace-primary", commit("overlap", "coach-1", "2026-07-21T10:30:00Z", "2026-07-21T11:30:00Z", "confirmed"))
	var conflict *mutation.PolicyConflictError
	if !errors.As(err, &conflict) || conflict.Code != "booking.owner_busy" || conflict.Field != "owner_schedule" {
		t.Fatalf("temporal conflict=%#v err=%v", conflict, err)
	}
	for _, allowed := range []transactionmodel.RecordMutationCommit{
		commit("adjacent", "coach-1", "2026-07-21T11:00:00Z", "2026-07-21T12:00:00Z", "confirmed"),
		commit("other-scope", "coach-2", "2026-07-21T10:30:00Z", "2026-07-21T11:30:00Z", "confirmed"),
		commit("excluded", "coach-1", "2026-07-21T10:30:00Z", "2026-07-21T11:30:00Z", "cancelled"),
	} {
		if err := recordStore(store).CommitRecordMutation(t.Context(), "workspace-primary", allowed); err != nil {
			t.Fatalf("allowed temporal mutation %s: %v", allowed.Record.ID, err)
		}
	}
	batch := []transactionmodel.RecordMutationCommit{
		commit("batch-one", "coach-3", "2026-07-21T13:00:00Z", "2026-07-21T14:00:00Z", "confirmed"),
		commit("batch-overlap", "coach-3", "2026-07-21T13:30:00Z", "2026-07-21T14:30:00Z", "confirmed"),
	}
	if err := recordStore(store).CommitRecordMutationBatch(t.Context(), "workspace-primary", batch); !errors.As(err, &conflict) {
		t.Fatalf("batch temporal conflict=%#v err=%v", conflict, err)
	}
	for _, id := range []string{"overlap", "batch-one", "batch-overlap"} {
		var count int
		if err := store.DB().QueryRow(`SELECT COUNT(*) FROM booking_slot WHERE id = ?`, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("temporal conflict leaked %s count=%d err=%v", id, count, err)
		}
	}
}

func TestRelatedAggregateInvariantLocksParentAndUsesExactCandidateAggregate(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	auditmodulefixture.Bind(t, t.Context(), store)
	for _, ddl := range []string{
		`CREATE TABLE payment_limit (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, paid_amount TEXT)`,
		`CREATE TABLE refund_fact (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, payment_id TEXT, amount TEXT, status TEXT)`,
		`CREATE TABLE class_limit (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, capacity REAL)`,
		`CREATE TABLE class_booking (workspace_id TEXT NOT NULL, id TEXT PRIMARY KEY, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, class_id TEXT, status TEXT)`,
	} {
		if _, err := store.DB().Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	currency := definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 12, "scale": 2}}
	payment := definitionmodel.ObjectSchema{Key: "payment_limit", Fields: []definitionmodel.FieldSchema{{Key: "paid_amount", Type: "currency", Config: currency.Config}}}
	if err := recordStore(store).InsertRecord(t.Context(), "workspace-primary", payment, recordmodel.Record{ID: "payment-1", CreatedAt: "now", UpdatedAt: "now", Data: map[string]any{"paid_amount": "100.00"}}); err != nil {
		t.Fatal(err)
	}
	refund := definitionmodel.ObjectSchema{Key: "refund_fact", Fields: []definitionmodel.FieldSchema{
		{Key: "payment_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "payment_limit"}}, currency, {Key: "status", Type: "select"},
	}, Validations: []definitionmodel.ValidationSchema{{Key: "refund_not_above_paid", Type: "related_aggregate_invariant", Message: "refund.amount_exceeds_paid", Config: map[string]any{
		"relation_field": "payment_id", "aggregate": "sum", "value_field": "amount", "limit_field": "paid_amount", "operator": "lte", "status_field": "status", "included_statuses": []any{"pending", "approved"},
	}}}}
	refundCommit := func(id, amount, status string) transactionmodel.RecordMutationCommit {
		return transactionmodel.RecordMutationCommit{Operation: "create", Object: refund, Record: recordmodel.Record{ID: id, CreatedAt: "now", UpdatedAt: "now", Data: map[string]any{"payment_id": "payment-1", "amount": amount, "status": status}}}
	}
	for _, commit := range []transactionmodel.RecordMutationCommit{refundCommit("refund-60", "60.00", "approved"), refundCommit("refund-40", "40.00", "pending"), refundCommit("refund-cancelled", "50.00", "cancelled")} {
		if err := recordStore(store).CommitRecordMutation(t.Context(), "workspace-primary", commit); err != nil {
			t.Fatalf("allowed refund %s: %v", commit.Record.ID, err)
		}
	}
	err := recordStore(store).CommitRecordMutation(t.Context(), "workspace-primary", refundCommit("refund-over", "0.01", "approved"))
	var conflict *mutation.PolicyConflictError
	if !errors.As(err, &conflict) || conflict.Code != "refund.amount_exceeds_paid" || conflict.Field != "refund_not_above_paid" {
		t.Fatalf("refund aggregate conflict=%#v err=%v", conflict, err)
	}

	class := definitionmodel.ObjectSchema{Key: "class_limit", Fields: []definitionmodel.FieldSchema{{Key: "capacity", Type: "number"}}}
	if err := recordStore(store).InsertRecord(t.Context(), "workspace-primary", class, recordmodel.Record{ID: "class-1", CreatedAt: "now", UpdatedAt: "now", Data: map[string]any{"capacity": 2}}); err != nil {
		t.Fatal(err)
	}
	booking := definitionmodel.ObjectSchema{Key: "class_booking", Fields: []definitionmodel.FieldSchema{
		{Key: "class_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "class_limit"}}, {Key: "status", Type: "select"},
	}, Validations: []definitionmodel.ValidationSchema{{Key: "class_capacity", Type: "related_aggregate_invariant", Message: "business.capacity_full", Config: map[string]any{
		"relation_field": "class_id", "aggregate": "count", "limit_field": "capacity", "operator": "lte", "status_field": "status", "included_statuses": []any{"reserved"},
	}}}}
	bookingCommit := func(id string) transactionmodel.RecordMutationCommit {
		return transactionmodel.RecordMutationCommit{Operation: "create", Object: booking, Record: recordmodel.Record{ID: id, CreatedAt: "now", UpdatedAt: "now", Data: map[string]any{"class_id": "class-1", "status": "reserved"}}}
	}
	if err := recordStore(store).CommitRecordMutationBatch(t.Context(), "workspace-primary", []transactionmodel.RecordMutationCommit{bookingCommit("booking-1"), bookingCommit("booking-2")}); err != nil {
		t.Fatal(err)
	}
	if err := recordStore(store).CommitRecordMutation(t.Context(), "workspace-primary", bookingCommit("booking-3")); !errors.As(err, &conflict) || conflict.Code != "business.capacity_full" {
		t.Fatalf("count aggregate conflict=%#v err=%v", conflict, err)
	}
	for _, id := range []string{"refund-over", "booking-3"} {
		table := "refund_fact"
		if strings.HasPrefix(id, "booking") {
			table = "class_booking"
		}
		var count int
		if err := store.DB().QueryRow("SELECT COUNT(*) FROM "+table+" WHERE id = ?", id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("aggregate conflict leaked %s count=%d err=%v", id, count, err)
		}
	}
}
