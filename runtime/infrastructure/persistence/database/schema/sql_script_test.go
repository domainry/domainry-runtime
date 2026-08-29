package schema

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

var errSchemaSQL = errors.New("scripted schema SQL failure")

type schemaSQLState struct {
	execSteps   []schemaSQLExecStep
	execQueries []string
	querySteps  []schemaSQLQueryStep
	beginErr    error
	commitErr   error
}

type schemaSQLExecStep struct{ err error }
type schemaSQLQueryStep struct {
	columns      []string
	rows         [][]driver.Value
	err, nextErr error
}

func openSchemaScriptedDB(state *schemaSQLState) *sql.DB {
	return sql.OpenDB(schemaSQLConnector{state})
}

type schemaSQLConnector struct{ state *schemaSQLState }

func (c schemaSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &schemaSQLConn{c.state}, nil
}
func (schemaSQLConnector) Driver() driver.Driver { return schemaSQLDriver{} }

type schemaSQLDriver struct{}

func (schemaSQLDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use schema connector")
}

type schemaSQLConn struct{ state *schemaSQLState }

func (*schemaSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*schemaSQLConn) Close() error                        { return nil }
func (c *schemaSQLConn) Begin() (driver.Tx, error) {
	if c.state.beginErr != nil {
		return nil, c.state.beginErr
	}
	return schemaSQLTx{state: c.state}, nil
}
func (c *schemaSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *schemaSQLConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.state.execQueries = append(c.state.execQueries, query)
	step := schemaSQLExecStep{}
	if len(c.state.execSteps) > 0 {
		step, c.state.execSteps = c.state.execSteps[0], c.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return driver.RowsAffected(1), nil
}
func (c *schemaSQLConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	step := schemaSQLQueryStep{}
	if len(c.state.querySteps) > 0 {
		step, c.state.querySteps = c.state.querySteps[0], c.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &schemaSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr}, nil
}

type schemaSQLTx struct{ state *schemaSQLState }

func (t schemaSQLTx) Commit() error { return t.state.commitErr }
func (schemaSQLTx) Rollback() error { return nil }

type schemaSQLRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	nextErr error
}

func (r *schemaSQLRows) Columns() []string { return r.columns }
func (*schemaSQLRows) Close() error        { return nil }
func (r *schemaSQLRows) Next(values []driver.Value) error {
	if r.index < len(r.rows) {
		copy(values, r.rows[r.index])
		r.index++
		return nil
	}
	if r.nextErr != nil {
		err := r.nextErr
		r.nextErr = nil
		return err
	}
	return io.EOF
}

type scriptedSchemaStore struct {
	db        *sql.DB
	ensureErr error
	driver    string
}

func (s scriptedSchemaStore) SchemaDB() SQLDatabase { return s.db }
func (s scriptedSchemaStore) Driver() string {
	if s.driver != "" {
		return s.driver
	}
	return "sqlite"
}
func (scriptedSchemaStore) DatabaseSchema() string              { return "main" }
func (scriptedSchemaStore) Identifier(value string) string      { return `"` + value + `"` }
func (scriptedSchemaStore) TableIdentifier(value string) string { return `"` + value + `"` }
func (scriptedSchemaStore) Placeholder(int) string              { return "?" }
func (scriptedSchemaStore) CreateIndexIfMissing(context.Context, string, string, bool, ...string) error {
	return nil
}
func (s scriptedSchemaStore) EnsureRuntimeColumn(context.Context, string, string, string) error {
	return s.ensureErr
}
func (s scriptedSchemaStore) RuntimeTableExists(ctx context.Context, table string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&count)
	return count > 0, err
}
func (scriptedSchemaStore) MetadataIDColumnType() string                { return "TEXT" }
func (scriptedSchemaStore) LocalizedTextKeyColumnType() string          { return "TEXT" }
func (scriptedSchemaStore) RuntimeColumnDefinition(value string) string { return value }
func (scriptedSchemaStore) RuntimeProfile() persistencedriver.EngineProfile {
	return sqlite.NewEngine()
}
func (scriptedSchemaStore) RuntimeRenderer() ormdialect.Renderer {
	return sqlite.NewEngine().SQLDialect().WithSchema("")
}
