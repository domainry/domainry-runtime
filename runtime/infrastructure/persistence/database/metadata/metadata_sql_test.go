package metadata

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
)

var errMetadataSQL = errors.New("scripted metadata SQL failure")

type metadataSQLState struct {
	execSteps                        []metadataSQLExecStep
	querySteps                       []metadataSQLQueryStep
	queryLog                         []string
	execLog                          []string
	beginErr, commitErr, rollbackErr error
	queryHook                        func()
}

type metadataSQLExecStep struct {
	rows         int64
	err, rowsErr error
}
type metadataSQLQueryStep struct {
	columns      []string
	rows         [][]driver.Value
	err, nextErr error
	closeErr     error
}

func openMetadataScriptedDB(state *metadataSQLState) *sql.DB {
	return sql.OpenDB(metadataSQLConnector{state})
}

type metadataSQLConnector struct{ state *metadataSQLState }

func (c metadataSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &metadataSQLConn{c.state}, nil
}
func (metadataSQLConnector) Driver() driver.Driver { return metadataSQLDriver{} }

type metadataSQLDriver struct{}

func (metadataSQLDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type metadataSQLConn struct{ state *metadataSQLState }

func (*metadataSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*metadataSQLConn) Close() error                        { return nil }
func (c *metadataSQLConn) Begin() (driver.Tx, error) {
	if c.state.beginErr != nil {
		return nil, c.state.beginErr
	}
	return metadataSQLTx{c.state}, nil
}
func (c *metadataSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *metadataSQLConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.state.execLog = append(c.state.execLog, query)
	step := metadataSQLExecStep{rows: 1}
	if len(c.state.execSteps) > 0 {
		step, c.state.execSteps = c.state.execSteps[0], c.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return metadataSQLResult{step.rows, step.rowsErr}, nil
}
func (c *metadataSQLConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.state.queryLog = append(c.state.queryLog, query)
	if c.state.queryHook != nil {
		c.state.queryHook()
	}
	step := metadataSQLQueryStep{}
	if len(c.state.querySteps) > 0 {
		step, c.state.querySteps = c.state.querySteps[0], c.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &metadataSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr, closeErr: step.closeErr}, nil
}

type metadataSQLResult struct {
	rows int64
	err  error
}

func (metadataSQLResult) LastInsertId() (int64, error)   { return 0, nil }
func (r metadataSQLResult) RowsAffected() (int64, error) { return r.rows, r.err }

type metadataSQLTx struct{ state *metadataSQLState }

func (t metadataSQLTx) Commit() error   { return t.state.commitErr }
func (t metadataSQLTx) Rollback() error { return t.state.rollbackErr }

type metadataSQLRows struct {
	columns  []string
	rows     [][]driver.Value
	index    int
	nextErr  error
	closeErr error
}

func (r *metadataSQLRows) Columns() []string { return r.columns }
func (r *metadataSQLRows) Close() error      { return r.closeErr }
func (r *metadataSQLRows) Next(values []driver.Value) error {
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

func scriptedMetadataStore(t *testing.T, state *metadataSQLState, store MetadataStore) MetadataStore {
	t.Helper()
	store.db = openMetadataScriptedDB(state)
	store.schemaDB = store.db
	t.Cleanup(func() { _ = store.db.Close() })
	return store
}
