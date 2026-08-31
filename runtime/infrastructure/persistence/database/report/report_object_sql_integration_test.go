package report

import (
	"path/filepath"
	"testing"
	"time"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestReportObjectSQLExecutesPOSFixtureWithIsolationRLSAndExactMoney(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "object-sql.db"), IntegrationSecretKey: "object-sql-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, statement := range []string{
		`CREATE TABLE sale (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, employee_id TEXT, team_id TEXT, department_path TEXT, status TEXT, net_total TEXT, discount_total TEXT, refund_total TEXT, units INTEGER, UNIQUE (workspace_id, id))`,
		`CREATE TABLE payment (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, sale_id TEXT, owner_id TEXT, kind TEXT, amount TEXT, UNIQUE (workspace_id, id))`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	currency := map[string]any{"precision": 19, "scale": 2, "currency_code": "CNY"}
	objects := map[string]definitionmodel.ObjectSchema{
		"sale": {Key: "sale", Fields: []definitionmodel.FieldSchema{
			{Key: "employee_id", Type: "user"}, {Key: "team_id", Type: "relation"}, {Key: "department_path", Type: "text"}, {Key: "status", Type: "text"},
			{Key: "net_total", Type: "currency", Config: currency}, {Key: "discount_total", Type: "currency", Config: currency}, {Key: "refund_total", Type: "currency", Config: currency}, {Key: "units", Type: "integer"},
		}},
		"payment": {Key: "payment", Fields: []definitionmodel.FieldSchema{{Key: "sale_id", Type: "relation"}, {Key: "owner_id", Type: "user"}, {Key: "kind", Type: "text"}, {Key: "amount", Type: "currency", Config: currency}}},
	}
	records := recordpersistence.NewRecordStore(store)
	insert := func(workspace string, object definitionmodel.ObjectSchema, id string, data map[string]any) {
		t.Helper()
		if err := records.InsertRecord(t.Context(), workspace, object, recordmodel.Record{ID: id, CreatedAt: "2026-08-01T10:00:00Z", UpdatedAt: "2026-08-01T10:00:00Z", Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	insert("workspace-a", objects["sale"], "s1", map[string]any{"employee_id": "e1", "team_id": "t1", "department_path": "sales/east", "status": "paid", "net_total": "10.10", "discount_total": "0.30", "refund_total": "0.00", "units": 2})
	insert("workspace-a", objects["sale"], "s2", map[string]any{"employee_id": "e1", "team_id": "t2", "department_path": "sales/west", "status": "refunded", "net_total": "20.20", "discount_total": "1.20", "refund_total": "5.05", "units": 0})
	insert("workspace-a", objects["sale"], "s3", map[string]any{"employee_id": "e2", "team_id": "t1", "department_path": "sales/east", "status": "paid", "net_total": "0.10", "discount_total": "0.00", "refund_total": "0.00", "units": 1})
	insert("workspace-b", objects["sale"], "other", map[string]any{"employee_id": "e1", "team_id": "t1", "department_path": "sales/east", "status": "paid", "net_total": "999.99", "discount_total": "0.00", "refund_total": "0.00", "units": 1})
	insert("workspace-a", objects["payment"], "p1", map[string]any{"sale_id": "s1", "owner_id": "user-1", "kind": "cash", "amount": "10.10"})
	insert("workspace-a", objects["payment"], "p2", map[string]any{"sale_id": "s2", "owner_id": "user-2", "kind": "card", "amount": "15.15"})
	insert("workspace-a", objects["payment"], "p3", map[string]any{"sale_id": "s2", "owner_id": "user-1", "kind": "cash", "amount": "5.05"})

	executor := NewReportDatasetStore(store)
	executeWithParameters := func(schema reportmodel.ReportObjectSQLSchema, queries map[string]recordmodel.RecordListQuery, parameters map[string]any) []map[string]string {
		t.Helper()
		plan, err := reportcontract.CompileReportObjectSQL(schema, objects)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range plan.Sources {
			query := queries[source.Alias]
			query.SelectFields = append([]string(nil), source.Fields...)
			queries[source.Alias] = query
		}
		result, err := executor.ExecuteReportObjectSQL(t.Context(), reportcontract.ReportObjectSQLExecutionRequest{WorkspaceID: "workspace-a", Plan: plan, Objects: aliasObjects(plan, objects), Queries: queries, Parameters: parameters, Timeout: 2 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		return result.Rows
	}
	execute := func(schema reportmodel.ReportObjectSQLSchema, queries map[string]recordmodel.RecordListQuery) []map[string]string {
		return executeWithParameters(schema, queries, map[string]any{})
	}

	sales := executeWithParameters(reportmodel.ReportObjectSQLSchema{
		SQL:           `SELECT s.employee_id AS employee_id, ROUND(SUM(s.net_total), 2) AS revenue, AVG(s.net_total) AS average_sale, SUM(s.discount_total) AS discounts, SUM(CASE WHEN s.status = :refunded_status THEN s.refund_total ELSE 0 END) AS refunds, FLOOR(COALESCE(SUM(s.units), 0) / NULLIF(COUNT(*), 0)) AS units_per_sale FROM sale s GROUP BY s.employee_id ORDER BY employee_id LIMIT 10`,
		SourceObjects: []string{"sale"},
		Parameters:    []reportmodel.ReportObjectSQLParameter{{Key: "refunded_status", Type: "text", Required: true}},
		ResultSchema:  []reportmodel.ReportResultColumnSchema{{Key: "employee_id", Type: "text", Kind: "dimension"}, {Key: "revenue", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}, {Key: "average_sale", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}, {Key: "discounts", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}, {Key: "refunds", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}, {Key: "units_per_sale", Type: "decimal", Kind: "measure"}},
	}, map[string]recordmodel.RecordListQuery{"s": {Scope: "all_records"}}, map[string]any{"refunded_status": "refunded"})
	if len(sales) != 2 || sales[0]["employee_id"] != "e1" || sales[0]["revenue"] != "30.30" || sales[0]["average_sale"] != "15.15" || sales[0]["discounts"] != "1.50" || sales[0]["refunds"] != "5.05" || sales[0]["units_per_sale"] != "1" || sales[1]["revenue"] != "0.10" {
		t.Fatalf("sales=%#v", sales)
	}
	currencyDivision := executeWithParameters(reportmodel.ReportObjectSQLSchema{
		SQL: `SELECT SUM(s.net_total) / NULLIF(COUNT(*), 0) AS average_ticket, SUM(s.net_total) / NULLIF(:unit_count, 0) AS unit_economics FROM sale s LIMIT 1`, SourceObjects: []string{"sale"},
		Parameters: []reportmodel.ReportObjectSQLParameter{{Key: "unit_count", Type: "decimal", Required: true}},
		ResultSchema: []reportmodel.ReportResultColumnSchema{
			{Key: "average_ticket", Type: "currency", Kind: "measure", Precision: 19, Scale: 2},
			{Key: "unit_economics", Type: "currency", Kind: "measure", Precision: 19, Scale: 2},
		},
	}, map[string]recordmodel.RecordListQuery{"s": {Scope: "all_records"}}, map[string]any{"unit_count": "2.5"})
	if len(currencyDivision) != 1 || currencyDivision[0]["average_ticket"] != "10.13" || currencyDivision[0]["unit_economics"] != "12.16" {
		t.Fatalf("currency division=%#v", currencyDivision)
	}

	payments := execute(reportmodel.ReportObjectSQLSchema{
		SQL: `SELECT p.kind AS payment_kind, SUM(p.amount) AS paid_amount FROM payment p GROUP BY p.kind ORDER BY payment_kind LIMIT 10`, SourceObjects: []string{"payment"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "payment_kind", Type: "text", Kind: "dimension"}, {Key: "paid_amount", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}},
	}, map[string]recordmodel.RecordListQuery{"p": {Scope: "owned_records", OwnerField: "owner_id", PrincipalUserID: "user-1"}})
	if len(payments) != 1 || payments[0]["payment_kind"] != "cash" || payments[0]["paid_amount"] != "15.15" {
		t.Fatalf("payments=%#v", payments)
	}

	joined := execute(reportmodel.ReportObjectSQLSchema{
		SQL: `SELECT s.id AS sale_id, COALESCE(SUM(p.amount), 0) AS visible_paid FROM sale s LEFT JOIN payment p ON p.sale_id = s.id GROUP BY s.id ORDER BY sale_id LIMIT 10`, SourceObjects: []string{"sale", "payment"},
		JoinCardinalities: []reportmodel.ReportObjectSQLCardinality{{Alias: "p", Cardinality: "one_to_many"}},
		ResultSchema:      []reportmodel.ReportResultColumnSchema{{Key: "sale_id", Type: "text", Kind: "dimension"}, {Key: "visible_paid", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}},
	}, map[string]recordmodel.RecordListQuery{"s": {Scope: "all_records"}, "p": {Scope: "owned_records", OwnerField: "owner_id", PrincipalUserID: "user-1"}})
	if len(joined) != 3 || joined[0]["visible_paid"] != "10.10" || joined[1]["visible_paid"] != "5.05" || joined[2]["visible_paid"] != "0.00" {
		t.Fatalf("left joined=%#v", joined)
	}
	zeroDivision := execute(reportmodel.ReportObjectSQLSchema{
		SQL: `SELECT s.id AS sale_id, s.units / NULLIF(s.units, 0) AS safe_ratio FROM sale s ORDER BY sale_id LIMIT 10`, SourceObjects: []string{"sale"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "sale_id", Type: "text", Kind: "dimension"}, {Key: "safe_ratio", Type: "decimal", Kind: "measure"}},
	}, map[string]recordmodel.RecordListQuery{"s": {Scope: "all_records"}})
	if len(zeroDivision) != 3 || zeroDivision[0]["safe_ratio"] != "1" || zeroDivision[1]["safe_ratio"] != "" || zeroDivision[2]["safe_ratio"] != "1" {
		t.Fatalf("zero division=%#v", zeroDivision)
	}

	countSchema := reportmodel.ReportObjectSQLSchema{SQL: `SELECT COUNT(*) AS visible_count FROM sale s LIMIT 1`, SourceObjects: []string{"sale"}, ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "visible_count", Type: "integer", Kind: "measure"}}}
	scopes := map[string]struct {
		query recordmodel.RecordListQuery
		want  string
	}{
		"owned":      {query: recordmodel.RecordListQuery{Scope: "owned_records", OwnerField: "employee_id", PrincipalUserID: "e1"}, want: "2"},
		"team":       {query: recordmodel.RecordListQuery{Scope: "team", TeamField: "team_id", PrincipalTeamIDs: []string{"t1"}}, want: "2"},
		"department": {query: recordmodel.RecordListQuery{Scope: "department", DepartmentPathField: "department_path", PrincipalDepartmentPath: "sales/east"}, want: "2"},
		"custom": {query: recordmodel.RecordListQuery{Scope: "custom", RootObjectKey: "sale", ScopeExpression: &recordmodel.RecordScopeExpression{
			Operator: "eq", FieldKey: "status", Values: []string{"paid"},
		}}, want: "2"},
	}
	for name, scope := range scopes {
		t.Run("scope_"+name, func(t *testing.T) {
			rows := execute(countSchema, map[string]recordmodel.RecordListQuery{"s": scope.query})
			if len(rows) != 1 || rows[0]["visible_count"] != scope.want {
				t.Fatalf("rows=%#v", rows)
			}
		})
	}
	injectionSchema := countSchema
	injectionSchema.SQL = `SELECT COUNT(*) AS visible_count FROM sale s WHERE s.status = :status LIMIT 1`
	injectionSchema.Parameters = []reportmodel.ReportObjectSQLParameter{{Key: "status", Type: "text", Required: true}}
	injectionRows := executeWithParameters(injectionSchema, map[string]recordmodel.RecordListQuery{"s": {Scope: "all_records"}}, map[string]any{"status": `paid' OR 1=1 --`})
	if len(injectionRows) != 1 || injectionRows[0]["visible_count"] != "0" {
		t.Fatalf("parameter injection escaped binding: %#v", injectionRows)
	}
}

func TestReportObjectSQLExecutesInexactNumberAndExactPercentAggregatesWithRows(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "back-aggregates.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE back_ledger (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT, quantity REAL, rate_snapshot TEXT, UNIQUE(workspace_id,id))`); err != nil {
		t.Fatal(err)
	}
	percentConfig := map[string]any{"precision": 8, "scale": 2}
	percent, err := recordmodel.RecordNormalizeDecimalConfig(percentConfig)
	if err != nil {
		t.Fatal(err)
	}
	encodedRate, err := recordmodel.RecordEncodeSQLiteDecimal("7.50", percent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO back_ledger VALUES ('workspace-a','back-1','now','now','posted',1.25,?)`, encodedRate); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "back_ledger", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "text"}, {Key: "quantity", Type: "number"}, {Key: "rate_snapshot", Type: "percent", Config: percentConfig},
	}}
	schema := reportmodel.ReportObjectSQLSchema{
		SQL:           `SELECT SUM(COALESCE(b.quantity,0.0)) AS quantity_total, SUM(CASE WHEN b.status = :status THEN b.quantity ELSE 0.0 END) AS posted_quantity, MAX(COALESCE(b.rate_snapshot,0.0)) AS maximum_rate FROM back_ledger b LIMIT 1`,
		SourceObjects: []string{"back_ledger"},
		Parameters:    []reportmodel.ReportObjectSQLParameter{{Key: "status", Type: "text", Required: true}},
		ResultSchema: []reportmodel.ReportResultColumnSchema{
			{Key: "quantity_total", Type: "number", Kind: "measure"},
			{Key: "posted_quantity", Type: "number", Kind: "measure"},
			{Key: "maximum_rate", Type: "decimal", Kind: "measure"},
		},
	}
	objects := map[string]definitionmodel.ObjectSchema{"back_ledger": object}
	plan, err := reportcontract.CompileReportObjectSQL(schema, objects)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ResultSchema[2].Precision != 8 || plan.ResultSchema[2].Scale != 2 {
		t.Fatalf("exact percent result schema=%#v", plan.ResultSchema[2])
	}
	query := recordmodel.RecordListQuery{Scope: "all_records", SelectFields: append([]string(nil), plan.Sources[0].Fields...)}
	result, err := NewReportDatasetStore(store).ExecuteReportObjectSQL(t.Context(), reportcontract.ReportObjectSQLExecutionRequest{
		WorkspaceID: "workspace-a", Plan: plan, Objects: map[string]definitionmodel.ObjectSchema{"b": object}, Queries: map[string]recordmodel.RecordListQuery{"b": query}, Parameters: map[string]any{"status": "posted"}, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0]["quantity_total"] != "1.25" || result.Rows[0]["posted_quantity"] != "1.25" || result.Rows[0]["maximum_rate"] != "7.50" {
		t.Fatalf("rows=%#v", result.Rows)
	}
	unsafe := schema
	unsafe.ResultSchema = append([]reportmodel.ReportResultColumnSchema(nil), schema.ResultSchema...)
	unsafe.ResultSchema[0].Type = "decimal"
	if _, err := reportcontract.CompileReportObjectSQL(unsafe, objects); err == nil {
		t.Fatal("inexact number aggregate was accepted as exact decimal")
	}
}

func aliasObjects(plan reportmodel.ReportObjectSQLPlan, objects map[string]definitionmodel.ObjectSchema) map[string]definitionmodel.ObjectSchema {
	result := make(map[string]definitionmodel.ObjectSchema, len(plan.Sources))
	for _, source := range plan.Sources {
		result[source.Alias] = objects[source.ObjectKey]
	}
	return result
}
