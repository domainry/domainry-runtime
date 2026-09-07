package record_test

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	ormschema "github.com/domainry/domainry-orm/schema"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordContainsFiltersBeforeLimitAndPagesTiedTimesWithinStore(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	object := definitionmodel.ObjectSchema{Key: "voucher", Fields: []definitionmodel.FieldSchema{
		{Key: "customer", Type: "text"}, {Key: "cast_name", Type: "long_text"}, {Key: "occurred_at", Type: "datetime"},
	}}
	columns := []ormschema.ColumnDefinition{}
	for _, field := range []string{"workspace_id", "id", "created_at", "updated_at", "owner_org_id", "customer", "cast_name", "occurred_at"} {
		columns = append(columns, ormschema.Column(field, ormschema.Text()))
	}
	statement, args, err := ormschema.NewTable(store.RuntimeRenderer(), object.Key).Columns(columns...).PrimaryKey("workspace_id", "id").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
	repository := recordStore(store)
	base := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	expected := []recordmodel.Record{}
	insert := func(workspace, owner, id string, customer any, cast string, occurred time.Time) {
		t.Helper()
		record := recordmodel.Record{ID: id, OwnerOrgID: owner, CreatedAt: base.Format(time.RFC3339Nano), UpdatedAt: base.Format(time.RFC3339Nano),
			Data: map[string]any{"customer": customer, "cast_name": cast, "occurred_at": occurred.Format(time.RFC3339Nano)}}
		if err := repository.InsertRecord(t.Context(), workspace, object, record); err != nil {
			t.Fatal(err)
		}
		if workspace == "workspace-a" && owner == "store-a" && customer == "guest 50%_off~猫 end" && cast == "東京 花子" {
			expected = append(expected, record)
		}
	}
	// More than a page of newer nonmatches must not hide the matching interval.
	for index := range 160 {
		insert("workspace-a", "store-a", fmt.Sprintf("noise-%03d", index), "guest 50XXoff~猫 end", "東京 花子", base.Add(100*time.Hour))
	}
	for index := range 105 {
		insert("workspace-a", "store-a", fmt.Sprintf("match-%03d", index), "guest 50%_off~猫 end", "東京 花子", base.Add(time.Duration(index/3)*time.Hour))
	}
	insert("workspace-b", "store-a", "foreign-workspace", "guest 50%_off~猫 end", "東京 花子", base)
	insert("workspace-a", "store-b", "foreign-store", "guest 50%_off~猫 end", "東京 花子", base)
	insert("workspace-a", "store-a", "wrong-cast", "guest 50%_off~猫 end", "大阪", base)
	insert("workspace-a", "store-a", "null-customer", nil, "東京 花子", base)
	insert("workspace-a", "store-a", "injection-literal", "' OR 1=1 --", "東京 花子", base)
	insert("workspace-a", "store-a", "bracket-literal", "[a-z]", "東京 花子", base)
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].Data["occurred_at"] == expected[j].Data["occurred_at"] {
			return expected[i].ID < expected[j].ID
		}
		return expected[i].Data["occurred_at"].(string) > expected[j].Data["occurred_at"].(string)
	})
	baseFilters := []recordmodel.RecordFilterExpression{
		{Operator: "or", Children: []recordmodel.RecordFilterExpression{{Field: "customer", Operator: "contains", Value: " 50%_off~猫 "}, {Field: "id", Operator: "contains", Value: "never-match"}}},
		{Field: "cast_name", Operator: "contains", Value: "花子"},
	}
	query := recordmodel.RecordListQuery{Page: 1, PageSize: 40, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, OwnerOrganizationScopeID: "store-a",
		Sort: []recordmodel.RecordSortRule{{Field: "occurred_at", Direction: "desc"}, {Field: "id", Direction: "asc"}}}
	gotIDs, wantIDs := []string{}, []string{}
	for _, record := range expected {
		wantIDs = append(wantIDs, record.ID)
	}
	var cursor *recordmodel.Record
	for pageNumber := range 3 {
		filters := append([]recordmodel.RecordFilterExpression(nil), baseFilters...)
		if cursor != nil {
			filters = append(filters, recordmodel.RecordFilterExpression{Operator: "or", Children: []recordmodel.RecordFilterExpression{
				{Field: "occurred_at", Operator: "lt", Value: cursor.Data["occurred_at"]},
				{Operator: "and", Children: []recordmodel.RecordFilterExpression{{Field: "occurred_at", Operator: "eq", Value: cursor.Data["occurred_at"]}, {Field: "id", Operator: "gt", Value: cursor.ID}}},
			}})
		}
		query.FilterExpression = &recordmodel.RecordFilterExpression{Operator: "and", Children: filters}
		page, err := repository.ListRecords(t.Context(), "workspace-a", object, query)
		if err != nil || len(page.Items) == 0 || page.Total != 105-pageNumber*40 {
			t.Fatalf("page=%+v error=%v", page, err)
		}
		for _, record := range page.Items {
			gotIDs = append(gotIDs, record.ID)
		}
		last := page.Items[len(page.Items)-1]
		cursor = &last
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("paged identities=%v want=%v", gotIDs, wantIDs)
	}
	for _, sample := range []struct{ literal, id string }{{"' OR 1=1 --", "injection-literal"}, {"[a-z]", "bracket-literal"}} {
		query.FilterExpression = &recordmodel.RecordFilterExpression{Field: "customer", Operator: "contains", Value: sample.literal}
		page, err := repository.ListRecords(t.Context(), "workspace-a", object, query)
		if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != sample.id {
			t.Fatalf("literal=%q page=%+v error=%v", sample.literal, page, err)
		}
	}
	query.AuthorizationMode = recordmodel.RecordQueryAuthorizationDeny
	page, err := repository.ListRecords(t.Context(), "workspace-a", object, query)
	if err != nil || len(page.Items) != 0 || page.Total != 0 {
		t.Fatalf("denied page=%+v error=%v", page, err)
	}
}
