package record

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/mutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestConditionalUpdateManyUsesOneLockedSelectAndOneUpdate(t *testing.T) {
	state := &recordSQLState{
		querySteps: []recordSQLQueryStep{{
			columns: []string{"id", "created_at", "updated_at", "owner_org_id", "staff_id", "status", "clock_out"},
			rows: [][]driver.Value{
				{"shift-1", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "store-a", "staff-1", "working", nil},
				{"shift-2", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "store-a", "staff-2", "working", nil},
			},
		}},
		execSteps: []recordSQLExecStep{{rows: 2}},
	}
	store := scriptedRecordStore(t, state)
	// Exercise a production locking dialect so this contract test proves the
	// set read is a physical SELECT ... FOR UPDATE, not only a logical intent.
	store.store.RuntimeEngine = testEngineProfile("mysql")
	object := definitionmodel.ObjectSchema{Key: "shift", Fields: []definitionmodel.FieldSchema{
		{Key: "staff_id", Type: "text"}, {Key: "status", Type: "text"}, {Key: "clock_out", Type: "datetime"},
	}}
	filter := &recordmodel.RecordFilterExpression{Operator: "and", Children: []recordmodel.RecordFilterExpression{
		{Field: "staff_id", Operator: "in", Values: []any{"staff-1", "staff-2"}},
		{Field: "status", Operator: "eq", Value: "working"},
		{Field: "clock_out", Operator: "is_null"},
	}}
	ctx := WithActionExecutionTransaction(t.Context(), store.database())
	page, err := store.ListRecords(ctx, "workspace-a", object, recordmodel.RecordListQuery{
		Page: 1, PageSize: 2, SkipTotal: true, FilterExpression: filter,
		Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}, LockIntent: recordmodel.RecordQueryLockForUpdate,
		AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, OwnerOrganizationScopeID: "store-a",
	})
	if err != nil || len(page.Items) != 2 || page.HasNext {
		t.Fatalf("locked page=%+v error=%v", page, err)
	}
	commit := transactionmodel.RecordMutationCommit{
		Operation: "conditional_update_many", Object: object,
		Record:       recordmodel.Record{ID: "shift-1", OwnerOrgID: "store-a", UpdatedAt: "2026-01-02T00:00:00Z", UpdateBy: "manager", Data: map[string]any{"status": "finished", "clock_out": "2026-09-06T23:00:00Z"}},
		SetRecordIDs: []string{"shift-1", "shift-2"}, SetFilterExpression: filter,
		SetExpectedAffected: 2, SetOwnerOrganizationScope: "store-a",
		SetExactCoverageField: "staff_id", SetExactCoverageValues: []any{"staff-1", "staff-2"},
	}
	if err := store.ApplyRecordMutationTx(ctx, store.database(), "workspace-a", commit); err != nil {
		t.Fatal(err)
	}
	if len(state.queryStatements) != 1 || len(state.execStatements) != 1 {
		t.Fatalf("queries=%d execs=%d\nqueries=%v\nexecs=%v", len(state.queryStatements), len(state.execStatements), state.queryStatements, state.execStatements)
	}
	if !strings.Contains(state.queryStatements[0], "staff_id") || !strings.Contains(state.queryStatements[0], "owner_org_id") || !strings.HasSuffix(state.queryStatements[0], " FOR UPDATE") ||
		!strings.HasPrefix(state.execStatements[0], `UPDATE "shift"`) || !strings.Contains(state.execStatements[0], `"id" IN`) || strings.Count(state.execStatements[0], `"staff_id" IN`) < 2 {
		t.Fatalf("locked SELECT=%s\nconditional UPDATE=%s", state.queryStatements[0], state.execStatements[0])
	}
}

