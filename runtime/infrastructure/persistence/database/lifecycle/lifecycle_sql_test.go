package lifecycle

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
)

type lifecycleSQLState struct {
	execSteps  []lifecycleSQLExecStep
	querySteps []lifecycleSQLQueryStep
	beginErr   error
	commitErr  error
}

type lifecycleSQLExecStep struct {
	rows    int64
	err     error
	rowsErr error
}

type lifecycleSQLQueryStep struct {
	columns  []string
	rows     [][]driver.Value
	err      error
	nextErr  error
	closeErr error
}

func openLifecycleScriptedDB(state *lifecycleSQLState) *sql.DB {
	return sql.OpenDB(lifecycleSQLConnector{state: state})
}

type lifecycleSQLConnector struct{ state *lifecycleSQLState }

func (c lifecycleSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &lifecycleSQLConn{state: c.state}, nil
}
func (lifecycleSQLConnector) Driver() driver.Driver { return lifecycleSQLDriver{} }

type lifecycleSQLDriver struct{}

func (lifecycleSQLDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type lifecycleSQLConn struct{ state *lifecycleSQLState }

func (*lifecycleSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*lifecycleSQLConn) Close() error                        { return nil }
func (c *lifecycleSQLConn) Begin() (driver.Tx, error) {
	if c.state.beginErr != nil {
		return nil, c.state.beginErr
	}
	return lifecycleSQLTx{state: c.state}, nil
}
func (c *lifecycleSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *lifecycleSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	step := lifecycleSQLExecStep{rows: 1}
	if len(c.state.execSteps) > 0 {
		step, c.state.execSteps = c.state.execSteps[0], c.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return lifecycleSQLResult{rows: step.rows, err: step.rowsErr}, nil
}
func (c *lifecycleSQLConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	step := lifecycleSQLQueryStep{}
	if len(c.state.querySteps) > 0 {
		step, c.state.querySteps = c.state.querySteps[0], c.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &lifecycleSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr, closeErr: step.closeErr}, nil
}

type lifecycleSQLResult struct {
	rows int64
	err  error
}

func (lifecycleSQLResult) LastInsertId() (int64, error)   { return 0, nil }
func (r lifecycleSQLResult) RowsAffected() (int64, error) { return r.rows, r.err }

type lifecycleSQLTx struct{ state *lifecycleSQLState }

func (t lifecycleSQLTx) Commit() error { return t.state.commitErr }
func (lifecycleSQLTx) Rollback() error { return nil }

type lifecycleSQLRows struct {
	columns  []string
	rows     [][]driver.Value
	index    int
	nextErr  error
	closeErr error
}

func (r *lifecycleSQLRows) Columns() []string { return r.columns }
func (r *lifecycleSQLRows) Close() error      { return r.closeErr }
func (r *lifecycleSQLRows) Next(values []driver.Value) error {
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
