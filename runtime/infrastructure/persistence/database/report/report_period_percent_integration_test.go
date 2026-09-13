package report

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	ormschema "github.com/domainry/domainry-orm/schema"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestReportPeriodPercentUsesStoredScaleAndFloorsOnce(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "period-percent.db"), IntegrationSecretKey: "period-percent-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := store.EnsureApplicationSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	columns := []ormschema.ColumnDefinition{}
	for _, field := range []string{"workspace_id", "id", "created_at", "updated_at", "owner_org_id", "rate"} {
		columns = append(columns, ormschema.Column(field, ormschema.Text()))
	}
	columns = append(columns, ormschema.Column("fees", ormschema.Integer()))
	statement, args, err := ormschema.NewTable(store.RuntimeRenderer(), "nomination").Columns(columns...).PrimaryKey("workspace_id", "id").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "nomination", Fields: []definitionmodel.FieldSchema{
		{Key: "fees", Type: "integer"}, {Key: "rate", Type: "percent", Config: map[string]any{"precision": 7, "scale": 4, "rounding": "half_even"}},
	}}
	objects := map[string]definitionmodel.ObjectSchema{"nomination": object}
	records := recordpersistence.NewRecordStore(store)
	cases := []struct {
		name                string
		fees                []int64
		rate, product, tier string
	}{
		{"accumulated", []int64{40000, 20000}, "0.1000", "6000", "6000"},
		{"below_threshold", []int64{49999}, "0.1000", "4999", "0"},
		{"at_threshold", []int64{50000}, "0.1000", "5000", "5000"},
		{"fractional_yen", []int64{40001, 20000}, "0.1000", "6000", "6000"},
		{"zero_rate", []int64{60000}, "0.0000", "0", "0"},
		{"negative_floor", []int64{-1}, "0.0001", "-1", "0"},
		// Each source value is JSON-safe; the SQL aggregate exceeds 2^53.
		{"beyond_float_integer_precision", []int64{4503599627370496, 4503599627370497}, "0.9999", "9006298534815518", "9006298534815518"},
	}
	for _, test := range cases {
		for index, fees := range test.fees {
			if err := records.InsertRecord(t.Context(), "workspace-a", object, recordmodel.Record{ID: fmt.Sprintf("%s-fee-%d", test.name, index), OwnerOrgID: test.name, CreatedAt: "2026-09-08T00:00:00Z", UpdatedAt: "2026-09-08T00:00:00Z", Data: map[string]any{"fees": fees, "rate": test.rate}}); err != nil {
				t.Fatal(err)
			}
		}
		// Same store reference in a different workspace must never enter totals.
		if err := records.InsertRecord(t.Context(), "workspace-b", object, recordmodel.Record{ID: test.name + "-foreign", OwnerOrgID: test.name, CreatedAt: "2026-09-08T00:00:00Z", UpdatedAt: "2026-09-08T00:00:00Z", Data: map[string]any{"fees": int64(999999), "rate": "0.9999"}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for name, calculation := range map[string]struct{ expression, want string }{
				"scaled_percentage": {"FLOOR(SUM(s.fees) * MAX(s.rate))", test.product},
				"reversed_operands": {"FLOOR(MAX(s.rate) * SUM(s.fees))", test.product},
				"period_case_zero":  {"FLOOR(SUM(s.fees) * (CASE WHEN SUM(s.fees) >= 50000 THEN MAX(s.rate) ELSE 0 END))", test.tier},
			} {
				t.Run(name, func(t *testing.T) {
					plan, err := reportcontract.CompileReportObjectSQL(reportmodel.ReportObjectSQLSchema{SQL: "SELECT " + calculation.expression + " AS period_back, MAX(s.rate) AS selected_rate FROM nomination s LIMIT 1"}, objects)
					if err != nil {
						t.Fatal(err)
					}
					result, err := NewReportSQLStore(store).ExecuteReportObjectSQL(t.Context(), reportcontract.ReportObjectSQLExecutionRequest{
						WorkspaceID: "workspace-a", Plan: plan, Objects: map[string]definitionmodel.ObjectSchema{"s": object}, Timeout: 2 * time.Second,
						Queries: map[string]recordmodel.RecordListQuery{"s": {AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, OwnerOrganizationScopeID: test.name, SelectFields: plan.Sources[0].Fields}},
					})
					if err != nil {
						t.Fatal(err)
					}
					if len(result.Rows) != 1 || result.Rows[0]["period_back"] != calculation.want || result.Rows[0]["selected_rate"] != test.rate {
						t.Fatalf("expected back=%s and original rate=%s in selected store/workspace only, rows=%+v", calculation.want, test.rate, result.Rows)
					}
				})
			}
		})
	}
}
