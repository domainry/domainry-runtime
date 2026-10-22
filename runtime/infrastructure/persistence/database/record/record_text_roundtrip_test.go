package record

import (
	"reflect"
	"testing"

	ormschema "github.com/domainry/domainry-orm/schema"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordStorePreservesEmptyTextOnRead(t *testing.T) {
	store := openRuntimeStore(t)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	object := definitionmodel.ObjectSchema{Key: "text_receipt", Fields: []definitionmodel.FieldSchema{
		{Key: "bank_name", Type: "text"}, {Key: "account_number", Type: "text"}, {Key: "code", Type: "text"}, {Key: "note", Type: "long_text"},
	}}
	columns := []ormschema.ColumnDefinition{}
	for _, field := range []string{"bank_name", "account_number", "code", "note"} {
		columns = append(columns, ormschema.Column(field, ormschema.Text()))
	}
	createTestRecordTable(t, store, object.Key, columns...)
	repository := NewRecordStore(store)
	want := map[string]any{"bank_name": "", "account_number": "", "code": "  vendor code  ", "note": " \n "}
	row := recordmodel.Record{RecordID: "row-1", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z", Data: want}
	if err := repository.InsertRecord(t.Context(), "workspace", object, row); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.GetRecord(t.Context(), "workspace", object, row.RecordID)
	if err != nil || !found {
		t.Fatalf("get found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(got.Data, want) {
		t.Errorf("GET lost stored text: got %#v want %#v", got.Data, want)
	}
	page, err := repository.ListRecords(t.Context(), "workspace", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list=%#v err=%v", page, err)
	}
	if !reflect.DeepEqual(page.Items[0].Data, want) {
		t.Errorf("LIST lost stored text: got %#v want %#v", page.Items[0].Data, want)
	}
}
