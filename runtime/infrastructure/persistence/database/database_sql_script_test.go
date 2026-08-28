package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
)

var errDatabaseSQL = errors.New("scripted database SQL failure")

type databaseSQLState struct {
	execSteps                        []databaseSQLExecStep
	querySteps                       []databaseSQLQueryStep
	beginErr, commitErr, rollbackErr error
	closeErr                         error
	closeStarted, closeBlock         chan struct{}
	execHook                         func()
	queryHook                        func()
}

type databaseSQLExecStep struct {
	rows         int64
	err, rowsErr error
}

type databaseSQLQueryStep struct {
	columns                []string
	rows                   [][]driver.Value
	err, nextErr, closeErr error
}

func openDatabaseScriptedDB(state *databaseSQLState) *sql.DB {
	return sql.OpenDB(databaseSQLConnector{state: state})
}

type databaseSQLConnector struct{ state *databaseSQLState }

func (connector databaseSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &databaseSQLConn{state: connector.state}, nil
}
func (databaseSQLConnector) Driver() driver.Driver { return databaseSQLDriver{} }

type databaseSQLDriver struct{}

func (databaseSQLDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use database SQL connector")
}

type databaseSQLConn struct{ state *databaseSQLState }

func (*databaseSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (connection *databaseSQLConn) Close() error {
	if connection.state.closeStarted != nil {
		select {
		case <-connection.state.closeStarted:
		default:
			close(connection.state.closeStarted)
		}
	}
	if connection.state.closeBlock != nil {
		<-connection.state.closeBlock
	}
	return connection.state.closeErr
}
func (connection *databaseSQLConn) Begin() (driver.Tx, error) {
	if connection.state.beginErr != nil {
		return nil, connection.state.beginErr
	}
	return databaseSQLTx{state: connection.state}, nil
}
func (connection *databaseSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return connection.Begin()
}
func (connection *databaseSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if connection.state.execHook != nil {
		connection.state.execHook()
		connection.state.execHook = nil
	}
	step := databaseSQLExecStep{rows: 1}
	if len(connection.state.execSteps) > 0 {
		step, connection.state.execSteps = connection.state.execSteps[0], connection.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return databaseSQLResult{rows: step.rows, err: step.rowsErr}, nil
}
func (connection *databaseSQLConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if connection.state.queryHook != nil {
		connection.state.queryHook()
		connection.state.queryHook = nil
	}
	step := databaseSQLQueryStep{}
	if len(connection.state.querySteps) > 0 {
		step, connection.state.querySteps = connection.state.querySteps[0], connection.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &databaseSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr, closeErr: step.closeErr}, nil
}

type databaseSQLResult struct {
	rows int64
	err  error
}

func (databaseSQLResult) LastInsertId() (int64, error) { return 0, nil }
func (result databaseSQLResult) RowsAffected() (int64, error) {
	return result.rows, result.err
}

type databaseSQLTx struct{ state *databaseSQLState }

func (transaction databaseSQLTx) Commit() error   { return transaction.state.commitErr }
func (transaction databaseSQLTx) Rollback() error { return transaction.state.rollbackErr }

type databaseSQLRows struct {
	columns           []string
	rows              [][]driver.Value
	index             int
	nextErr, closeErr error
}

func (rows *databaseSQLRows) Columns() []string { return rows.columns }
func (rows *databaseSQLRows) Close() error      { return rows.closeErr }
func (rows *databaseSQLRows) Next(values []driver.Value) error {
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
