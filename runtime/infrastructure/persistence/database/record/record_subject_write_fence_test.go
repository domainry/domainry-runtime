package record

import (
	"testing"
	"time"

	sharedsubject "github.com/domainry/domainry-foundation/subjectlifecycle"
	ormschema "github.com/domainry/domainry-orm/schema"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestErasedRecordCannotBeRecreatedByRawCommitOrLocalizedWriters(t *testing.T) {
	store := openRuntimeStore(t)
	createTestRecordTable(t, store, "member_profile", ormschema.Column("name", ormschema.Text()))
	if err := sharedsubject.EnsureSchema(t.Context(), store.SQLRenderer, store); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "member_profile", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Config: map[string]any{"localized": true}}}}
	records := NewRecordStore(store)
	for _, item := range []struct{ workspace, id string }{{"workspace-a", "alice"}, {"workspace-a", "bob"}, {"workspace-b", "alice-b"}} {
		if err := records.InsertRecord(t.Context(), item.workspace, object, recordmodel.Record{RecordID: item.id, CreatedAt: "before", UpdatedAt: "before", Data: map[string]any{"name": "cleaned"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _subject_requests(request_id,workspace_id,request_type,kind,status,subject_id,resolved_identity,updated_at,payload_json) VALUES('erase-alice','workspace-a','subject_request','erase','completed','alice','alice',?,'{}')`, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _subject_steps(workspace_id,request_id,owner,operation,payload_json,completed_at) VALUES('workspace-a','erase-alice','lifecycle','erase_fence','{}',?)`, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	planStep := `{"workspace_id":"workspace-a","request_id":"erase-alice","owner":"runtime_evidence","operation":"erase_plan","payload":{"request_id":"erase-alice","workspace_id":"workspace-a","subject_id":"alice","resources":[{"object_key":"member_profile","record_id":"alice"}],"event_ids":[],"rows":[]},"completed_at":"2026-01-01T00:00:00Z"}`
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _subject_steps(workspace_id,request_id,owner,operation,payload_json,completed_at) VALUES(?,?,?,?,?,?)`, "workspace-a", "erase-alice", "runtime_evidence", "erase_plan", planStep, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	store.BindSubjectLifecyclePersistence()
	late := recordmodel.Record{RecordID: "alice", CreatedAt: "before", UpdatedAt: "late", Data: map[string]any{"name": "alice@private.example"}}
	if err := records.UpdateRecord(t.Context(), "workspace-a", object, late); err == nil {
		t.Fatal("raw update restored erased fields")
	}
	if changed, err := records.UpdateRecordWhere(t.Context(), "workspace-a", object, late, map[string]any{"updated_at": "before"}); err != nil || changed {
		t.Fatalf("conditional update restored erased fields: %v %v", changed, err)
	}
	if err := records.DeleteRecord(t.Context(), "workspace-a", object, "alice"); err == nil {
		t.Fatal("raw delete removed retained erasure record")
	}
	tx, err := store.DB().BeginTx(t.Context(), recordMutationTxOptions())
	if err != nil {
		t.Fatal(err)
	}
	err = records.applyRecordLocalizedMutationsTx(t.Context(), tx, "workspace-a", object, "alice", "late", []recordmodel.RecordLocalizedValueMutation{{FieldKey: "name", Locale: "en-US", TextValue: "alice@private.example"}})
	_ = tx.Rollback()
	if err == nil {
		t.Fatal("localized insert restored erased fields")
	}
	if _, err := store.DB().ExecContext(t.Context(), `DELETE FROM member_profile WHERE workspace_id='workspace-a' AND member_profile_id='alice'`); err != nil {
		t.Fatal(err)
	}
	if err := records.InsertRecord(t.Context(), "workspace-a", object, late); err == nil {
		t.Fatal("raw insert recreated erased root")
	}
	if err := records.CommitRecordMutation(t.Context(), "workspace-a", transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: late}); err == nil {
		t.Fatal("commit insert recreated erased root")
	}
	late.RecordID, late.CreateBy = "new-alice-record", "alice"
	if err := records.InsertRecord(t.Context(), "workspace-a", object, late); err == nil {
		t.Fatal("raw insert accepted erased creator")
	}
	for _, item := range []struct{ workspace, id string }{{"workspace-a", "bob"}, {"workspace-b", "alice-b"}} {
		peer := late
		peer.RecordID, peer.CreateBy = item.id, ""
		if err := records.UpdateRecord(t.Context(), item.workspace, object, peer); err != nil {
			t.Fatal("peer write was denied", item, err)
		}
	}
	var leaked int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _record_localized_values WHERE workspace_id='workspace-a' AND record_id='alice'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("erased locale restored: %d %v", leaked, err)
	}
}
