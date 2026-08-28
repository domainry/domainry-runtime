package deployment

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strconv"
)

type releaseSQLStore struct{ db *sql.DB }

func (s releaseSQLStore) DB() *sql.DB                       { return s.db }
func (releaseSQLStore) TableIdentifier(value string) string { return `"` + value + `"` }
func (releaseSQLStore) Identifier(value string) string      { return `"` + value + `"` }
func (releaseSQLStore) Placeholder(int) string              { return "?" }

func scriptedReleaseStore(state *releaseSQLState) (RuntimeReleaseCohortStore, func()) {
	db := sql.OpenDB(releaseSQLConnector{state: state})
	return NewRuntimeReleaseCohortStore(releaseSQLStore{db: db}), func() { _ = db.Close() }
}

type releaseSQLState struct {
	beginErr, commitErr error
	execSteps           []releaseSQLExecStep
	querySteps          []releaseSQLQueryStep
}

type releaseSQLExecStep struct {
	rows    int64
	err     error
	rowsErr error
}

type releaseSQLQueryStep struct {
	columns []string
	rows    [][]driver.Value
	err     error
	nextErr error
}

type releaseSQLConnector struct{ state *releaseSQLState }

func (c releaseSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &releaseSQLConn{state: c.state}, nil
}
func (releaseSQLConnector) Driver() driver.Driver { return releaseSQLDriver{} }

type releaseSQLDriver struct{}

func (releaseSQLDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type releaseSQLConn struct{ state *releaseSQLState }

func (*releaseSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*releaseSQLConn) Close() error                        { return nil }
func (c *releaseSQLConn) Begin() (driver.Tx, error) {
	if c.state.beginErr != nil {
		return nil, c.state.beginErr
	}
	return releaseSQLTx{state: c.state}, nil
}
func (c *releaseSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *releaseSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	step := releaseSQLExecStep{rows: 1}
	if len(c.state.execSteps) > 0 {
		step, c.state.execSteps = c.state.execSteps[0], c.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return releaseSQLResult{rows: step.rows, err: step.rowsErr}, nil
}
func (c *releaseSQLConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	step := releaseSQLQueryStep{}
	if len(c.state.querySteps) > 0 {
		step, c.state.querySteps = c.state.querySteps[0], c.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &releaseSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr}, nil
}

type releaseSQLTx struct{ state *releaseSQLState }

func (t releaseSQLTx) Commit() error { return t.state.commitErr }
func (releaseSQLTx) Rollback() error { return nil }

type releaseSQLResult struct {
	rows int64
	err  error
}

func (releaseSQLResult) LastInsertId() (int64, error)   { return 0, nil }
func (r releaseSQLResult) RowsAffected() (int64, error) { return r.rows, r.err }

type releaseSQLRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	nextErr error
}

func (r *releaseSQLRows) Columns() []string { return r.columns }
func (*releaseSQLRows) Close() error        { return nil }
func (r *releaseSQLRows) Next(values []driver.Value) error {
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

func releaseColumns(count int) []string {
	out := make([]string, count)
	for index := range out {
		out[index] = "c" + strconv.Itoa(index)
	}
	return out
}
