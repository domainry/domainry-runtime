package report

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	ormschema "github.com/domainry/domainry-orm/schema"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportquery "github.com/domainry/domainry-report-sdk/query"
	reportobjectsql "github.com/domainry/domainry-report-sdk/query/objectsql"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestReportContainsAggregatesCompleteMatchingScopeWithLiteralSearch(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "contains.db"), IntegrationSecretKey: "report-contains-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	columns := []ormschema.ColumnDefinition{}
	for _, field := range []string{"workspace_id", "id", "created_at", "updated_at", "owner_org_id", "customer", "cast_names"} {
		columns = append(columns, ormschema.Column(field, ormschema.Text()))
	}
	columns = append(columns, ormschema.Column("amount", ormschema.Integer()))
	statement, args, err := ormschema.NewTable(store.RuntimeRenderer(), "voucher").Columns(columns...).PrimaryKey("workspace_id", "id").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "voucher", Fields: []definitionmodel.FieldSchema{{Key: "customer", Type: "text"}, {Key: "cast_names", Type: "long_text"}, {Key: "amount", Type: "integer"}}}
	objects := map[string]reportquery.Object{"voucher": {Key: "voucher", Fields: []reportquery.Field{{Key: "customer", Type: "text"}, {Key: "cast_names", Type: "long_text"}, {Key: "amount", Type: "integer"}}}}
	records := recordpersistence.NewRecordStore(store)
	insert := func(workspace, owner, id string, customer any, cast string, amount int64) {
		t.Helper()
		if err := records.InsertRecord(t.Context(), workspace, object, recordmodel.Record{ID: id, OwnerOrgID: owner, CreatedAt: "2026-09-08T00:00:00Z", UpdatedAt: "2026-09-08T00:00:00Z",
			Data: map[string]any{"customer": customer, "cast_names": cast, "amount": amount}}); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 160 {
		insert("workspace-a", "store-a", fmt.Sprintf("noise-%03d", index), "guest 50XXoff~猫 end", "東京 花子", 99999)
	}
	for index := range 105 {
		insert("workspace-a", "store-a", fmt.Sprintf("match-%03d", index), "guest 50%_off~猫 end", "東京 花子", int64(index+1))
	}
	insert("workspace-b", "store-a", "foreign-workspace", "guest 50%_off~猫 end", "東京 花子", 99999)
	insert("workspace-a", "store-b", "foreign-store", "guest 50%_off~猫 end", "東京 花子", 99999)
	insert("workspace-a", "store-a", "wrong-cast", "guest 50%_off~猫 end", "大阪", 99999)
	insert("workspace-a", "store-a", "null-customer", nil, "東京 花子", 99999)
	insert("workspace-a", "store-a", "quoted-literal", "' OR 1=1 --", "東京 花子", 42)
	executor := NewReportSQLStore(store)
	execute := func(predicate string, parameters map[string]any, mode recordmodel.RecordQueryAuthorizationMode) map[string]string {
		t.Helper()
		schema := reportmodel.ReportObjectSQLSchema{
			SQL:        "SELECT COUNT(v.id) AS record_count, COALESCE(SUM(v.amount), 0) AS amount FROM voucher v WHERE " + predicate + " LIMIT 1",
			Parameters: []reportmodel.ReportObjectSQLParameter{{Key: "keyword", Type: "text"}, {Key: "cast_keyword", Type: "text"}},
		}
		canonical, plan, err := reportobjectsql.CanonicalReportObjectSQL(schema, objects)
		if err != nil {
			t.Fatal(err)
		}
		normalized, err := reportobjectsql.NormalizeParameters(canonical.Parameters, parameters)
		if err != nil {
			t.Fatal(err)
		}
		result, err := executor.ExecuteReportObjectSQL(t.Context(), reportcontract.ReportObjectSQLExecutionRequest{
			WorkspaceID: "workspace-a", Plan: plan, Objects: map[string]definitionmodel.ObjectSchema{"v": object}, Parameters: normalized, Timeout: 2 * time.Second,
			Queries: map[string]recordmodel.RecordListQuery{"v": {AuthorizationMode: mode, OwnerOrganizationScopeID: "store-a", SelectFields: plan.Sources[0].Fields}},
		})
		if err != nil || len(result.Rows) != 1 {
			t.Fatalf("result=%+v error=%v", result, err)
		}
		return result.Rows[0]
	}
	predicate := "CONTAINS(v.customer, :keyword) AND CONTAINS(v.cast_names, :cast_keyword)"
	for _, keyword := range []string{" 50%_off~猫 ", "%_"} {
		row := execute(predicate, map[string]any{"keyword": keyword, "cast_keyword": "花子"}, recordmodel.RecordQueryAuthorizationUnrestricted)
		if row["record_count"] != "105" || row["amount"] != "5565" {
			t.Fatalf("literal=%q row=%+v", keyword, row)
		}
	}
	row := execute(predicate, map[string]any{"keyword": "' OR 1=1 --", "cast_keyword": "花子"}, recordmodel.RecordQueryAuthorizationUnrestricted)
	if row["record_count"] != "1" || row["amount"] != "42" {
		t.Fatalf("quoted literal row=%+v", row)
	}
	row = execute("NOT CONTAINS(v.customer, :keyword)", nil, recordmodel.RecordQueryAuthorizationUnrestricted)
	if row["record_count"] != "0" || row["amount"] != "0" {
		t.Fatalf("NOT NULL semantics=%+v", row)
	}
	row = execute(predicate, map[string]any{"keyword": "", "cast_keyword": "花子"}, recordmodel.RecordQueryAuthorizationUnrestricted)
	if row["record_count"] != "266" {
		t.Fatalf("empty literal should match non-null fields only: %+v", row)
	}
	row = execute(predicate, map[string]any{"keyword": "", "cast_keyword": "花子"}, recordmodel.RecordQueryAuthorizationDeny)
	if row["record_count"] != "0" || row["amount"] != "0" {
		t.Fatalf("denied aggregate=%+v", row)
	}
}
