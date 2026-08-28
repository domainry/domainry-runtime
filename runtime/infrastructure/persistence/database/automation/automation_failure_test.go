package automation

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

func TestAutomationExecutionWriteFailures(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	base := NewAutomationExecutionStore(store)
	wantErr := errors.New("write")
	for _, seed := range []bool{false, true} {
		copy := base
		db := sql.OpenDB(automationConnector{state: &automationDBState{execSteps: []automationExecStep{{err: wantErr}}}})
		copy.db = db
		value := automationmodel.AutomationRuleExecution{ID: "id", CreatedAt: "created"}
		var err error
		if seed {
			_, err = copy.InsertExecutionSeed(t.Context(), "default", value)
		} else {
			_, err = copy.InsertExecution(t.Context(), "default", value)
		}
		db.Close()
		if err == nil {
			t.Fatalf("seed=%v write failure ignored", seed)
		}
	}
}

func TestAutomationWorkerStagedClaimFailures(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	base := NewAutomationWorkerStore(store)
	wantErr := errors.New("worker")
	existing := automationQueryStep{columns: automationInstructionExecutionColumns(), rows: [][]driver.Value{automationInstructionRow("{}")}}
	succeededRow := automationInstructionRow("{}")
	succeededRow[9] = "succeeded"
	succeeded := automationQueryStep{columns: automationInstructionExecutionColumns(), rows: [][]driver.Value{succeededRow}}
	request := automationmodel.AutomationInstructionExecution{IdempotencyKey: "key"}
	for _, test := range []struct {
		name      string
		state     *automationDBState
		wantError bool
		wantClaim bool
	}{
		{"find", &automationDBState{execSteps: []automationExecStep{{err: wantErr}}, querySteps: []automationQueryStep{{err: wantErr}}}, true, false},
		{"missing", &automationDBState{execSteps: []automationExecStep{{err: wantErr}}, querySteps: []automationQueryStep{{columns: automationInstructionExecutionColumns()}}}, true, false},
		{"succeeded", &automationDBState{execSteps: []automationExecStep{{err: wantErr}}, querySteps: []automationQueryStep{succeeded}}, false, false},
		{"update", &automationDBState{execSteps: []automationExecStep{{err: wantErr}, {err: wantErr}}, querySteps: []automationQueryStep{existing}}, true, false},
		{"update rows", &automationDBState{execSteps: []automationExecStep{{err: wantErr}, {rowsErr: wantErr}}, querySteps: []automationQueryStep{existing}}, true, false},
		{"lost race", &automationDBState{execSteps: []automationExecStep{{err: wantErr}, {rows: 0}}, querySteps: []automationQueryStep{existing}}, false, false},
		{"reload", &automationDBState{execSteps: []automationExecStep{{err: wantErr}, {rows: 1}}, querySteps: []automationQueryStep{existing, {err: wantErr}}}, true, false},
		{"reload missing", &automationDBState{execSteps: []automationExecStep{{err: wantErr}, {rows: 1}}, querySteps: []automationQueryStep{existing, {columns: automationInstructionExecutionColumns()}}}, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, closeDB := scriptedAutomationWorker(base, test.state)
			defer closeDB()
			_, claimed, err := worker.ClaimInstruction(t.Context(), "default", request, "worker", "now", "later")
			if (err != nil) != test.wantError || claimed != test.wantClaim {
				t.Fatalf("claimed=%v err=%v", claimed, err)
			}
		})
	}
}

