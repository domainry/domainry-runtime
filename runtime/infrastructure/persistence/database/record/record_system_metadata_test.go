package record

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordStorePersistsORMSystemMetadata(t *testing.T) {
	store := openRuntimeStore(t)
	if _, err := store.DB().Exec(`CREATE TABLE system_metadata_record (
		workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		deleted BOOLEAN NOT NULL DEFAULT FALSE, ext_info TEXT NOT NULL DEFAULT '{}',
		create_user_id TEXT, update_user_id TEXT, name TEXT, UNIQUE (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "system_metadata_record", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	want := recordmodel.Record{
		WorkspaceID: "ignored-caller-scope", ID: "record-1", CreatedAt: "v1", UpdatedAt: "v1",
		ExtInfo: map[string]any{"source": "import"}, CreateUserID: "user-1", UpdateUserID: "user-1",
		Data: map[string]any{"name": "Acme"},
	}
	if err := repository.InsertRecord(t.Context(), "workspace-a", object, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.GetRecord(t.Context(), "workspace-a", object, want.ID)
	if err != nil || !found {
		t.Fatalf("get record: found=%v err=%v", found, err)
	}
	if got.WorkspaceID != "workspace-a" || got.CreateUserID != "user-1" || got.UpdateUserID != "user-1" || got.Deleted || got.ExtInfo["source"] != "import" {
		t.Fatalf("system metadata mismatch: %#v", got)
	}
	if _, leaked := got.Data["workspace_id"]; leaked || len(got.Data) != 1 {
		t.Fatalf("system metadata leaked into business data: %#v", got.Data)
	}
}
