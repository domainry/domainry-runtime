package integration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"

	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

type integrationSQLState struct {
	execSteps       []integrationSQLExecStep
	querySteps      []integrationSQLQueryStep
	beginErr        error
	commitErr       error
	rollbackErr     error
	queryWorkspaces []string
	queryStatements []string
}

type integrationSQLExecStep struct {
	rows    int64
	err     error
	rowsErr error
}

type integrationSQLQueryStep struct {
	columns []string
	rows    [][]driver.Value
	err     error
	nextErr error
}

func openIntegrationScriptedDB(state *integrationSQLState) *sql.DB {
	return sql.OpenDB(integrationSQLConnector{state: state})
}

type integrationSQLConnector struct{ state *integrationSQLState }

func (c integrationSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &integrationSQLConn{state: c.state}, nil
}
func (integrationSQLConnector) Driver() driver.Driver { return integrationSQLDriver{} }

type integrationSQLDriver struct{}

func (integrationSQLDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type integrationSQLConn struct{ state *integrationSQLState }

func (*integrationSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*integrationSQLConn) Close() error                        { return nil }
func (c *integrationSQLConn) Begin() (driver.Tx, error) {
	if c.state.beginErr != nil {
		return nil, c.state.beginErr
	}
	return integrationSQLTx{state: c.state}, nil
}
func (c *integrationSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *integrationSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	step := integrationSQLExecStep{rows: 1}
	if len(c.state.execSteps) > 0 {
		step, c.state.execSteps = c.state.execSteps[0], c.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return integrationSQLResult{rows: step.rows, err: step.rowsErr}, nil
}
func (c *integrationSQLConn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.state.queryWorkspaces = append(c.state.queryWorkspaces, requestcontext.WorkspaceID(ctx))
	c.state.queryStatements = append(c.state.queryStatements, query)
	step := integrationSQLQueryStep{}
	if len(c.state.querySteps) > 0 {
		step, c.state.querySteps = c.state.querySteps[0], c.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &integrationSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr}, nil
}

type integrationSQLResult struct {
	rows int64
	err  error
}

func (integrationSQLResult) LastInsertId() (int64, error)   { return 0, nil }
func (r integrationSQLResult) RowsAffected() (int64, error) { return r.rows, r.err }

type integrationSQLTx struct{ state *integrationSQLState }

func (t integrationSQLTx) Commit() error   { return t.state.commitErr }
func (t integrationSQLTx) Rollback() error { return t.state.rollbackErr }

type integrationSQLRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	nextErr error
}

func (r *integrationSQLRows) Columns() []string { return r.columns }
func (*integrationSQLRows) Close() error        { return nil }
func (r *integrationSQLRows) Next(values []driver.Value) error {
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
