package changeplan

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

func TestBusinessEvidenceStoreStagedFailures(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	wantErr := errors.New("injected change plan persistence failure")
	value := businessseedmodel.BusinessSeedProvenance{SeedKey: "seed"}
	for _, test := range []struct {
		name  string
		state *changePlanDBState
	}{
		{name: "begin", state: &changePlanDBState{beginErrors: []error{wantErr}}},
		{name: "delete", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}}}},
		{name: "insert", state: &changePlanDBState{execSteps: []changePlanExecStep{{rows: 1}, {err: wantErr}}}},
		{name: "commit", state: &changePlanDBState{execSteps: []changePlanExecStep{{rows: 1}, {rows: 1}}, commitErrors: []error{wantErr}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedEvidenceStore(base, test.state)
			defer closeDB()
			if err := store.UpsertSeedProvenance(t.Context(), value); err == nil {
				t.Fatal("seed provenance failure ignored")
			}
		})
	}
	columns := []string{"seed_key", "object_key", "record_id", "source_kind", "source_id", "template_id", "template_version", "content_hash", "materialized_at"}
	for _, step := range []changePlanQueryStep{
		{err: wantErr},
		{columns: append(columns, "extra"), rows: [][]driver.Value{{"seed", "object", "record", "kind", "source", "template", "v1", "hash", "at", "extra"}}},
		{columns: columns, nextErr: wantErr},
	} {
		store, closeDB := scriptedEvidenceStore(base, &changePlanDBState{querySteps: []changePlanQueryStep{step}})
		if _, err := store.ListSeedProvenance(t.Context()); err == nil {
			t.Fatal("list seed provenance failure ignored")
		}
		closeDB()
	}
}

