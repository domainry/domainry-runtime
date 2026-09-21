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

func TestDispatchCallbackReceiptSchemaRendersForSupportedDialects(t *testing.T) {
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
			db := &dispatchCallbackSchemaCaptureDB{}
			store := &dispatchCallbackSchemaCaptureStore{db: db, driver: test.name, profile: test.profile, renderer: test.renderer}
			if err := EnsureDispatchCallbackReceiptSchema(t.Context(), store); err != nil {
				t.Fatal(err)
			}
			if len(db.statements) != 1 || !strings.Contains(db.statements[0], DispatchCallbackReceiptTable) ||
				!strings.Contains(db.statements[0], "request_fingerprint") || !strings.Contains(db.statements[0], "fencing_token") ||
				!strings.Contains(strings.ToLower(db.statements[0]), "primary key") {
				t.Fatalf("ddl=%q", db.statements)
			}
			if len(store.indexes) != 2 || store.indexes[0] != "uniq_dispatch_callback_scope:workspace_id,id,idempotency_key" ||
				store.indexes[1] != "idx_dispatch_callback_lease:workspace_id,status,lease_expires_at" {
				t.Fatalf("indexes=%#v", store.indexes)
			}
		})
	}
}

type dispatchCallbackSchemaCaptureDB struct{ statements []string }

func (d *dispatchCallbackSchemaCaptureDB) ExecContext(_ context.Context, statement string, _ ...any) (sql.Result, error) {
	d.statements = append(d.statements, statement)
	return dispatchCallbackSchemaResult(1), nil
}
func (*dispatchCallbackSchemaCaptureDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}
func (*dispatchCallbackSchemaCaptureDB) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}
func (*dispatchCallbackSchemaCaptureDB) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	return nil, nil
}

type dispatchCallbackSchemaResult int64

func (r dispatchCallbackSchemaResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r dispatchCallbackSchemaResult) RowsAffected() (int64, error) { return int64(r), nil }

type dispatchCallbackSchemaCaptureStore struct {
	db       *dispatchCallbackSchemaCaptureDB
	driver   string
	profile  persistencedriver.EngineProfile
	renderer ormdialect.Renderer
	indexes  []string
}

func (s *dispatchCallbackSchemaCaptureStore) SchemaDB() SQLDatabase { return s.db }
func (s *dispatchCallbackSchemaCaptureStore) Driver() string        { return s.driver }
func (*dispatchCallbackSchemaCaptureStore) DatabaseSchema() string  { return "" }
func (s *dispatchCallbackSchemaCaptureStore) Identifier(value string) string {
	return s.renderer.Identifier(value)
}
func (s *dispatchCallbackSchemaCaptureStore) TableIdentifier(value string) string {
	return s.renderer.Table(value)
}
func (s *dispatchCallbackSchemaCaptureStore) Placeholder(index int) string {
	return s.renderer.Placeholder(index)
}
func (s *dispatchCallbackSchemaCaptureStore) RuntimeProfile() persistencedriver.EngineProfile {
	return s.profile
}
func (s *dispatchCallbackSchemaCaptureStore) RuntimeRenderer() ormdialect.Renderer { return s.renderer }
func (*dispatchCallbackSchemaCaptureStore) EnsureRuntimeColumn(context.Context, string, string, string) error {
	return nil
}
func (*dispatchCallbackSchemaCaptureStore) RuntimeTableExists(context.Context, string) (bool, error) {
	return false, nil
}
func (*dispatchCallbackSchemaCaptureStore) ApplicationSchemaIDColumnType() string { return "TEXT" }
func (*dispatchCallbackSchemaCaptureStore) LocalizedTextKeyColumnType() string    { return "TEXT" }
func (*dispatchCallbackSchemaCaptureStore) RuntimeColumnDefinition(value string) string {
	return value
}
func (s *dispatchCallbackSchemaCaptureStore) CreateIndexIfMissing(_ context.Context, _ string, index string, _ bool, columns ...string) error {
	s.indexes = append(s.indexes, index+":"+strings.Join(columns, ","))
	return nil
}

var _ Store = (*dispatchCallbackSchemaCaptureStore)(nil)
