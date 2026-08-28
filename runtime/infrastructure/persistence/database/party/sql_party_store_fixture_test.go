package party

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
)

func scriptedPartyStore(state *partySQLState) (*SQLPartyStore, func()) {
	db := sql.OpenDB(partySQLConnector{state: state})
	return NewSQLPartyStore(db, "sqlite"), func() { _ = db.Close() }
}

type partySQLState struct {
	execCount, execFailAt        int
	queryCount, queryFailAt      int
	failure, beginErr, commitErr error
	querySteps                   []partySQLQueryStep
}

type partySQLQueryStep struct {
	columns  []string
	rows     [][]driver.Value
	nextErr  error
	closeErr error
}

type partySQLConnector struct{ state *partySQLState }

func (c partySQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &partySQLConn{state: c.state}, nil
}

func (partySQLConnector) Driver() driver.Driver { return partySQLDriver{} }

type partySQLDriver struct{}

func (partySQLDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type partySQLConn struct{ state *partySQLState }

func (*partySQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*partySQLConn) Close() error                        { return nil }
func (c *partySQLConn) Begin() (driver.Tx, error) {
	if c.state.beginErr != nil {
		return nil, c.state.beginErr
	}
	return partySQLTx{state: c.state}, nil
}
func (c *partySQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *partySQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.state.execCount++
	if c.state.execCount == c.state.execFailAt {
		return nil, c.state.failure
	}
	return driver.RowsAffected(1), nil
}
func (c *partySQLConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.state.queryCount++
	if c.state.queryCount == c.state.queryFailAt {
		return nil, c.state.failure
	}
	if len(c.state.querySteps) == 0 {
		return &partySQLRows{}, nil
	}
	step := c.state.querySteps[0]
	c.state.querySteps = c.state.querySteps[1:]
	return &partySQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr, closeErr: step.closeErr}, nil
}

type partySQLTx struct{ state *partySQLState }

func (t partySQLTx) Commit() error { return t.state.commitErr }
func (partySQLTx) Rollback() error { return nil }

type partySQLRows struct {
	columns  []string
	rows     [][]driver.Value
	index    int
	nextErr  error
	closeErr error
}

func (r *partySQLRows) Columns() []string { return r.columns }
func (r *partySQLRows) Close() error      { return r.closeErr }
func (r *partySQLRows) Next(values []driver.Value) error {
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