func TestBusinessChangePlanDraftStagedFailures(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	wantErr := errors.New("injected change plan draft failure")
	draft := changeplanmodel.BusinessChangePlanDraft{PlanID: "plan", Payload: []byte(`{}`)}
	draftColumns := []string{"workspace_id", "plan_id", "revision", "status", "payload_json", "created_by", "updated_by", "created_at", "updated_at"}
	draftRow := []driver.Value{"default", "plan", int64(1), "draft", `{}`, "creator", "updater", "created", "updated"}
	for _, test := range []struct {
		name  string
		state *changePlanDBState
		call  func(BusinessChangePlanStore) error
	}{
		{name: "get invalid workspace", state: &changePlanDBState{}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.GetDraft(t.Context(), "", "plan")
			return err
		}},
		{name: "save invalid workspace", state: &changePlanDBState{}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.SaveDraft(t.Context(), "", draft, 0)
			return err
		}},
		{name: "save workspace mismatch", state: &changePlanDBState{}, call: func(store BusinessChangePlanStore) error {
			mismatched := draft
			mismatched.WorkspaceID = "other"
			_, _, err := store.SaveDraft(t.Context(), "default", mismatched, 0)
			return err
		}},
		{name: "save matching workspace", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}}}, call: func(store BusinessChangePlanStore) error {
			matched := draft
			matched.WorkspaceID = "default"
			_, _, err := store.SaveDraft(t.Context(), "default", matched, 0)
			return err
		}},
		{name: "transition invalid workspace", state: &changePlanDBState{}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.TransitionDraft(t.Context(), "", "plan", 1, "draft", "review", "by", "at")
			return err
		}},
		{name: "transition", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}}}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.TransitionDraft(t.Context(), "default", "plan", 1, "draft", "review", "by", "at")
			return err
		}},
		{name: "transition rows", state: &changePlanDBState{execSteps: []changePlanExecStep{{rowsErr: wantErr}}}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.TransitionDraft(t.Context(), "default", "plan", 1, "draft", "review", "by", "at")
			return err
		}},
		{name: "publish invalid workspace", state: &changePlanDBState{}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.PublishDraft(t.Context(), "", "plan", 1, "by", "at")
			return err
		}},
		{name: "get missing", state: &changePlanDBState{querySteps: []changePlanQueryStep{{columns: draftColumns}}}, call: func(store BusinessChangePlanStore) error {
			_, found, err := store.GetDraft(t.Context(), "default", "plan")
			if err == nil && !found {
				return wantErr
			}
			return err
		}},
		{name: "get query", state: &changePlanDBState{querySteps: []changePlanQueryStep{{err: wantErr}}}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.GetDraft(t.Context(), "default", "plan")
			return err
		}},
		{name: "get scan", state: &changePlanDBState{querySteps: []changePlanQueryStep{{columns: append(draftColumns, "extra"), rows: [][]driver.Value{append(draftRow, "extra")}}}}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.GetDraft(t.Context(), "default", "plan")
			return err
		}},
		{name: "create", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}}}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.SaveDraft(t.Context(), "default", draft, 0)
			return err
		}},
		{name: "create final read", state: &changePlanDBState{execSteps: []changePlanExecStep{{rows: 1}}, querySteps: []changePlanQueryStep{{err: wantErr}}}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.SaveDraft(t.Context(), "default", draft, 0)
			return err
		}},
		{name: "update", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}}}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.SaveDraft(t.Context(), "default", draft, 1)
			return err
		}},
		{name: "update rows", state: &changePlanDBState{execSteps: []changePlanExecStep{{rowsErr: wantErr}}}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.SaveDraft(t.Context(), "default", draft, 1)
			return err
		}},
		{name: "publish", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}}}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.PublishDraft(t.Context(), "default", "plan", 1, "by", "at")
			return err
		}},
		{name: "publish rows", state: &changePlanDBState{execSteps: []changePlanExecStep{{rowsErr: wantErr}}}, call: func(store BusinessChangePlanStore) error {
			_, _, err := store.PublishDraft(t.Context(), "default", "plan", 1, "by", "at")
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedChangePlanStore(base, test.state)
			defer closeDB()
			if err := test.call(store); err == nil {
				t.Fatal("draft failure stage succeeded")
			}
		})
	}
	for _, publish := range []bool{false, true} {
		store, closeDB := scriptedChangePlanStore(base, &changePlanDBState{execSteps: []changePlanExecStep{{rows: 0}}})
		var found bool
		var err error
		if publish {
			_, found, err = store.PublishDraft(t.Context(), "default", "plan", 1, "by", "at")
		} else {
			_, found, err = store.SaveDraft(t.Context(), "default", draft, 1)
		}
		closeDB()
		if err != nil || found {
			t.Fatalf("publish=%v found=%v err=%v", publish, found, err)
		}
	}
	store, closeDB := scriptedChangePlanStore(base, &changePlanDBState{execSteps: []changePlanExecStep{{rows: 0}}})
	if _, found, err := store.TransitionDraft(t.Context(), "default", "plan", 1, "draft", "review", "by", "at"); err != nil || found {
		t.Fatalf("stale transition found=%v err=%v", found, err)
	}
	closeDB()
	store, closeDB = scriptedChangePlanStore(base, &changePlanDBState{execSteps: []changePlanExecStep{{rows: 1}}, querySteps: []changePlanQueryStep{{columns: draftColumns, rows: [][]driver.Value{draftRow}}}})
	updated, found, err := store.SaveDraft(t.Context(), "default", draft, 1)
	closeDB()
	if err != nil || !found || updated.PlanID != "plan" {
		t.Fatalf("updated=%#v found=%v err=%v", updated, found, err)
	}
}

