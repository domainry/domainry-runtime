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

func TestRecordStoreDoesNotWriteSystemColumnsFromBusinessData(t *testing.T) {
	store := openRuntimeStore(t)
	if _, err := store.DB().Exec(`CREATE TABLE system_metadata_ownership (
		workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		deleted BOOLEAN NOT NULL DEFAULT FALSE, ext_info TEXT NOT NULL DEFAULT '{}',
		create_user_id TEXT, update_user_id TEXT, name TEXT, UNIQUE (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "system_metadata_ownership", Fields: []definitionmodel.FieldSchema{
		{Key: "workspace_id", Type: "text"}, {Key: "id", Type: "text"},
		{Key: "created_at", Type: "datetime"}, {Key: "updated_at", Type: "datetime"},
		{Key: "deleted", Type: "boolean"}, {Key: "ext_info", Type: "json"},
		{Key: "create_user_id", Type: "text"}, {Key: "update_user_id", Type: "text"},
		{Key: "name", Type: "text"},
	}}
	want := recordmodel.Record{
		ID: "record-1", CreatedAt: "created", UpdatedAt: "updated",
		ExtInfo: map[string]any{"owner": "record"}, CreateUserID: "creator", UpdateUserID: "updater",
		Data: map[string]any{
			"workspace_id": "attacker-workspace", "id": "attacker-id",
			"created_at": "attacker-created", "updated_at": "attacker-updated", "deleted": true,
			"ext_info": map[string]any{"owner": "data"}, "create_user_id": "attacker", "update_user_id": "attacker",
			"name": "kept",
		},
	}
	if err := repository.InsertRecord(t.Context(), "workspace-a", object, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.GetRecord(t.Context(), "workspace-a", object, want.ID)
	if err != nil || !found {
		t.Fatalf("get record: found=%v err=%v", found, err)
	}
	if got.ID != want.ID || got.WorkspaceID != "workspace-a" || got.CreatedAt != "created" || got.UpdatedAt != "updated" || got.Deleted || got.CreateUserID != "creator" || got.UpdateUserID != "updater" || got.ExtInfo["owner"] != "record" || got.Data["name"] != "kept" {
		t.Fatalf("business data overrode ORM system columns: %#v", got)
	}
}

func TestRecordStoreUpdatesORMSystemMetadata(t *testing.T) {
	store := openRuntimeStore(t)
	if _, err := store.DB().Exec(`CREATE TABLE system_metadata_update (
		workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
		deleted BOOLEAN NOT NULL DEFAULT FALSE, ext_info TEXT NOT NULL DEFAULT '{}',
		create_user_id TEXT, update_user_id TEXT, name TEXT, UNIQUE (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "system_metadata_update", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	seed := recordmodel.Record{ID: "record-1", CreatedAt: "v1", UpdatedAt: "v1", CreateUserID: "creator", UpdateUserID: "creator", Data: map[string]any{"name": "before"}}
	if err := repository.InsertRecord(t.Context(), "workspace-a", object, seed); err != nil {
		t.Fatal(err)
	}
	seed.UpdatedAt = "v2"
	seed.Deleted = true
	seed.UpdateUserID = "deleter"
	seed.ExtInfo = map[string]any{"reason": "retired"}
	seed.Data["name"] = "after"
	if err := repository.UpdateRecord(t.Context(), "workspace-a", object, seed); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.GetRecord(t.Context(), "workspace-a", object, seed.ID)
	if err != nil || !found {
		t.Fatalf("get updated record: found=%v err=%v", found, err)
	}
	if !got.Deleted || got.CreateUserID != "creator" || got.UpdateUserID != "deleter" || got.ExtInfo["reason"] != "retired" {
		t.Fatalf("updated system metadata mismatch: %#v", got)
	}
}
