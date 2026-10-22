package record

import (
	"testing"

	ormschema "github.com/domainry/domainry-orm/schema"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordStorePersistsORMSystemMetadata(t *testing.T) {
	store := openRuntimeStore(t)
	createTestRecordTable(t, store, "system_metadata_record", ormschema.Column("name", ormschema.Text()))
	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "system_metadata_record", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	want := recordmodel.Record{
		WorkspaceID: "ignored-caller-scope", RecordID: "record-1", CreatedAt: "2026-09-23T00:00:00Z", UpdatedAt: "2026-09-23T00:00:00Z",
		ExtInfo: map[string]any{"source": "import"}, CreateBy: "user-1", UpdateBy: "user-1",
		Data: map[string]any{"name": "Acme"},
	}
	if err := repository.InsertRecord(t.Context(), "workspace-a", object, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.GetRecord(t.Context(), "workspace-a", object, want.RecordID)
	if err != nil || !found {
		t.Fatalf("get record: found=%v err=%v", found, err)
	}
	if got.WorkspaceID != "workspace-a" || got.CreateBy != "user-1" || got.UpdateBy != "user-1" || got.Deleted || got.ExtInfo["source"] != "import" {
		t.Fatalf("system metadata mismatch: %#v", got)
	}
	if _, leaked := got.Data["workspace_id"]; leaked || len(got.Data) != 1 {
		t.Fatalf("system metadata leaked into business data: %#v", got.Data)
	}
}

func TestRecordStoreDoesNotWriteSystemColumnsFromBusinessData(t *testing.T) {
	store := openRuntimeStore(t)
	createTestRecordTable(t, store, "system_metadata_ownership", ormschema.Column("name", ormschema.Text()))
	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "system_metadata_ownership", Fields: []definitionmodel.FieldSchema{
		{Key: "workspace_id", Type: "text"}, {Key: "id", Type: "text"},
		{Key: "created_at", Type: "datetime"}, {Key: "updated_at", Type: "datetime"},
		{Key: "deleted", Type: "boolean"}, {Key: "ext_info", Type: "json"},
		{Key: "create_by", Type: "text"}, {Key: "update_by", Type: "text"},
		{Key: "name", Type: "text"},
	}}
	want := recordmodel.Record{
		RecordID: "record-1", CreatedAt: "2026-09-23T00:00:00Z", UpdatedAt: "2026-09-23T01:00:00Z",
		ExtInfo: map[string]any{"owner": "record"}, CreateBy: "creator", UpdateBy: "updater",
		Data: map[string]any{
			"workspace_id": "attacker-workspace", "id": "attacker-id",
			"created_at": "attacker-created", "updated_at": "attacker-updated", "deleted": true,
			"ext_info": map[string]any{"owner": "data"}, "create_by": "attacker", "update_by": "attacker",
			"name": "kept",
		},
	}
	if err := repository.InsertRecord(t.Context(), "workspace-a", object, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.GetRecord(t.Context(), "workspace-a", object, want.RecordID)
	if err != nil || !found {
		t.Fatalf("get record: found=%v err=%v", found, err)
	}
	if got.RecordID != want.RecordID || got.WorkspaceID != "workspace-a" || got.CreatedAt != want.CreatedAt || got.UpdatedAt != want.UpdatedAt || got.Deleted || got.CreateBy != "creator" || got.UpdateBy != "updater" || got.ExtInfo["owner"] != "record" || got.Data["name"] != "kept" {
		t.Fatalf("business data overrode ORM system columns: %#v", got)
	}
}

func TestRecordStoreUpdatesORMSystemMetadata(t *testing.T) {
	store := openRuntimeStore(t)
	createTestRecordTable(t, store, "system_metadata_update", ormschema.Column("name", ormschema.Text()))
	repository := NewRecordStore(store)
	object := definitionmodel.ObjectSchema{Key: "system_metadata_update", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	seed := recordmodel.Record{RecordID: "record-1", CreatedAt: "2026-09-23T00:00:00Z", UpdatedAt: "2026-09-23T00:00:00Z", CreateBy: "creator", UpdateBy: "creator", Data: map[string]any{"name": "before"}}
	if err := repository.InsertRecord(t.Context(), "workspace-a", object, seed); err != nil {
		t.Fatal(err)
	}
	seed.UpdatedAt = "2026-09-23T01:00:00Z"
	seed.Deleted = true
	seed.UpdateBy = "deleter"
	seed.ExtInfo = map[string]any{"reason": "retired"}
	seed.Data["name"] = "after"
	if err := repository.UpdateRecord(t.Context(), "workspace-a", object, seed); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.GetRecord(t.Context(), "workspace-a", object, seed.RecordID)
	if err != nil || !found {
		t.Fatalf("get updated record: found=%v err=%v", found, err)
	}
	if !got.Deleted || got.CreateBy != "creator" || got.UpdateBy != "deleter" || got.ExtInfo["reason"] != "retired" {
		t.Fatalf("updated system metadata mismatch: %#v", got)
	}
}
