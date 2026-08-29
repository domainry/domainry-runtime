package record

import (
	"path/filepath"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRecordLocalizationCreateSearchSortFallbackAndDelete(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "record-localization.db")
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureMetadataSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TABLE product (
		workspace_id TEXT NOT NULL,
		id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		sku TEXT NOT NULL,
		name TEXT NOT NULL,
		PRIMARY KEY (workspace_id, id)
	)`); err != nil {
		t.Fatal(err)
	}
	localized := map[string]any{"localized": true}
	object := definitionmodel.ObjectSchema{Key: "product", Fields: []definitionmodel.FieldSchema{
		{Key: "sku", Type: "text", Unique: true},
		{Key: "name", Type: "text", Config: localized},
	}}
	repository := NewRecordStore(store)
	create := func(workspace, id, sku, base string, translations recordmodel.RecordTranslations) {
		values, err := recordmodel.RecordNormalizeTranslations(object, translations)
		if err != nil {
			t.Fatal(err)
		}
		record := recordmodel.Record{ID: id, CreatedAt: "v1", UpdatedAt: "v1", Data: map[string]any{"sku": sku, "name": base}}
		if err := repository.CommitRecordMutation(t.Context(), workspace, transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: record, LocalizedValues: values}); err != nil {
			t.Fatal(err)
		}
	}
	create("workspace-a", "p1", "P-1", "Default phone", recordmodel.RecordTranslations{"en-US": {"name": "IPhone"}, "zh-CN": {"name": "苹果手机"}})
	create("workspace-a", "p2", "P-2", "Default tablet", recordmodel.RecordTranslations{"en-US": {"name": "Tablet"}, "zh-CN": {"name": "平板电脑"}})
	create("workspace-b", "p1", "P-1", "Private phone", recordmodel.RecordTranslations{"zh-CN": {"name": "其他租户商品"}})

	query := recordmodel.RecordListQuery{Page: 1, PageSize: 10, Search: "苹果", SearchFields: []string{"name"}, Locale: "zh-CN", FallbackLocale: "en-US"}
	page, err := repository.ListRecords(t.Context(), "workspace-a", object, query)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "p1" || page.Items[0].Data["name"] != "苹果手机" {
		t.Fatalf("localized search page=%+v err=%v", page, err)
	}
	if page.Items[0].Localization == nil || page.Items[0].Localization.FieldSources["name"] != "zh-CN" {
		t.Fatalf("localization evidence=%+v", page.Items[0].Localization)
	}

	query.Search = "iPhone"
	page, err = repository.ListRecords(t.Context(), "workspace-a", object, query)
	if err != nil || page.Total != 1 || page.Items[0].ID != "p1" {
		t.Fatalf("fallback search page=%+v err=%v", page, err)
	}

	query.Search = ""
	query.Locale = "en-US"
	query.Sort = []recordmodel.RecordSortRule{{Field: "name", Direction: "desc"}}
	page, err = repository.ListRecords(t.Context(), "workspace-a", object, query)
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != "p2" || page.Items[1].ID != "p1" {
		t.Fatalf("localized sort page=%+v err=%v", page, err)
	}
	if page.Items[0].Data["sku"] != "P-2" {
		t.Fatalf("stable field changed during localization: %+v", page.Items[0].Data)
	}

	fallback, found, err := repository.GetRecordLocalized(t.Context(), "workspace-a", object, "p1", "ja-JP", "en-US")
	if err != nil || !found || fallback.Data["name"] != "IPhone" || fallback.Localization.FieldSources["name"] != "en-US" {
		t.Fatalf("fallback record=%+v found=%v err=%v", fallback, found, err)
	}

	private, found, err := repository.GetRecordLocalized(t.Context(), "workspace-b", object, "p1", "zh-CN", "en-US")
	if err != nil || !found || private.Data["name"] != "其他租户商品" {
		t.Fatalf("workspace isolation record=%+v found=%v err=%v", private, found, err)
	}

	base, found, err := repository.GetRecord(t.Context(), "workspace-a", object, "p1")
	if err != nil || !found {
		t.Fatal(err)
	}
	base.UpdatedAt = "v2"
	updateValues, _ := recordmodel.RecordNormalizeTranslations(object, recordmodel.RecordTranslations{"zh-CN": {"name": "新苹果手机"}})
	if err := repository.CommitRecordMutation(t.Context(), "workspace-a", transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: base, ExpectedUpdatedAt: "v1", LocalizedValues: updateValues}); err != nil {
		t.Fatal(err)
	}
	updated, _, err := repository.GetRecordLocalized(t.Context(), "workspace-a", object, "p1", "zh-CN", "en-US")
	if err != nil || updated.Data["name"] != "新苹果手机" {
		t.Fatalf("localized update=%+v err=%v", updated, err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	repository = NewRecordStore(restarted)
	persisted, found, err := repository.GetRecordLocalized(t.Context(), "workspace-a", object, "p1", "zh-CN", "en-US")
	if err != nil || !found || persisted.Data["name"] != "新苹果手机" {
		t.Fatalf("localized value did not survive Runtime restart: record=%+v found=%v err=%v", persisted, found, err)
	}

	if err := repository.CommitRecordMutation(t.Context(), "workspace-a", transactionmodel.RecordMutationCommit{Operation: "delete", Object: object, Record: base, RecordID: "p1", ExpectedUpdatedAt: "v2"}); err != nil {
		t.Fatal(err)
	}
	values, err := repository.ListRecordLocalizedValues(t.Context(), "workspace-a", object, []string{"p1"}, []string{"name"}, []string{"zh-CN", "en-US"})
	if err != nil || len(values) != 0 {
		t.Fatalf("localized values survived delete: values=%+v err=%v", values, err)
	}
}

func TestRecordLocalizedQuerySQLIsDialectAwareAndNonMultiplying(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "product", Fields: []definitionmodel.FieldSchema{{Key: "sku", Type: "text"}, {Key: "name", Type: "text", Config: map[string]any{"localized": true}}}}
	query := recordmodel.RecordListQuery{Search: "phone", SearchFields: []string{"sku", "name"}, Sort: []recordmodel.RecordSortRule{{Field: "name", Direction: "asc"}}, Locale: "zh-CN", FallbackLocale: "en-US"}
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			store := openRuntimeStore(t)
			if err := store.SetEngineForTesting(driver); err != nil {
				t.Fatal(err)
			}
			where, args, err := recordLocalizedSearchWhere(store, "workspace-a", object, query)
			if err != nil {
				t.Fatal(err)
			}
			order, orderArgs := recordLocalizedOrder(store, "workspace-a", object, query, len(args))
			if !strings.Contains(where, "EXISTS (SELECT 1") || strings.Contains(where, " JOIN ") || !strings.Contains(order, "COALESCE((SELECT") {
				t.Fatalf("driver=%s where=%s order=%s", driver, where, order)
			}
			if len(args) != 9 || len(orderArgs) != 8 {
				t.Fatalf("driver=%s where args=%d order args=%d", driver, len(args), len(orderArgs))
			}
			if driver == "postgres" && (!strings.Contains(where, "$1") || !strings.Contains(order, "$10")) {
				t.Fatalf("postgres placeholders where=%s order=%s", where, order)
			}
		})
	}
}

func TestRecordLocalizationContractRejectsUnstableFields(t *testing.T) {
	for _, field := range []definitionmodel.FieldSchema{
		{Key: "price", Type: "currency", Config: map[string]any{"localized": true}},
		{Key: "sku", Type: "text", Unique: true, Config: map[string]any{"localized": true}},
	} {
		if err := recordmodel.RecordValidateLocalizedFieldContract(definitionmodel.ObjectSchema{Key: "product", Fields: []definitionmodel.FieldSchema{field}}); err == nil {
			t.Fatalf("localized contract accepted %+v", field)
		}
	}
}
