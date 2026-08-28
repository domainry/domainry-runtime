package datamigration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
)

type inventoryScriptResult struct {
	columns  []string
	rows     [][]driver.Value
	err      error
	rowsErr  error
	closeErr error
}

type inventoryScript struct {
	mu         sync.Mutex
	results    []inventoryScriptResult
	execErrors []error
	queries    []string
	execs      []string
}

func (script *inventoryScript) next(query string) (driver.Rows, error) {
	script.mu.Lock()
	defer script.mu.Unlock()
	script.queries = append(script.queries, query)
	if len(script.results) == 0 {
		return nil, errors.New("unexpected inventory query: " + query)
	}
	result := script.results[0]
	script.results = script.results[1:]
	if result.err != nil {
		return nil, result.err
	}
	return &inventoryScriptRows{columns: result.columns, rows: result.rows, terminalErr: result.rowsErr, closeErr: result.closeErr}, nil
}

func (script *inventoryScript) exec(query string) (driver.Result, error) {
	script.mu.Lock()
	defer script.mu.Unlock()
	script.execs = append(script.execs, query)
	if len(script.execErrors) == 0 {
		return nil, errors.New("unexpected inventory exec: " + query)
	}
	err := script.execErrors[0]
	script.execErrors = script.execErrors[1:]
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

type inventoryScriptDriver struct{}

type inventoryScriptConnector struct{ script *inventoryScript }

type inventoryScriptConn struct{ script *inventoryScript }

type inventoryScriptRows struct {
	columns     []string
	rows        [][]driver.Value
	index       int
	terminalErr error
	closeErr    error
}

var (
	inventoryScriptsMu  sync.Mutex
	inventoryScripts    = map[string]*inventoryScript{}
	inventoryDriverOnce sync.Once
)

func (inventoryScriptDriver) Open(name string) (driver.Conn, error) {
	inventoryScriptsMu.Lock()
	script := inventoryScripts[name]
	inventoryScriptsMu.Unlock()
	if script == nil {
		return nil, errors.New("inventory script not found")
	}
	return &inventoryScriptConn{script: script}, nil
}

func (inventoryScriptDriver) OpenConnector(name string) (driver.Connector, error) {
	inventoryScriptsMu.Lock()
	script := inventoryScripts[name]
	inventoryScriptsMu.Unlock()
	if script == nil {
		return nil, errors.New("inventory script not found")
	}
	return &inventoryScriptConnector{script: script}, nil
}

func (connector *inventoryScriptConnector) Connect(context.Context) (driver.Conn, error) {
	return &inventoryScriptConn{script: connector.script}, nil
}

func (*inventoryScriptConnector) Driver() driver.Driver { return inventoryScriptDriver{} }

func (*inventoryScriptConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (*inventoryScriptConn) Close() error { return nil }

func (*inventoryScriptConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (conn *inventoryScriptConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	return conn.script.next(query)
}

func (conn *inventoryScriptConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	return conn.script.exec(query)
}

func (rows *inventoryScriptRows) Columns() []string { return rows.columns }

func (rows *inventoryScriptRows) Close() error { return rows.closeErr }

func (rows *inventoryScriptRows) Next(values []driver.Value) error {
	if rows.index >= len(rows.rows) {
		if rows.terminalErr != nil {
			err := rows.terminalErr
			rows.terminalErr = nil
			return err
		}
		return io.EOF
	}
	copy(values, rows.rows[rows.index])
	rows.index++
	return nil
}

func openInventoryScriptDatabase(t interface {
	Helper()
	Cleanup(func())
	Fatal(...any)
}, name string, results ...inventoryScriptResult) *sql.DB {
	return openInventoryScriptDatabaseWithExec(t, name, results, nil)
}

func openInventoryScriptDatabaseWithExec(t interface {
	Helper()
	Cleanup(func())
	Fatal(...any)
}, name string, results []inventoryScriptResult, execErrors []error) *sql.DB {
	t.Helper()
	inventoryDriverOnce.Do(func() { sql.Register("runtime-inventory-script", inventoryScriptDriver{}) })
	script := &inventoryScript{results: append([]inventoryScriptResult(nil), results...), execErrors: append([]error(nil), execErrors...)}
	inventoryScriptsMu.Lock()
	inventoryScripts[name] = script
	inventoryScriptsMu.Unlock()
	db, err := sql.Open("runtime-inventory-script", name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		inventoryScriptsMu.Lock()
		delete(inventoryScripts, name)
		inventoryScriptsMu.Unlock()
	})
	return db
}

func inventoryRows(columns []string, rows ...[]driver.Value) inventoryScriptResult {
	return inventoryScriptResult{columns: columns, rows: rows}
}
