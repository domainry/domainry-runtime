package operations

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
)

var errOperationsSQL = errors.New("scripted operations SQL failure")

type operationsSQLState struct {
	execSteps                        []operationsSQLExecStep
	querySteps                       []operationsSQLQueryStep
	beginErr, commitErr, rollbackErr error
}

type operationsSQLExecStep struct {
	rows         int64
	err, rowsErr error
}

type operationsSQLQueryStep struct {
	columns      []string
	rows         [][]driver.Value
	err, nextErr error
}

func openOperationsScriptedDB(state *operationsSQLState) *sql.DB {
	return sql.OpenDB(operationsSQLConnector{state: state})
}

type operationsSQLConnector struct{ state *operationsSQLState }

func (connector operationsSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &operationsSQLConn{state: connector.state}, nil
}
func (operationsSQLConnector) Driver() driver.Driver { return operationsSQLDriver{} }

type operationsSQLDriver struct{}

func (operationsSQLDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use operations SQL connector")
}

type operationsSQLConn struct{ state *operationsSQLState }

func (*operationsSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*operationsSQLConn) Close() error                        { return nil }
func (connection *operationsSQLConn) Begin() (driver.Tx, error) {
	if connection.state.beginErr != nil {
		return nil, connection.state.beginErr
	}
	return operationsSQLTx{state: connection.state}, nil
}
func (connection *operationsSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return connection.Begin()
}
func (connection *operationsSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	step := operationsSQLExecStep{rows: 1}
	if len(connection.state.execSteps) > 0 {
		step, connection.state.execSteps = connection.state.execSteps[0], connection.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return operationsSQLResult{rows: step.rows, err: step.rowsErr}, nil
}
func (connection *operationsSQLConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	step := operationsSQLQueryStep{}
	if len(connection.state.querySteps) > 0 {
		step, connection.state.querySteps = connection.state.querySteps[0], connection.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &operationsSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr}, nil
}

type operationsSQLResult struct {
	rows int64
	err  error
}

func (operationsSQLResult) LastInsertId() (int64, error) { return 0, nil }
func (result operationsSQLResult) RowsAffected() (int64, error) {
	return result.rows, result.err
}

type operationsSQLTx struct{ state *operationsSQLState }

func (transaction operationsSQLTx) Commit() error   { return transaction.state.commitErr }
func (transaction operationsSQLTx) Rollback() error { return transaction.state.rollbackErr }

type operationsSQLRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	nextErr error
}

func (rows *operationsSQLRows) Columns() []string { return rows.columns }
func (*operationsSQLRows) Close() error           { return nil }
func (rows *operationsSQLRows) Next(values []driver.Value) error {
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

func scriptedOperationsStore(t *testing.T, state *operationsSQLState) OperationsStore {
	t.Helper()
	runtimeStore := openDatabaseRetirementExecutorStore(t)
	store := NewOperationsStore(runtimeStore)
	store.db = openOperationsScriptedDB(state)
	t.Cleanup(func() { _ = store.db.Close() })
	return store
}

func scriptedRetirementExecutor(t *testing.T, state *operationsSQLState) DatabaseRetirementSQLExecutor {
	t.Helper()
	runtimeStore := openDatabaseRetirementExecutorStore(t)
	executor := NewDatabaseRetirementSQLExecutor(runtimeStore, nil, nil)
	executor.db = openOperationsScriptedDB(state)
	t.Cleanup(func() { _ = executor.db.Close() })
	return executor
}
