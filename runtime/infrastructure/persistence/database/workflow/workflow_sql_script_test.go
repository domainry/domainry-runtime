package workflow

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
)

var errWorkflowSQL = errors.New("scripted workflow SQL failure")

type workflowSQLState struct {
	execSteps                        []workflowSQLExecStep
	querySteps                       []workflowSQLQueryStep
	beginErr, commitErr, rollbackErr error
	execHook                         func()
}

type workflowSQLExecStep struct {
	rows         int64
	err, rowsErr error
}

type workflowSQLQueryStep struct {
	columns                []string
	rows                   [][]driver.Value
	err, nextErr, closeErr error
}

func openWorkflowScriptedDB(state *workflowSQLState) *sql.DB {
	return sql.OpenDB(workflowSQLConnector{state: state})
}

type workflowSQLConnector struct{ state *workflowSQLState }

func (connector workflowSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &workflowSQLConn{state: connector.state}, nil
}
func (workflowSQLConnector) Driver() driver.Driver { return workflowSQLDriver{} }

type workflowSQLDriver struct{}

func (workflowSQLDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use workflow SQL connector")
}

type workflowSQLConn struct{ state *workflowSQLState }

func (*workflowSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*workflowSQLConn) Close() error                        { return nil }
func (connection *workflowSQLConn) Begin() (driver.Tx, error) {
	if connection.state.beginErr != nil {
		return nil, connection.state.beginErr
	}
	return workflowSQLTx{state: connection.state}, nil
}
func (connection *workflowSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return connection.Begin()
}
func (connection *workflowSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if connection.state.execHook != nil {
		connection.state.execHook()
		connection.state.execHook = nil
	}
	step := workflowSQLExecStep{rows: 1}
	if len(connection.state.execSteps) > 0 {
		step, connection.state.execSteps = connection.state.execSteps[0], connection.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return workflowSQLResult{rows: step.rows, err: step.rowsErr}, nil
}
func (connection *workflowSQLConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	step := workflowSQLQueryStep{}
	if len(connection.state.querySteps) > 0 {
		step, connection.state.querySteps = connection.state.querySteps[0], connection.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &workflowSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr, closeErr: step.closeErr}, nil
}

type workflowSQLResult struct {
	rows int64
	err  error
}

func (workflowSQLResult) LastInsertId() (int64, error) { return 0, nil }
func (result workflowSQLResult) RowsAffected() (int64, error) {
	return result.rows, result.err
}

type workflowSQLTx struct{ state *workflowSQLState }

func (transaction workflowSQLTx) Commit() error   { return transaction.state.commitErr }
func (transaction workflowSQLTx) Rollback() error { return transaction.state.rollbackErr }

type workflowSQLRows struct {
	columns           []string
	rows              [][]driver.Value
	index             int
	nextErr, closeErr error
}

func (rows *workflowSQLRows) Columns() []string { return rows.columns }
func (rows *workflowSQLRows) Close() error      { return rows.closeErr }
func (rows *workflowSQLRows) Next(values []driver.Value) error {
	if rows.index < len(rows.rows) {
		copy(values, rows.rows[rows.index])
		rows.index++
		return nil
	}
	if rows.nextErr != nil {
		err := rows.nextErr
		rows.nextErr = nil
		return err
	}
	return io.EOF
}
