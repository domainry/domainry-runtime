package schema

import (
	"context"
	"database/sql"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type schemaCaptureDB struct{ statements []string }

func (d *schemaCaptureDB) ExecContext(_ context.Context, statement string, _ ...any) (sql.Result, error) {
	d.statements = append(d.statements, statement)
	return schemaCaptureResult(1), nil
}

func (*schemaCaptureDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}

func (*schemaCaptureDB) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }
func (*schemaCaptureDB) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) { return nil, nil }

type schemaCaptureResult int64

func (r schemaCaptureResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r schemaCaptureResult) RowsAffected() (int64, error) { return int64(r), nil }

type schemaCaptureStore struct {
	db       *schemaCaptureDB
	driver   string
	profile  persistencedriver.EngineProfile
	renderer ormdialect.Renderer
	indexes  []string
}

func (s *schemaCaptureStore) SchemaDB() SQLDatabase { return s.db }
func (s *schemaCaptureStore) Driver() string        { return s.driver }
func (*schemaCaptureStore) DatabaseSchema() string  { return "" }
func (s *schemaCaptureStore) Identifier(value string) string {
	return s.renderer.Identifier(value)
}
func (s *schemaCaptureStore) TableIdentifier(value string) string { return s.renderer.Table(value) }
func (s *schemaCaptureStore) Placeholder(index int) string        { return s.renderer.Placeholder(index) }
func (s *schemaCaptureStore) RuntimeProfile() persistencedriver.EngineProfile {
	return s.profile
}
func (s *schemaCaptureStore) RuntimeRenderer() ormdialect.Renderer { return s.renderer }
func (*schemaCaptureStore) RuntimeTableExists(context.Context, string) (bool, error) {
	return false, nil
}
func (*schemaCaptureStore) ApplicationSchemaIDColumnType() string       { return "TEXT" }
func (*schemaCaptureStore) LocalizedTextKeyColumnType() string          { return "TEXT" }
func (*schemaCaptureStore) RuntimeColumnDefinition(value string) string { return value }
func (s *schemaCaptureStore) CreateIndexIfMissing(_ context.Context, _ string, index string, _ bool, columns ...string) error {
	s.indexes = append(s.indexes, index+":"+strings.Join(columns, ","))
	return nil
}

var _ Store = (*schemaCaptureStore)(nil)