func TestAutomationWorkerCompletionHeartbeatFailures(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	base := NewAutomationWorkerStore(store)
	wantErr := errors.New("stage")
	valid := automationQueryStep{columns: automationInstructionExecutionColumns(), rows: [][]driver.Value{automationInstructionRow("{}")}}
	completion := func(worker AutomationWorkerStore) error {
		_, err := worker.CompleteInstruction(t.Context(), "default", "key", "worker", 1, "ok", nil, "", "now")
		return err
	}
	heartbeat := func(worker AutomationWorkerStore) error {
		_, err := worker.HeartbeatInstruction(t.Context(), "default", "key", "worker", 1, "later", "now")
		return err
	}
	for _, test := range []struct {
		name  string
		state *automationDBState
		call  func(AutomationWorkerStore) error
	}{
		{"complete exec", &automationDBState{execSteps: []automationExecStep{{err: wantErr}}}, completion},
		{"complete rows", &automationDBState{execSteps: []automationExecStep{{rowsErr: wantErr}}}, completion},
		{"complete lease", &automationDBState{execSteps: []automationExecStep{{rows: 0}}}, completion},
		{"complete find", &automationDBState{execSteps: []automationExecStep{{rows: 1}}, querySteps: []automationQueryStep{{err: wantErr}}}, completion},
		{"complete missing", &automationDBState{execSteps: []automationExecStep{{rows: 1}}, querySteps: []automationQueryStep{{columns: automationInstructionExecutionColumns()}}}, completion},
		{"heartbeat exec", &automationDBState{execSteps: []automationExecStep{{err: wantErr}}}, heartbeat},
		{"heartbeat rows", &automationDBState{execSteps: []automationExecStep{{rowsErr: wantErr}}}, heartbeat},
		{"heartbeat lease", &automationDBState{execSteps: []automationExecStep{{rows: 0}}}, heartbeat},
		{"heartbeat find", &automationDBState{execSteps: []automationExecStep{{rows: 1}}, querySteps: []automationQueryStep{{err: wantErr}}}, heartbeat},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, closeDB := scriptedAutomationWorker(base, test.state)
			defer closeDB()
			if err := test.call(worker); err == nil {
				t.Fatal("failure ignored")
			}
		})
	}
	worker, closeDB := scriptedAutomationWorker(base, &automationDBState{execSteps: []automationExecStep{{rows: 1}}, querySteps: []automationQueryStep{valid}})
	defer closeDB()
	if _, err := worker.HeartbeatInstruction(t.Context(), "default", "key", "worker", 1, "later", "now"); err != nil {
		t.Fatal(err)
	}
}

func scriptedAutomationWorker(base AutomationWorkerStore, state *automationDBState) (AutomationWorkerStore, func()) {
	db := sql.OpenDB(automationConnector{state: state})
	base.db = db
	return base, func() { _ = db.Close() }
}

type automationQueryStep struct {
	columns []string
	rows    [][]driver.Value
	err     error
	nextErr error
}
type automationExecStep struct {
	rows    int64
	err     error
	rowsErr error
}
type automationDBState struct {
	querySteps []automationQueryStep
	execSteps  []automationExecStep
}
type automationConnector struct{ state *automationDBState }

func (c automationConnector) Connect(context.Context) (driver.Conn, error) {
	return &automationConn{state: c.state}, nil
}
func (automationConnector) Driver() driver.Driver { return automationDriver{} }

type automationDriver struct{}

func (automationDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type automationConn struct{ state *automationDBState }

func (*automationConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*automationConn) Close() error                        { return nil }
func (*automationConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (c *automationConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if len(c.state.querySteps) == 0 {
		return &automationRows{}, nil
	}
	step := c.state.querySteps[0]
	c.state.querySteps = c.state.querySteps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return &automationRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr}, nil
}
func (c *automationConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if len(c.state.execSteps) == 0 {
		return automationResult{rows: 1}, nil
	}
	step := c.state.execSteps[0]
	c.state.execSteps = c.state.execSteps[1:]
	return automationResult{rows: step.rows, err: step.rowsErr}, step.err
}

type automationResult struct {
	rows int64
	err  error
}

func (automationResult) LastInsertId() (int64, error)   { return 0, nil }
func (r automationResult) RowsAffected() (int64, error) { return r.rows, r.err }

type automationRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	nextErr error
}

func (r *automationRows) Columns() []string { return r.columns }
func (*automationRows) Close() error        { return nil }
func (r *automationRows) Next(values []driver.Value) error {
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
