package record

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestErasedRecordCannotBeRecreatedByRawCommitOrLocalizedWriters(t *testing.T) {
	store := openRuntimeStore(t)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE member_profile(workspace_id TEXT NOT NULL,id TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,name TEXT,create_by TEXT,update_by TEXT,owner_user_id TEXT,PRIMARY KEY(workspace_id,id))`); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "member_profile", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Config: map[string]any{"localized": true}}}}
	records := NewRecordStore(store)
	for _, item := range []struct{ workspace, id string }{{"workspace-a", "alice"}, {"workspace-a", "bob"}, {"workspace-b", "alice"}} {
		if err := records.InsertRecord(t.Context(), item.workspace, object, recordmodel.Record{ID: item.id, CreatedAt: "before", UpdatedAt: "before", Data: map[string]any{"name": "cleaned"}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, fence := range []struct{ kind, object, id string }{{"record", "member_profile", "alice"}, {"subject", "", "alice"}} {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _subject_evidence_erasure_fences(workspace_id,kind,object_key,record_id,request_id) VALUES(?,?,?,?,?)`, "workspace-a", fence.kind, fence.object, fence.id, "erase-alice"); err != nil {
			t.Fatal(err)
		}
	}
	late := recordmodel.Record{ID: "alice", CreatedAt: "before", UpdatedAt: "late", Data: map[string]any{"name": "alice@private.example"}}
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
	if _, err := store.DB().ExecContext(t.Context(), `DELETE FROM member_profile WHERE workspace_id='workspace-a' AND id='alice'`); err != nil {
		t.Fatal(err)
	}
	if err := records.InsertRecord(t.Context(), "workspace-a", object, late); err == nil {
		t.Fatal("raw insert recreated erased root")
	}
	if err := records.CommitRecordMutation(t.Context(), "workspace-a", transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: late}); err == nil {
		t.Fatal("commit insert recreated erased root")
	}
	late.ID, late.CreateBy = "new-alice-record", "alice"
	if err := records.InsertRecord(t.Context(), "workspace-a", object, late); err == nil {
		t.Fatal("raw insert accepted erased creator")
	}
	for _, item := range []struct{ workspace, id string }{{"workspace-a", "bob"}, {"workspace-b", "alice"}} {
		peer := late
		peer.ID, peer.CreateBy = item.id, ""
		if err := records.UpdateRecord(t.Context(), item.workspace, object, peer); err != nil {
			t.Fatal("peer write was denied", item, err)
		}
	}
	var leaked int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _record_localized_values WHERE workspace_id='workspace-a' AND record_id='alice'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("erased locale restored: %d %v", leaked, err)
	}
}