func TestBusinessChangePlanOperationStagedFailures(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	wantErr := errors.New("injected change plan operation failure")
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	request := changeplanmodel.ChangePlanOperationClaimRequest{Execution: changeplanmodel.ChangePlanOperationExecution{WorkspaceID: "default", PlanID: "plan", PlanRevision: 1, Operation: "apply", IdempotencyKey: "key"}, RequestFingerprint: "fingerprint", LeaseOwner: "owner"}
	operationColumns := changePlanOperationColumns()
	operationRow := changePlanOperationRow(now, string(idempotency.StatusFailedRetryable))

	store, closeDB := scriptedChangePlanStore(base, &changePlanDBState{execSteps: []changePlanExecStep{{rows: 1}}})
	claim, err := store.TryBeginOperation(t.Context(), "default", request)
	closeDB()
	if err != nil || claim.Decision != idempotency.DecisionAcquired || claim.Execution.LeaseExpiresAt == "" {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}

	for _, test := range []struct {
		name  string
		state *changePlanDBState
	}{
		{name: "find error", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}}, querySteps: []changePlanQueryStep{{err: wantErr}}}},
		{name: "missing after insert", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}}, querySteps: []changePlanQueryStep{{columns: operationColumns}}}},
		{name: "reclaim update", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}, {err: wantErr}}, querySteps: []changePlanQueryStep{{columns: operationColumns, rows: [][]driver.Value{operationRow}}}}},
		{name: "reclaim rows", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}, {rowsErr: wantErr}}, querySteps: []changePlanQueryStep{{columns: operationColumns, rows: [][]driver.Value{operationRow}}}}},
		{name: "reclaim final read", state: &changePlanDBState{execSteps: []changePlanExecStep{{err: wantErr}, {rows: 1}}, querySteps: []changePlanQueryStep{{columns: operationColumns, rows: [][]driver.Value{operationRow}}, {err: wantErr}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate, closeDB := scriptedChangePlanStore(base, test.state)
			defer closeDB()
			if _, err := candidate.TryBeginOperation(t.Context(), "default", request); err == nil {
				t.Fatal("operation failure stage succeeded")
			}
		})
	}
	for _, test := range []struct {
		name     string
		rows     int64
		finalRow []driver.Value
		decision idempotency.Decision
	}{
		{name: "final missing", rows: 1},
		{name: "lost reclaim race", rows: 0, finalRow: operationRow, decision: idempotency.DecisionInProgress},
	} {
		candidate, closeDB := scriptedChangePlanStore(base, &changePlanDBState{
			execSteps: []changePlanExecStep{{err: wantErr}, {rows: test.rows}},
			querySteps: []changePlanQueryStep{
				{columns: operationColumns, rows: [][]driver.Value{operationRow}},
				{columns: operationColumns, rows: func() [][]driver.Value {
					if test.finalRow == nil {
						return nil
					}
					return [][]driver.Value{test.finalRow}
				}()},
			},
		})
		result, err := candidate.TryBeginOperation(t.Context(), "default", request)
		closeDB()
		if err != nil || result.Decision != test.decision {
			t.Fatalf("%s result=%#v err=%v", test.name, result, err)
		}
	}

	completion := changeplanmodel.ChangePlanOperationCompletion{ExecutionID: "execution", LeaseOwner: "owner", FencingToken: 1, Result: map[string]any{"ok": true}}
	failure := changeplanmodel.ChangePlanOperationFailure{ExecutionID: "execution", LeaseOwner: "owner", FencingToken: 1, ErrorCode: "failed", Result: map[string]any{"ok": false}}
	for _, terminal := range []bool{false, true} {
		for _, stage := range []string{"encode", "exec", "rows", "find"} {
			state := &changePlanDBState{}
			currentCompletion, currentFailure := completion, failure
			switch stage {
			case "encode":
				if terminal {
					currentFailure.Result = map[string]any{"bad": make(chan int)}
				} else {
					currentCompletion.Result = map[string]any{"bad": make(chan int)}
				}
			case "exec":
				state.execSteps = []changePlanExecStep{{err: wantErr}}
			case "rows":
				state.execSteps = []changePlanExecStep{{rowsErr: wantErr}}
			case "find":
				state.execSteps = []changePlanExecStep{{rows: 1}}
				state.querySteps = []changePlanQueryStep{{err: wantErr}}
			}
			candidate, closeDB := scriptedChangePlanStore(base, state)
			var err error
			if terminal {
				_, err = candidate.FailOperation(t.Context(), "default", currentFailure)
			} else {
				_, err = candidate.CompleteOperation(t.Context(), "default", currentCompletion)
			}
			closeDB()
			if err == nil {
				t.Fatalf("terminal=%v stage=%s succeeded", terminal, stage)
			}
		}
	}
	for _, retryable := range []bool{false, true} {
		state := &changePlanDBState{execSteps: []changePlanExecStep{{rows: 1}}, querySteps: []changePlanQueryStep{{columns: operationColumns, rows: [][]driver.Value{operationRow}}}}
		candidate, closeDB := scriptedChangePlanStore(base, state)
		current := failure
		current.Retryable = retryable
		if _, err := candidate.FailOperation(t.Context(), "default", current); err != nil {
			t.Fatalf("retryable=%v err=%v", retryable, err)
		}
		closeDB()
	}
}

