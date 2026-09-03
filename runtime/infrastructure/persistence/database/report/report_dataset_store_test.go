package report

import (
	"path/filepath"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestReportDatasetStorePushesRLSJoinFilterAndExactDecimalProjectionToSQLite(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "report.db"), IntegrationSecretKey: "report-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, statement := range []string{
		`CREATE TABLE account (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, segment TEXT, version_counter INTEGER, UNIQUE (workspace_id, id))`,
		`CREATE TABLE transaction_fact (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, account_id TEXT, account_version INTEGER, owner_id TEXT, status TEXT, amount TEXT, UNIQUE (workspace_id, id))`,
		`CREATE INDEX idx_transaction_fact_account ON transaction_fact(workspace_id, account_id, account_version)`,
		`CREATE INDEX idx_transaction_fact_owner ON transaction_fact(workspace_id, owner_id)`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	objects := map[string]definitionmodel.ObjectSchema{
		"accounts": {Key: "account", Fields: []definitionmodel.FieldSchema{{Key: "segment", Type: "status"}, {Key: "version_counter", Type: "integer"}}},
		"transactions": {Key: "transaction_fact", Fields: []definitionmodel.FieldSchema{
			{Key: "account_id", Type: "relation"}, {Key: "account_version", Type: "integer"}, {Key: "owner_id", Type: "relation"}, {Key: "status", Type: "status"},
			{Key: "amount", Type: "currency", Config: map[string]any{"precision": 8, "scale": 2, "currency_code": "USD"}},
		}},
	}
	records := recordpersistence.NewRecordStore(store)
	insert := func(object definitionmodel.ObjectSchema, id string, data map[string]any) {
		t.Helper()
		if err := records.InsertRecord(t.Context(), "workspace-a", object, recordmodel.Record{ID: id, CreatedAt: "2026-07-21T00:00:00Z", UpdatedAt: "2026-07-21T00:00:00Z", Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	insert(objects["accounts"], "account-a", map[string]any{"segment": "A", "version_counter": 2})
	insert(objects["accounts"], "account-b", map[string]any{"segment": "B", "version_counter": 1})
	insert(objects["transactions"], "tx-1", map[string]any{"account_id": "account-a", "account_version": 2, "owner_id": "user-1", "status": "settled", "amount": "10.00"})
	insert(objects["transactions"], "tx-2", map[string]any{"account_id": "account-a", "account_version": 1, "owner_id": "user-2", "status": "settled", "amount": "20.00"})
	insert(objects["transactions"], "tx-3", map[string]any{"account_id": "account-b", "account_version": 1, "owner_id": "user-1", "status": "settled", "amount": "5.00"})

	dataset := reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{ObjectKey: "account", Alias: "accounts"},
		Joins: []reportmodel.ReportDatasetJoin{{ObjectKey: "transaction_fact", Alias: "transactions", Type: "inner", LeftAlias: "accounts", FieldEqualities: []reportmodel.ReportDatasetJoinFieldEquality{
			{LeftField: "id", RightField: "account_id"}, {LeftField: "version_counter", RightField: "account_version"},
		}, Cardinality: "one_to_many"}},
		Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "transactions", FieldKey: "amount"}, Operator: "between", Values: []any{"6.00", "15.00"}}},
	}
	request := reportcontract.ReportDatasetRowReadRequest{
		WorkspaceID: "workspace-a",
		Plan:        reportmodel.ReportDatasetPlan{ReportKey: "revenue", Dataset: dataset, AliasObjects: map[string]string{"accounts": "account", "transactions": "transaction_fact"}},
		Objects:     objects,
		Queries: map[string]recordmodel.RecordListQuery{
			"accounts":     {AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, SelectFields: []string{"segment", "version_counter"}},
			"transactions": {AuthorizationMode: recordmodel.RecordQueryAuthorizationPredicate, RootObjectKey: "transaction_fact", ScopeExpression: &recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: "owner_id", Values: []string{"user-1"}}, SelectFields: []string{"account_id", "account_version", "owner_id", "status", "amount"}},
		},
	}
	rows, err := NewReportDatasetStore(store).ReadReportDatasetRows(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Records["accounts"].ID != "account-a" || rows[0].Records["transactions"].ID != "tx-1" || rows[0].Records["transactions"].Data["amount"] != "10.00" {
		t.Fatalf("rows=%#v", rows)
	}
	version, err := NewReportDatasetStore(store).ReadReportSnapshotSourceVersion(t.Context(), reportcontract.ReportSnapshotSourceVersionRequest{WorkspaceID: request.WorkspaceID, Objects: request.Objects, Queries: request.Queries})
	if err != nil || version.Watermark != "2026-07-21T00:00:00Z" || version.SourceVersions["accounts"] == "" || version.SourceVersions["transactions"] == "" || version.SourceVersions["transactions"][:2] != "2:" {
		t.Fatalf("version=%#v err=%v", version, err)
	}
}
