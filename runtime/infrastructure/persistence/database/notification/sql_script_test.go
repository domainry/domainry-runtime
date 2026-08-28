package notification

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
)

var errNotificationSQL = errors.New("scripted notification SQL failure")

type notificationSQLState struct {
	execSteps                        []notificationSQLExecStep
	querySteps                       []notificationSQLQueryStep
	beginErr, commitErr, rollbackErr error
	execCalls                        int
}

type notificationSQLExecStep struct {
	rows         int64
	err, rowsErr error
}

type notificationSQLQueryStep struct {
	columns      []string
	rows         [][]driver.Value
	err, nextErr error
	closeErr     error
}

func openNotificationScriptedDB(state *notificationSQLState) *sql.DB {
	return sql.OpenDB(notificationSQLConnector{state: state})
}

type notificationSQLConnector struct{ state *notificationSQLState }

func (connector notificationSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &notificationSQLConn{state: connector.state}, nil
}
func (notificationSQLConnector) Driver() driver.Driver { return notificationSQLDriver{} }

type notificationSQLDriver struct{}

func (notificationSQLDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use notification SQL connector")
}

type notificationSQLConn struct{ state *notificationSQLState }

func (*notificationSQLConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*notificationSQLConn) Close() error                        { return nil }
func (connection *notificationSQLConn) Begin() (driver.Tx, error) {
	if connection.state.beginErr != nil {
		return nil, connection.state.beginErr
	}
	return notificationSQLTx{state: connection.state}, nil
}
func (connection *notificationSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return connection.Begin()
}
func (connection *notificationSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	connection.state.execCalls++
	step := notificationSQLExecStep{rows: 1}
	if len(connection.state.execSteps) > 0 {
		step, connection.state.execSteps = connection.state.execSteps[0], connection.state.execSteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return notificationSQLResult{rows: step.rows, err: step.rowsErr}, nil
}
func (connection *notificationSQLConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	step := notificationSQLQueryStep{}
	if len(connection.state.querySteps) > 0 {
		step, connection.state.querySteps = connection.state.querySteps[0], connection.state.querySteps[1:]
	}
	if step.err != nil {
		return nil, step.err
	}
	return &notificationSQLRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr, closeErr: step.closeErr}, nil
}

type notificationSQLResult struct {
	rows int64
	err  error
}

func (notificationSQLResult) LastInsertId() (int64, error) { return 0, nil }
func (result notificationSQLResult) RowsAffected() (int64, error) {
	return result.rows, result.err
}

type notificationSQLTx struct{ state *notificationSQLState }

func (transaction notificationSQLTx) Commit() error   { return transaction.state.commitErr }
func (transaction notificationSQLTx) Rollback() error { return transaction.state.rollbackErr }

type notificationSQLRows struct {
	columns  []string
	rows     [][]driver.Value
	index    int
	nextErr  error
	closeErr error
}

func (rows *notificationSQLRows) Columns() []string { return rows.columns }
func (rows *notificationSQLRows) Close() error      { return rows.closeErr }
func (rows *notificationSQLRows) Next(values []driver.Value) error {
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

func scriptedNotificationStore(t *testing.T, state *notificationSQLState) DeliveryMetricsStore {
	t.Helper()
	_, store := openNotificationFailureStore(t)
	store.db = openNotificationScriptedDB(state)
	t.Cleanup(func() { _ = store.db.Close() })
	return store
}
