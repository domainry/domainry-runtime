package record

import (
	"reflect"
	"strings"
	"testing"

	ormschema "github.com/domainry/domainry-orm/schema"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordStoreRoundTripsCanonicalFileAndFileListValues(t *testing.T) {
	store := openRuntimeStore(t)
	object := definitionmodel.ObjectSchema{Key: "file_record", Fields: []definitionmodel.FieldSchema{
		{Key: "attachment", Type: recordmodel.RecordFileFieldType, Config: map[string]any{"scan_required": false}},
		{Key: "attachments", Type: recordmodel.RecordFileListFieldType, Config: map[string]any{"scan_required": false}},
	}}
	statement, args, err := ormschema.NewTable(store.RuntimeRenderer(), object.Key).Columns(
		ormschema.Column("workspace_id", ormschema.Text()).NotNull(),
		ormschema.Column("id", ormschema.Text()).NotNull(),
		ormschema.Column("created_at", ormschema.Text()).NotNull(),
		ormschema.Column("updated_at", ormschema.Text()).NotNull(),
		ormschema.Column("attachment", ormschema.Text()),
		ormschema.Column("attachments", ormschema.Text()),
	).PrimaryKey("workspace_id", "id").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
	reference := func(id string) map[string]any {
		return map[string]any{
			"file_id": id, "filename": id + ".pdf", "content_type": "application/pdf", "size": int64(7),
			"content_sha256": strings.Repeat("a", 64),
		}
	}
	want := map[string]any{"attachment": reference("file-1"), "attachments": []any{reference("file-2"), reference("file-3")}}
	repository := NewRecordStore(store)
	if err := repository.InsertRecord(t.Context(), "workspace-a", object, recordmodel.Record{ID: "record-1", CreatedAt: "v1", UpdatedAt: "v1", Data: want}); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.GetRecord(t.Context(), "workspace-a", object, "record-1")
	if err != nil || !found || !reflect.DeepEqual(got.Data, want) {
		t.Fatalf("file round trip found=%v data=%#v want=%#v err=%v", found, got.Data, want, err)
	}
}
