package transaction

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestBoundaryIntentStagedDatabaseFailures(t *testing.T) {
	wantErr := errors.New("injected boundary intent database failure")
	current := []driver.Value{"intent-1", "default", "owner", "operation", "resource", "key", "executing", `{}`, `{}`, int64(0), "", "worker", "expiry", int64(1), "", "created", "updated"}
	newStore := func(state *boundaryIntentDBState) (BoundaryIntentStore, func()) {
		store := openBoundaryIntentStore(t)
		db := sql.OpenDB(boundaryIntentConnector{state: state})
		store.db = db
		return store, func() { _ = db.Close() }
	}

	t.Run("claim read", func(t *testing.T) {
		store, closeDB := newStore(&boundaryIntentDBState{execSteps: []boundaryIntentExecStep{{rows: 1}}, querySteps: []boundaryIntentQueryStep{{err: wantErr}}})
		defer closeDB()
		if _, _, err := store.ClaimBoundaryIntent(t.Context(), "intent-1", "worker", time.Now().UTC().Format(time.RFC3339)); !errors.Is(err, wantErr) {
			t.Fatalf("claim read error=%v", err)
		}
	})

	t.Run("transition read", func(t *testing.T) {
		store, closeDB := newStore(&boundaryIntentDBState{querySteps: []boundaryIntentQueryStep{{err: wantErr}}})
		defer closeDB()
		if _, err := store.TransitionBoundaryIntent(t.Context(), "intent-1", "worker", 1, transactionmodel.BoundaryIntentSucceeded, "", ""); !errors.Is(err, wantErr) {
			t.Fatalf("transition read error=%v", err)
		}
	})

	t.Run("transition write", func(t *testing.T) {
		store, closeDB := newStore(&boundaryIntentDBState{querySteps: []boundaryIntentQueryStep{{rows: [][]driver.Value{current}}}, execSteps: []boundaryIntentExecStep{{err: wantErr}}})
		defer closeDB()
		if _, err := store.TransitionBoundaryIntent(t.Context(), "intent-1", "worker", 1, transactionmodel.BoundaryIntentSucceeded, "", ""); err == nil || !strings.Contains(err.Error(), "transition boundary intent") {
			t.Fatalf("transition write error=%v", err)
		}
	})

	t.Run("transition final read", func(t *testing.T) {
		store, closeDB := newStore(&boundaryIntentDBState{querySteps: []boundaryIntentQueryStep{{rows: [][]driver.Value{current}}, {err: wantErr}}, execSteps: []boundaryIntentExecStep{{rows: 1}}})
		defer closeDB()
		if _, err := store.TransitionBoundaryIntent(t.Context(), "intent-1", "worker", 1, transactionmodel.BoundaryIntentSucceeded, "", ""); !errors.Is(err, wantErr) {
			t.Fatalf("transition final read error=%v", err)
		}
	})
}

type boundaryIntentExecStep struct {
	rows int64
	err  error
}

type boundaryIntentQueryStep struct {
	rows [][]driver.Value
	err  error
}

type boundaryIntentDBState struct {
	execSteps  []boundaryIntentExecStep
	querySteps []boundaryIntentQueryStep
}

type boundaryIntentConnector struct{ state *boundaryIntentDBState }

func (c boundaryIntentConnector) Connect(context.Context) (driver.Conn, error) {
	return &boundaryIntentConn{state: c.state}, nil
}
func (boundaryIntentConnector) Driver() driver.Driver { return boundaryIntentDriver{} }

type boundaryIntentDriver struct{}

func (boundaryIntentDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type boundaryIntentConn struct{ state *boundaryIntentDBState }

func (*boundaryIntentConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*boundaryIntentConn) Close() error                        { return nil }
func (*boundaryIntentConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (c *boundaryIntentConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if len(c.state.execSteps) == 0 {
		return driver.RowsAffected(0), nil
	}
	step := c.state.execSteps[0]
	c.state.execSteps = c.state.execSteps[1:]
	return driver.RowsAffected(step.rows), step.err
}
func (c *boundaryIntentConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if len(c.state.querySteps) == 0 {
		return &boundaryIntentRows{}, nil
	}
	step := c.state.querySteps[0]
	c.state.querySteps = c.state.querySteps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return &boundaryIntentRows{rows: step.rows}, nil
}

type boundaryIntentRows struct {
	rows  [][]driver.Value
	index int
}

func (*boundaryIntentRows) Columns() []string {
	return []string{"id", "workspace_id", "owner", "operation", "resource_id", "idempotency_key", "status", "payload_json", "compensation_payload_json", "attempt_count", "next_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "last_error", "created_at", "updated_at"}
}
func (*boundaryIntentRows) Close() error { return nil }
func (r *boundaryIntentRows) Next(values []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(values, r.rows[r.index])
	r.index++
	return nil
}