func scriptedChangePlanStore(base *database.RuntimeStore, state *changePlanDBState) (BusinessChangePlanStore, func()) {
	db := sql.OpenDB(changePlanConnector{state: state})
	store := NewBusinessChangePlanStore(base)
	store.db = db
	return store, func() { _ = db.Close() }
}
func scriptedEvidenceStore(base *database.RuntimeStore, state *changePlanDBState) (BusinessEvidenceStore, func()) {
	db := sql.OpenDB(changePlanConnector{state: state})
	store := NewBusinessEvidenceStore(base)
	store.db = db
	return store, func() { _ = db.Close() }
}

func changePlanOperationRow(now time.Time, status string) []driver.Value {
	return []driver.Value{"execution", "default", "plan", int64(1), "apply", "key", "fingerprint", status, `{}`, "owner", now.Add(time.Minute).Format(time.RFC3339Nano), int64(1), "", now.Add(time.Hour).Format(time.RFC3339Nano), "actor", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)}
}

type changePlanExecStep struct {
	rows    int64
	err     error
	rowsErr error
}
type changePlanQueryStep struct {
	columns []string
	rows    [][]driver.Value
	err     error
	nextErr error
}
type changePlanDBState struct {
	execSteps    []changePlanExecStep
	querySteps   []changePlanQueryStep
	beginErrors  []error
	commitErrors []error
}
type changePlanConnector struct{ state *changePlanDBState }

func (c changePlanConnector) Connect(context.Context) (driver.Conn, error) {
	return &changePlanConn{state: c.state}, nil
}
func (changePlanConnector) Driver() driver.Driver { return changePlanDriver{} }

type changePlanDriver struct{}

func (changePlanDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type changePlanConn struct{ state *changePlanDBState }

func (*changePlanConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*changePlanConn) Close() error                        { return nil }
func (c *changePlanConn) Begin() (driver.Tx, error)         { return c.begin() }
func (c *changePlanConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.begin()
}
func (c *changePlanConn) begin() (driver.Tx, error) {
	if len(c.state.beginErrors) > 0 {
		err := c.state.beginErrors[0]
		c.state.beginErrors = c.state.beginErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return &changePlanTx{state: c.state}, nil
}
func (c *changePlanConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if len(c.state.execSteps) == 0 {
		return changePlanResult{rows: 1}, nil
	}
	step := c.state.execSteps[0]
	c.state.execSteps = c.state.execSteps[1:]
	return changePlanResult{rows: step.rows, err: step.rowsErr}, step.err
}
func (c *changePlanConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if len(c.state.querySteps) == 0 {
		return &changePlanRows{}, nil
	}
	step := c.state.querySteps[0]
	c.state.querySteps = c.state.querySteps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return &changePlanRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr}, nil
}

type changePlanTx struct{ state *changePlanDBState }

func (tx *changePlanTx) Commit() error {
	if len(tx.state.commitErrors) == 0 {
		return nil
	}
	err := tx.state.commitErrors[0]
	tx.state.commitErrors = tx.state.commitErrors[1:]
	return err
}
func (*changePlanTx) Rollback() error { return nil }

type changePlanResult struct {
	rows int64
	err  error
}

func (changePlanResult) LastInsertId() (int64, error)   { return 0, nil }
func (r changePlanResult) RowsAffected() (int64, error) { return r.rows, r.err }

type changePlanRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	nextErr error
}

func (r *changePlanRows) Columns() []string { return r.columns }
func (*changePlanRows) Close() error        { return nil }
func (r *changePlanRows) Next(values []driver.Value) error {
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
