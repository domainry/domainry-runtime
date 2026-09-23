package record

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
)

var errRecordSQL = errors.New("scripted record SQL failure")

type recordSQLState struct {
	execSteps                        []recordSQLExecStep
	querySteps                       []recordSQLQueryStep
	execStatements, queryStatements  []string
	execArguments, queryArguments    [][]driver.NamedValue
	beginErr, commitErr, rollbackErr error
	execHook                         func()
}

type recordSQLExecStep struct {
	rows         int64
	err, rowsErr error
}

type recordSQLQueryStep struct {
	columns                []string
	rows                   [][]driver.Value
	err, nextErr, closeErr error
}

func openRecordScriptedDB(state *recordSQLState) *sql.DB {
	return sql.OpenDB(recordSQLConnector{state: state})
}

type recordSQLConnector struct{ state *recordSQLState }

func (connector recordSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &recordSQLConn{state: connector.state}, nil
}
func (recordSQLConnector) Driver() driver.Driver { return recordSQLDriver{} }

type recordSQLDriver struct{}

func (recordSQLDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use record SQL connector")
}

type recordSQLConn struct{ state *recordSQLState }

func (*recordSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*recordSQLConn) Close() error                        { return nil }
func (connection *recordSQLConn) Begin() (driver.Tx, error) {
	if connection.state.beginErr != nil {
		return nil, connection.state.beginErr
	}
	return recordSQLTx{state: connection.state}, nil
}
func (connection *recordSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return connection.Begin()
}
func (connection *recordSQLConn) ExecContext(_ context.Context, statement string, arguments []driver.NamedValue) (driver.Result, error) {
	connection.state.execStatements = append(connection.state.execStatements, statement)
	connection.state.execArguments = append(connection.state.execArguments, append([]driver.NamedValue(nil), arguments...))
	if connection.state.execHook != nil {
		connection.state.execHook()
		connection.state.execHook = nil
	}
	step := recordSQLExecStep{rows: 1}
	if len(connection.state.execSteps) > 0 {
		step, connection.state.execSteps = connection.state.execSteps[0], connection.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return recordSQLResult{rows: step.rows, err: step.rowsErr}, nil
}
func (connection *recordSQLConn) QueryContext(_ context.Context, statement string, arguments []driver.NamedValue) (driver.Rows, error) {
	connection.state.queryStatements = append(connection.state.queryStatements, statement)
	connection.state.queryArguments = append(connection.state.queryArguments, append([]driver.NamedValue(nil), arguments...))
	step := recordSQLQueryStep{}
	if len(connection.state.querySteps) > 0 {
		step, connection.state.querySteps = connection.state.querySteps[0], connection.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &recordSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr, closeErr: step.closeErr}, nil
}

type recordSQLResult struct {
	rows int64
	err  error
}

func (recordSQLResult) LastInsertId() (int64, error) { return 0, nil }
func (result recordSQLResult) RowsAffected() (int64, error) {
	return result.rows, result.err
}

type recordSQLTx struct{ state *recordSQLState }

func (transaction recordSQLTx) Commit() error   { return transaction.state.commitErr }
func (transaction recordSQLTx) Rollback() error { return transaction.state.rollbackErr }

type recordSQLRows struct {
	columns           []string
	rows              [][]driver.Value
	index             int
	nextErr, closeErr error
}

func (rows *recordSQLRows) Columns() []string { return rows.columns }
func (rows *recordSQLRows) Close() error      { return rows.closeErr }
func (rows *recordSQLRows) Next(values []driver.Value) error {
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

func scriptedRecordStore(t *testing.T, state *recordSQLState) RecordStore {
	t.Helper()
	runtimeStore := openRuntimeStore(t)
	store := NewRecordStore(runtimeStore)
	store.db = openRecordScriptedDB(state)
	t.Cleanup(func() { _ = store.db.Close() })
	return store
}