func TestConditionalUpdateManyAffectedMismatchFailsClosed(t *testing.T) {
	state := &recordSQLState{execSteps: []recordSQLExecStep{{rows: 1}}}
	store := scriptedRecordStore(t, state)
	object := definitionmodel.ObjectSchema{Key: "shift", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	filter := &recordmodel.RecordFilterExpression{Field: "status", Operator: "eq", Value: "working"}
	commit := transactionmodel.RecordMutationCommit{
		Operation: "conditional_update_many", Object: object,
		Record:       recordmodel.Record{ID: "shift-1", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"status": "finished"}},
		SetRecordIDs: []string{"shift-1", "shift-2"}, SetFilterExpression: filter, SetExpectedAffected: 2,
		SetExactCoverageField: "id", SetExactCoverageValues: []any{"shift-1", "shift-2"},
	}
	err := store.ApplyRecordMutationTx(t.Context(), store.database(), "workspace-a", commit)
	var conflict *mutation.PolicyConflictError
	if !errors.As(err, &conflict) || conflict.Code != "backend.action.conditional_update_many_affected_mismatch" {
		t.Fatalf("affected mismatch error=%v", err)
	}
	if len(state.execStatements) != 1 {
		t.Fatalf("exec statements=%v", state.execStatements)
	}
}

func TestConditionalUpdateManyPersistenceRejectsNonDistinctExactCoverageWithoutWrite(t *testing.T) {
	state := &recordSQLState{}
	store := scriptedRecordStore(t, state)
	object := definitionmodel.ObjectSchema{Key: "shift", Fields: []definitionmodel.FieldSchema{{Key: "staff_id", Type: "text"}, {Key: "status", Type: "text"}}}
	filter := &recordmodel.RecordFilterExpression{Field: "status", Operator: "eq", Value: "working"}
	commit := transactionmodel.RecordMutationCommit{
		Operation: "conditional_update_many", Object: object,
		Record:       recordmodel.Record{ID: "shift-1", UpdatedAt: "2026-01-02T00:00:00Z", Data: map[string]any{"status": "finished"}},
		SetRecordIDs: []string{"shift-1", "shift-2"}, SetFilterExpression: filter, SetExpectedAffected: 2,
		SetExactCoverageField: "staff_id", SetExactCoverageValues: []any{"staff-1", "staff-1"},
	}
	if err := store.ApplyRecordMutationTx(t.Context(), store.database(), "workspace-a", commit); err == nil || !strings.Contains(err.Error(), "not distinct") {
		t.Fatalf("non-distinct coverage error=%v", err)
	}
	if len(state.execStatements) != 0 {
		t.Fatalf("invalid coverage reached write: %v", state.execStatements)
	}
}

func TestConditionalUpdateManyAffectedMismatchRollsBackTransaction(t *testing.T) {
	runtimeStore := openRuntimeStore(t)
	if err := runtimeStore.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeStore.DB().Exec(`CREATE TABLE shift (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, status TEXT, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	store := NewRecordStore(runtimeStore)
	object := definitionmodel.ObjectSchema{Key: "shift", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	if err := store.InsertRecord(t.Context(), "workspace-a", object, recordmodel.Record{ID: "shift-1", CreatedAt: "2026-09-06T22:00:00Z", UpdatedAt: "2026-09-06T22:00:00Z", Data: map[string]any{"status": "working"}}); err != nil {
		t.Fatal(err)
	}
	filter := &recordmodel.RecordFilterExpression{Field: "status", Operator: "eq", Value: "working"}
	commit := transactionmodel.RecordMutationCommit{
		Operation: "conditional_update_many", Object: object,
		Record:       recordmodel.Record{ID: "shift-1", UpdatedAt: "2026-09-06T23:00:00Z", Data: map[string]any{"status": "finished"}},
		SetRecordIDs: []string{"shift-1", "missing-shift"}, SetFilterExpression: filter, SetExpectedAffected: 2,
		SetExactCoverageField: "id", SetExactCoverageValues: []any{"shift-1", "missing-shift"},
	}
	err := store.CommitRecordMutationBatch(t.Context(), "workspace-a", []transactionmodel.RecordMutationCommit{commit})
	var conflict *mutation.PolicyConflictError
	if !errors.As(err, &conflict) || conflict.Code != "backend.action.conditional_update_many_affected_mismatch" {
		t.Fatalf("affected mismatch error=%v", err)
	}
	record, found, err := store.GetRecord(t.Context(), "workspace-a", object, "shift-1")
	if err != nil || !found || record.Data["status"] != "working" || record.UpdatedAt != "2026-09-06T22:00:00Z" {
		t.Fatalf("rolled-back record=%+v found=%v error=%v", record, found, err)
	}
}
