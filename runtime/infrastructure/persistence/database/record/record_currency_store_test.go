package record

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestSQLiteCurrencyPersistsSortsAndFiltersWithoutBinaryFloat(t *testing.T) {
	store := openRuntimeStore(t)
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().Exec(`CREATE TABLE exact_amount_record (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, amount TEXT NOT NULL, UNIQUE (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "exact_amount_record", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency", Config: map[string]any{"precision": 8, "scale": 2, "currency_code": "USD"}}}}
	repository := NewRecordStore(store)
	for _, item := range []struct{ id, amount string }{{"ten", "10.00"}, {"two", "2.00"}, {"negative", "-2.00"}} {
		if err := repository.InsertRecord(t.Context(), "workspace-primary", object, recordmodel.Record{ID: item.id, CreatedAt: "2026-07-20T00:00:00Z", UpdatedAt: "2026-07-20T00:00:00Z", Data: map[string]any{"amount": item.amount}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repository.ListRecords(t.Context(), "workspace-primary", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Sort: []recordmodel.RecordSortRule{{Field: "amount", Direction: "asc"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || page.Items[0].Data["amount"] != "-2.00" || page.Items[1].Data["amount"] != "2.00" || page.Items[2].Data["amount"] != "10.00" {
		t.Fatalf("sorted amounts=%#v", page.Items)
	}
	page, err = repository.ListRecords(t.Context(), "workspace-primary", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Filters: map[string]any{"amount__gte": "2.00"}, Sort: []recordmodel.RecordSortRule{{Field: "amount", Direction: "asc"}}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Items[0].Data["amount"] != "2.00" || page.Items[1].Data["amount"] != "10.00" {
		t.Fatalf("filtered amounts=%#v total=%d", page.Items, page.Total)
	}
}
