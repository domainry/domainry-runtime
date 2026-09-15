package schema

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

func TestReportExportPrepareReceiptSchemaRendersForSupportedDialects(t *testing.T) {
	for _, test := range []struct {
		name     string
		profile  persistencedriver.EngineProfile
		renderer ormdialect.Renderer
	}{
		{name: "sqlite", profile: sqlite.NewEngine(), renderer: sqlite.NewEngine().SQLDialect().WithSchema("")},
		{name: "postgres", profile: postgres.NewEngine(), renderer: postgres.NewEngine().SQLDialect().WithSchema("")},
		{name: "mysql", profile: mysql.NewEngine(), renderer: mysql.NewEngine().SQLDialect().WithSchema("")},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := &reportExportSchemaCaptureDB{}
			store := &reportExportSchemaCaptureStore{db: db, driver: test.name, profile: test.profile, renderer: test.renderer}
			if err := EnsureReportExportPrepareReceiptSchema(t.Context(), store); err != nil {
				t.Fatal(err)
			}
			if len(db.statements) != 1 || !strings.Contains(db.statements[0], ReportExportPrepareReceiptTable) ||
				!strings.Contains(db.statements[0], "idempotency_key") || !strings.Contains(db.statements[0], "terminal_error_code") ||
				!strings.Contains(strings.ToLower(db.statements[0]), "primary key") {
				t.Fatalf("ddl=%q", db.statements)
			}
			if test.name == "mysql" && strings.Contains(db.statements[0], "`payload_json` TEXT NOT NULL DEFAULT") {
				t.Fatalf("MySQL report export DDL contains an unsupported TEXT default: %q", db.statements[0])
			}
			if len(store.indexes) != 3 || store.indexes[0] != "uniq_report_export_prepare_operation:workspace_id,operation_id" ||
				store.indexes[1] != "uniq_report_export_prepare_caller:workspace_id,operation_id,idempotency_key" ||
				store.indexes[2] != "idx_report_export_prepare_lease:workspace_id,status,lease_expires_at" {
				t.Fatalf("indexes=%#v", store.indexes)
			}
		})
	}
}

type reportExportSchemaCaptureDB struct{ statements []string }

func (d *reportExportSchemaCaptureDB) ExecContext(_ context.Context, statement string, _ ...any) (sql.Result, error) {
	d.statements = append(d.statements, statement)
	return reportExportSchemaResult(1), nil
}
func (*reportExportSchemaCaptureDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}
func (*reportExportSchemaCaptureDB) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}
func (*reportExportSchemaCaptureDB) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, nil
}

type reportExportSchemaResult int64

func (r reportExportSchemaResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r reportExportSchemaResult) RowsAffected() (int64, error) { return int64(r), nil }

type reportExportSchemaCaptureStore struct {
	db       *reportExportSchemaCaptureDB
	driver   string
	profile  persistencedriver.EngineProfile
	renderer ormdialect.Renderer
	indexes  []string
}

func (s *reportExportSchemaCaptureStore) SchemaDB() SQLDatabase { return s.db }
func (s *reportExportSchemaCaptureStore) Driver() string        { return s.driver }
func (*reportExportSchemaCaptureStore) DatabaseSchema() string  { return "" }
func (s *reportExportSchemaCaptureStore) Identifier(value string) string {
	return s.renderer.Identifier(value)
}
func (s *reportExportSchemaCaptureStore) TableIdentifier(value string) string {
	return s.renderer.Table(value)
}
func (s *reportExportSchemaCaptureStore) Placeholder(index int) string {
	return s.renderer.Placeholder(index)
}
func (s *reportExportSchemaCaptureStore) RuntimeProfile() persistencedriver.EngineProfile {
	return s.profile
}
func (s *reportExportSchemaCaptureStore) RuntimeRenderer() ormdialect.Renderer { return s.renderer }
func (*reportExportSchemaCaptureStore) EnsureRuntimeColumn(context.Context, string, string, string) error {
	return nil
}
func (*reportExportSchemaCaptureStore) RuntimeTableExists(context.Context, string) (bool, error) {
	return false, nil
}
func (*reportExportSchemaCaptureStore) ApplicationSchemaIDColumnType() string       { return "TEXT" }
func (*reportExportSchemaCaptureStore) LocalizedTextKeyColumnType() string          { return "TEXT" }
func (*reportExportSchemaCaptureStore) RuntimeColumnDefinition(value string) string { return value }
func (s *reportExportSchemaCaptureStore) CreateIndexIfMissing(_ context.Context, _ string, index string, _ bool, columns ...string) error {
	s.indexes = append(s.indexes, index+":"+strings.Join(columns, ","))
	return nil
}

var _ Store = (*reportExportSchemaCaptureStore)(nil)
