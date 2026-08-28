package frontendcapability

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrontendCapabilityStorePersistenceFailures(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	wantErr := errors.New("injected frontend capability database failure")

	newRepository := func(state *frontendCapabilityDBState) (FrontendCapabilityStore, func()) {
		db := sql.OpenDB(frontendCapabilityConnector{state: state})
		repository := NewFrontendCapabilityStore(store)
		repository.db = db
		return repository, func() { _ = db.Close() }
	}

	t.Run("get missing error and scan", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			queryErr  error
			queryRows [][]driver.Value
			wantFound bool
			wantError string
		}{
			{name: "missing"},
			{name: "query error", queryErr: wantErr, wantError: "get frontend capability manifest"},
			{name: "scan error", queryRows: [][]driver.Value{{"workspace-a", "bad revision", "{}", "now"}}, wantError: "get frontend capability manifest"},
		} {
			t.Run(test.name, func(t *testing.T) {
				repository, closeDB := newRepository(&frontendCapabilityDBState{queryErr: test.queryErr, queryRows: test.queryRows})
				defer closeDB()
				_, found, err := repository.Get(t.Context(), "workspace-a")
				if found != test.wantFound || (test.wantError == "" && err != nil) || (test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError))) {
					t.Fatalf("found=%v err=%v", found, err)
				}
			})
		}
	})

	t.Run("put stages", func(t *testing.T) {
		tests := []struct {
			name      string
			execSteps []frontendCapabilityExecStep
			queryErr  error
			queryRows [][]driver.Value
			wantError string
		}{
			{name: "update", execSteps: []frontendCapabilityExecStep{{err: wantErr}}, wantError: "update frontend capability manifest"},
			{name: "insert retry update", execSteps: []frontendCapabilityExecStep{{rows: 0}, {err: wantErr}, {err: wantErr}}, wantError: "persist frontend capability manifest"},
			{name: "final get", execSteps: []frontendCapabilityExecStep{{rows: 1}}, queryErr: wantErr, wantError: "get frontend capability manifest"},
			{name: "final missing", execSteps: []frontendCapabilityExecStep{{rows: 1}}, wantError: sql.ErrNoRows.Error()},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				repository, closeDB := newRepository(&frontendCapabilityDBState{execSteps: test.execSteps, queryErr: test.queryErr, queryRows: test.queryRows})
				defer closeDB()
				_, err := repository.Put(t.Context(), "workspace-a", []byte(`{}`))
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("put error=%v want=%q", err, test.wantError)
				}
			})
		}
		repository, closeDB := newRepository(&frontendCapabilityDBState{
			execSteps: []frontendCapabilityExecStep{{rows: 0}, {err: wantErr}, {rows: 1}},
			queryRows: [][]driver.Value{{"workspace-a", int64(2), `{}`, "2026-07-20T00:00:00Z"}},
		})
		defer closeDB()
		record, err := repository.Put(t.Context(), "workspace-a", []byte(`{}`))
		if err != nil || record.Revision != 2 {
			t.Fatalf("concurrent insert winner record=%#v err=%v", record, err)
		}
	})
}

type frontendCapabilityExecStep struct {
	rows int64
	err  error
}

type frontendCapabilityDBState struct {
	execSteps []frontendCapabilityExecStep
	queryErr  error
	queryRows [][]driver.Value
}

type frontendCapabilityConnector struct{ state *frontendCapabilityDBState }

func (c frontendCapabilityConnector) Connect(context.Context) (driver.Conn, error) {
	return &frontendCapabilityConn{state: c.state}, nil
}
func (frontendCapabilityConnector) Driver() driver.Driver { return frontendCapabilityDriver{} }

type frontendCapabilityDriver struct{}

func (frontendCapabilityDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type frontendCapabilityConn struct{ state *frontendCapabilityDBState }

func (*frontendCapabilityConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*frontendCapabilityConn) Close() error                        { return nil }
func (*frontendCapabilityConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (c *frontendCapabilityConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if len(c.state.execSteps) == 0 {
		return driver.RowsAffected(0), nil
	}
	step := c.state.execSteps[0]
	c.state.execSteps = c.state.execSteps[1:]
	return driver.RowsAffected(step.rows), step.err
}
func (c *frontendCapabilityConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.state.queryErr != nil {
		return nil, c.state.queryErr
	}
	return &frontendCapabilityRows{rows: c.state.queryRows}, nil
}

type frontendCapabilityRows struct {
	rows  [][]driver.Value
	index int
}

func (*frontendCapabilityRows) Columns() []string {
	return []string{"workspace_id", "revision", "manifest_json", "updated_at"}
}
func (*frontendCapabilityRows) Close() error { return nil }
func (r *frontendCapabilityRows) Next(values []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(values, r.rows[r.index])
	r.index++
	return nil
}
