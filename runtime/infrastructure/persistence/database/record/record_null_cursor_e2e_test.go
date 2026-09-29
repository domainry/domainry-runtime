package record_test

import (
	"reflect"
	"testing"
	"time"

	ormschema "github.com/domainry/domainry-orm/schema"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordCursorPreservesNullDatetimeBoundaryAcrossPages(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	object := definitionmodel.ObjectSchema{Key: "task", Fields: []definitionmodel.FieldSchema{
		{Key: "due_at", Type: "datetime"}, {Key: "status", Type: "select"},
	}}
	columns := []ormschema.ColumnDefinition{
		ormschema.Column("workspace_id", ormschema.Text()), ormschema.Column("id", ormschema.Text()),
		ormschema.Column("created_at", ormschema.BigInt()), ormschema.Column("updated_at", ormschema.BigInt()),
		ormschema.Column("due_at", ormschema.BigInt()), ormschema.Column("status", ormschema.Text()),
	}
	statement, args, err := ormschema.NewTable(store.RuntimeRenderer(), object.Key).Columns(columns...).PrimaryKey("workspace_id", "id").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
	repository := recordStore(store)
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for _, id := range []string{"task-1", "task-2", "task-3", "task-4", "task-5"} {
		if err := repository.InsertRecord(t.Context(), "workspace-a", object, recordmodel.Record{
			ID: id, CreatedAt: now, UpdatedAt: now, Data: map[string]any{"status": "open"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	query := recordmodel.RecordListQuery{
		Page: 1, PageSize: 2, SkipTotal: true, StableNullsLast: true,
		AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted,
		Filters:           map[string]any{"status": "open"},
		Sort:              []recordmodel.RecordSortRule{{Field: "due_at", Direction: "asc"}, {Field: "id", Direction: "asc"}},
	}
	first, err := repository.ListRecords(t.Context(), "workspace-a", object, query)
	if err != nil {
		t.Fatal(err)
	}
	if got := recordIDs(first.Items); !reflect.DeepEqual(got, []string{"task-1", "task-2"}) || !first.HasNext {
		t.Fatalf("first page ids=%v has_next=%v", got, first.HasNext)
	}
	last := first.Items[len(first.Items)-1]
	if value, present := last.QuerySortValues["due_at"]; !present || value != nil {
		t.Fatalf("NULL due_at cursor value=%#v present=%v", value, present)
	}
	query.FilterExpression = &recordmodel.RecordFilterExpression{Operator: "and", Children: []recordmodel.RecordFilterExpression{
		{Operator: "is_null", Field: "due_at"},
		{Operator: "gt", Field: "id", Value: last.ID},
	}}
	second, err := repository.ListRecords(t.Context(), "workspace-a", object, query)
	if err != nil {
		t.Fatal(err)
	}
	if got := recordIDs(second.Items); !reflect.DeepEqual(got, []string{"task-3", "task-4"}) || !second.HasNext {
		t.Fatalf("second page ids=%v has_next=%v", got, second.HasNext)
	}
}

func recordIDs(records []recordmodel.Record) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	return ids
}
