package record

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestSubjectRecordErasureClearsTranslationsAtomicallyAndRetainsDeclaredEvidence(t *testing.T) {
	store := openRuntimeStore(t)
	if err := store.EnsureApplicationSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE member_profile(workspace_id TEXT NOT NULL,id TEXT NOT NULL,updated_at TEXT NOT NULL,name TEXT,sku TEXT,PRIMARY KEY(workspace_id,id))`); err != nil {
		t.Fatal(err)
	}
	for _, workspace := range []string{"workspace-a", "workspace-b"} {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO member_profile VALUES(?,?,?,?,?)`, workspace, "alice", "before", "private name", "retained sku"); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"name", "sku"} {
			for _, locale := range []string{"zh-CN", "en-US"} {
				if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _record_localized_values(workspace_id,object_key,record_id,field_key,locale,text_value,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, workspace, "member_profile", "alice", field, locale, "private translated "+field, "before", "before"); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	object := definitionmodel.ObjectSchema{Key: "member_profile", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text", Config: map[string]any{"localized": true, "lifecycle_erase": "delete"}},
		{Key: "sku", Type: "text", Config: map[string]any{"localized": true, "lifecycle_erase": "retain"}},
	}}
	mutation := recordmodel.SubjectErasureMutation{ObjectKey: "member_profile", RecordID: "alice", BeforeUpdatedAt: "before", AfterUpdatedAt: "after", Values: map[string]any{"name": nil}}
	records := NewRecordStore(store)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER fail_erasure_base BEFORE UPDATE ON member_profile WHEN OLD.workspace_id='workspace-a' BEGIN SELECT RAISE(ABORT,'injected base update failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := records.ApplySubjectErasure(t.Context(), "workspace-a", []definitionmodel.ObjectSchema{object}, []recordmodel.SubjectErasureMutation{mutation}); err == nil {
		t.Fatal("base update failure was ignored")
	}
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _record_localized_values WHERE workspace_id='workspace-a' AND field_key='name'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("failed erasure partially removed translations: %d %v", count, err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `DROP TRIGGER fail_erasure_base`); err != nil {
		t.Fatal(err)
	}
	if err := records.ApplySubjectErasure(t.Context(), "workspace-a", []definitionmodel.ObjectSchema{object}, []recordmodel.SubjectErasureMutation{mutation}); err != nil {
		t.Fatal(err)
	}
	for _, expect := range []struct {
		workspace, field string
		count            int
	}{{"workspace-a", "name", 0}, {"workspace-a", "sku", 2}, {"workspace-b", "name", 2}, {"workspace-b", "sku", 2}} {
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _record_localized_values WHERE workspace_id=? AND field_key=?`, expect.workspace, expect.field).Scan(&count); err != nil || count != expect.count {
			t.Fatalf("translations %s/%s: %d want %d err=%v", expect.workspace, expect.field, count, expect.count, err)
		}
	}
	if err := records.ApplySubjectErasure(t.Context(), "workspace-a", []definitionmodel.ObjectSchema{object}, []recordmodel.SubjectErasureMutation{mutation}); err != nil {
		t.Fatal("idempotent retry failed", err)
	}
}
