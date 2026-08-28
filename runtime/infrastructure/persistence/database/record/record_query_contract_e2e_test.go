package record_test

import (
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordQueryFilterProjectionAndLockOwnerContract(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	object := definitionmodel.ObjectSchema{Key: "work_item", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "select"}, {Key: "priority", Type: "integer"}, {Key: "secret", Type: "text"}}}
	if _, err := store.DB().Exec(`CREATE TABLE "work_item" ("workspace_id" TEXT NOT NULL, "id" TEXT NOT NULL, "created_at" TEXT NOT NULL, "updated_at" TEXT NOT NULL, "status" TEXT, "priority" INTEGER, "secret" TEXT, PRIMARY KEY ("workspace_id", "id"))`); err != nil {
		t.Fatal(err)
	}
	repository := recordStore(store)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for _, record := range []recordmodel.Record{
		{ID: "a", CreatedAt: now, UpdatedAt: now, Data: map[string]any{"status": "ready", "priority": 5, "secret": "hidden-a"}},
		{ID: "b", CreatedAt: now, UpdatedAt: now, Data: map[string]any{"status": "ready", "priority": 10, "secret": "hidden-b"}},
		{ID: "c", CreatedAt: now, UpdatedAt: now, Data: map[string]any{"status": "done", "priority": 20, "secret": "hidden-c"}},
	} {
		if err := repository.InsertRecord(t.Context(), "default", object, record); err != nil {
			t.Fatal(err)
		}
	}
	filter := &recordmodel.RecordFilterExpression{Operator: "and", Children: []recordmodel.RecordFilterExpression{
		{Operator: "eq", Field: "status", Value: "ready"},
		{Operator: "gte", Field: "priority", Value: "5"},
	}}
	page, err := repository.ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{Page: 1, PageSize: 1, FilterExpression: filter, Sort: []recordmodel.RecordSortRule{{Field: "priority", Direction: "desc"}}, SelectFields: []string{"status"}, LockIntent: recordmodel.RecordQueryLockNone})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != "b" || page.Items[0].Data["status"] != "ready" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if _, exists := page.Items[0].Data["secret"]; exists {
		t.Fatalf("projection leaked unselected field: %#v", page.Items[0].Data)
	}
	for _, lockIntent := range []string{recordmodel.RecordQueryLockForUpdate, recordmodel.RecordQueryLockForUpdateSkipLocked, "unknown"} {
		if _, err := repository.ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{Page: 1, PageSize: 1, LockIntent: lockIntent}); err == nil {
			t.Fatalf("ordinary list accepted transactional lock intent %q", lockIntent)
		}
	}
}
