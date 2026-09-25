package deployment

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

func TestIdempotencyReceiptScopeAndStatusInputBoundaries(t *testing.T) {
	for _, test := range []struct{ owner, value, target, want string }{
		{"record", " create ", "", "record.create"},
		{"action", "", "", "action.execute_object"},
		{"action", "", "record", "action.execute_record"},
		{"workflow", "", "", "workflow.execute"},
		{"changeplan", " apply ", "", "change_plan.apply"},
		{"auth", " reset ", "", "reset"},
		{"custom", "", "", "custom"},
	} {
		if got := idempotencyReceiptScope(test.owner, test.value, test.target); got != test.want {
			t.Fatalf("scope %#v=%q", test, got)
		}
	}
	base := openDeploymentFailureStore(t)
	store := NewRuntimeStatusStore(base)
	if _, err := store.IdempotencyOperationalStatus(t.Context(), "", time.Time{}); err == nil {
		t.Fatal("empty status workspace accepted")
	}
	if _, err := store.IdempotencyOperationalStatusForSystem(t.Context(), principalmodel.SystemScope{}, time.Time{}); err == nil {
		t.Fatal("invalid system scope accepted")
	}
	if _, err := store.ListIdempotencyReceipts(t.Context(), "", "", 10); err == nil {
		t.Fatal("empty receipt workspace accepted")
	}
	if changed, err := store.RetryIdempotencyReceipt(t.Context(), "workspace-primary", "unknown", "id"); err != nil || changed {
		t.Fatalf("unknown owner changed=%v err=%v", changed, err)
	}
	if changed, err := store.ResetIdempotencyReceipt(t.Context(), "workspace-primary", "record", " "); err != nil || changed {
		t.Fatalf("empty id changed=%v err=%v", changed, err)
	}
}

func TestRuntimeStatusStagedQueryFailures(t *testing.T) {
	base := openDeploymentFailureStore(t)
	wantErr := errors.New("injected deployment status failure")
	for _, test := range []struct {
		name  string
		steps []deploymentQueryStep
	}{
		{name: "backlog query", steps: []deploymentQueryStep{{err: wantErr}}},
		{name: "backlog scan", steps: []deploymentQueryStep{{columns: []string{"status", "count", "extra"}, rows: [][]driver.Value{{"processing", int64(1), "extra"}}}}},
		{name: "backlog close", steps: []deploymentQueryStep{{columns: []string{"status", "count"}, closeErr: wantErr}}},
		{name: "backlog iteration", steps: []deploymentQueryStep{{columns: []string{"status", "count"}, nextErr: wantErr}}},
		{name: "expired query", steps: []deploymentQueryStep{{columns: []string{"status", "count"}}, {err: wantErr}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedDeploymentStore(base, &deploymentDBState{querySteps: test.steps})
			defer closeDB()
			if _, err := store.IdempotencyOperationalStatus(t.Context(), "workspace-primary", time.Now()); err == nil {
				t.Fatal("status query failure ignored")
			}
		})
	}
	steps := make([]deploymentQueryStep, 0, len(idempotencyReceiptTables)*2+1)
	for range idempotencyReceiptTables {
		steps = append(steps, deploymentQueryStep{columns: []string{"status", "count"}}, deploymentQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}})
	}
	steps = append(steps, deploymentQueryStep{err: wantErr})
	store, closeDB := scriptedDeploymentStore(base, &deploymentDBState{querySteps: steps})
	defer closeDB()
	if _, err := store.IdempotencyOperationalStatusForSystem(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test"), time.Time{}); err == nil || !strings.Contains(err.Error(), "cleanup status") {
		t.Fatalf("cleanup status error=%v", err)
	}
}

func TestReceiptListAndTransitionStagedFailures(t *testing.T) {
	base := openDeploymentFailureStore(t)
	wantErr := errors.New("injected receipt failure")
	columns := []string{"id", "workspace", "scope", "target", "key", "fingerprint", "status", "fencing", "updated", "expires"}
	for _, step := range []deploymentQueryStep{
		{err: wantErr},
		{columns: append(columns, "extra"), rows: [][]driver.Value{{"id", "workspace-primary", "scope", "target", "key", "fingerprint", "status", int64(1), "updated", "expires", "extra"}}},
		{columns: columns, closeErr: wantErr},
		{columns: columns, nextErr: wantErr},
	} {
		store, closeDB := scriptedDeploymentStore(base, &deploymentDBState{querySteps: []deploymentQueryStep{step}})
		if _, err := store.ListIdempotencyReceipts(t.Context(), "workspace-primary", " processing ", 10); err == nil {
			t.Fatal("receipt list failure ignored")
		}
		closeDB()
	}
	for _, step := range []deploymentExecStep{{err: wantErr}, {rowsErr: wantErr}, {rows: 0}, {rows: 1}} {
		store, closeDB := scriptedDeploymentStore(base, &deploymentDBState{execSteps: []deploymentExecStep{step}})
		changed, err := store.RetryIdempotencyReceipt(t.Context(), "workspace-primary", "record", "id")
		closeDB()
		if step.err == nil && step.rowsErr == nil && ((step.rows == 1) != changed || err != nil) {
			t.Fatalf("step=%#v changed=%v err=%v", step, changed, err)
		}
		if (step.err != nil || step.rowsErr != nil) && err == nil {
			t.Fatal("transition error ignored")
		}
	}
}

func TestRuntimeStatusCleanupStatesAndReceiptLimit(t *testing.T) {
	base := openDeploymentFailureStore(t)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		row  []driver.Value
		want string
	}{
		{name: "running", row: []driver.Value{"worker", now.Add(time.Minute).UnixMilli(), int64(3), timevalue.Millis("2026-07-20T11:59:00Z"), int64(0), int64(0), ""}, want: "running"},
		{name: "expired", row: []driver.Value{"worker", now.Add(-time.Minute).UnixMilli(), int64(3), timevalue.Millis("2026-07-20T11:59:00Z"), int64(0), int64(0), "boom"}, want: "failed"},
		{name: "failed", row: []driver.Value{"", int64(0), int64(3), timevalue.Millis("2026-07-20T11:59:00Z"), int64(0), int64(0), "boom"}, want: "failed"},
		{name: "completed", row: []driver.Value{"", int64(0), int64(3), timevalue.Millis("2026-07-20T11:59:00Z"), timevalue.Millis("2026-07-20T12:00:00Z"), int64(2), ""}, want: "completed"},
		{name: "idle", row: []driver.Value{"", int64(0), int64(0), int64(0), int64(0), int64(0), ""}, want: "idle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			steps := deploymentOperationalStatusSteps(test.row)
			store, closeDB := scriptedDeploymentStore(base, &deploymentDBState{querySteps: steps})
			defer closeDB()
			status, err := store.IdempotencyOperationalStatus(t.Context(), "workspace-primary", now)
			if err != nil || status.Cleanup.State != test.want {
				t.Fatalf("state=%q want=%q err=%v", status.Cleanup.State, test.want, err)
			}
		})
	}

	steps := make([]deploymentQueryStep, 0, len(idempotencyReceiptTables))
	for index, spec := range idempotencyReceiptTables {
		steps = append(steps, deploymentQueryStep{columns: deploymentOperationColumns(), rows: [][]driver.Value{deploymentOperationRow(spec, index)}})
	}
	store, closeDB := scriptedDeploymentStore(base, &deploymentDBState{querySteps: steps})
	defer closeDB()
	values, err := store.ListIdempotencyReceipts(t.Context(), "workspace-primary", "", 1)
	if err != nil || len(values) != 1 || values[0].FencingToken != int64(len(idempotencyReceiptTables)-1) {
		t.Fatalf("values=%#v err=%v", values, err)
	}
}

func deploymentOperationColumns() []string {
	return []string{"id", "workspace_id", "system_purpose", "owner", "kind", "action_key", "parent_id", "resource_type", "resource_id", "idempotency_key", "request_fingerprint", "requested_by", "reason", "reference", "status", "status_url", "result_json", "metadata_json", "error_code", "failure_class", "next_action", "related_ids_json", "correlation", "evidence_json", "lease_owner", "lease_expires_at", "fencing_token", "expires_at", "created_at", "started_at", "finished_at", "updated_at"}
}

func deploymentOperationRow(spec idempotencyReceiptTable, index int) []driver.Value {
	updatedAt := timevalue.Millis(fmt.Sprintf("2026-07-20T12:00:0%dZ", index))
	return []driver.Value{
		"id", "workspace-primary", "", spec.rowOwner, "scope", "scope", "", "resource", "target", "key", "fingerprint", "actor", "", "key",
		"processing", "/operations/id", "{}", "{}", "", "", "", "[]", "", "[]", "", int64(0), int64(index), int64(0), updatedAt, updatedAt, int64(0), updatedAt,
	}
}

func deploymentOperationalStatusSteps(cleanupRow []driver.Value) []deploymentQueryStep {
	steps := make([]deploymentQueryStep, 0, len(idempotencyReceiptTables)*2+1)
	for range idempotencyReceiptTables {
		steps = append(steps,
			deploymentQueryStep{columns: []string{"status", "count"}},
			deploymentQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}},
		)
	}
	return append(steps, deploymentQueryStep{
		columns: []string{"lease_owner", "lease_expires_at", "fencing_token", "last_started_at", "last_completed_at", "checkpoint", "last_error"},
		rows:    [][]driver.Value{cleanupRow},
	})
}

func TestCleanupLeaseAndDeleteStages(t *testing.T) {
	base := openDeploymentFailureStore(t)
	wantErr := errors.New("injected cleanup failure")
	validNow := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := NewRuntimeStatusStore(base).RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{}); err == nil {
		t.Fatal("empty cleanup owner accepted")
	}
	for _, test := range []struct {
		name  string
		state *deploymentDBState
	}{
		{name: "ensure check query", state: &deploymentDBState{execSteps: []deploymentExecStep{{err: wantErr}}, querySteps: []deploymentQueryStep{{err: wantErr}}}},
		{name: "ensure absent", state: &deploymentDBState{execSteps: []deploymentExecStep{{err: wantErr}}, querySteps: []deploymentQueryStep{{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}}}},
		{name: "claim", state: &deploymentDBState{execSteps: []deploymentExecStep{{rows: 1}, {err: wantErr}}}},
		{name: "claim rows", state: &deploymentDBState{execSteps: []deploymentExecStep{{rows: 1}, {rowsErr: wantErr}}}},
		{name: "claim unavailable", state: &deploymentDBState{execSteps: []deploymentExecStep{{rows: 1}, {rows: 0}}}},
		{name: "fencing", state: &deploymentDBState{execSteps: []deploymentExecStep{{rows: 1}, {rows: 1}}, querySteps: []deploymentQueryStep{{err: wantErr}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedDeploymentStore(base, test.state)
			defer closeDB()
			result, err := store.RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: "worker", BatchSize: 1})
			if test.name == "claim unavailable" {
				if err != nil || result.Acquired {
					t.Fatalf("result=%#v err=%v", result, err)
				}
			} else if err == nil {
				t.Fatal("cleanup stage failure ignored")
			}
		})
	}

	for _, test := range []struct {
		name  string
		state *deploymentDBState
	}{
		{name: "query", state: &deploymentDBState{querySteps: []deploymentQueryStep{{err: wantErr}}}},
		{name: "scan", state: &deploymentDBState{querySteps: []deploymentQueryStep{{columns: []string{"id", "workspace_id", "extra"}, rows: [][]driver.Value{{"id", "workspace", "extra"}}}}}},
		{name: "close", state: &deploymentDBState{querySteps: []deploymentQueryStep{{columns: []string{"id", "workspace_id"}, closeErr: wantErr}}}},
		{name: "delete", state: &deploymentDBState{querySteps: []deploymentQueryStep{{columns: []string{"id", "workspace_id"}, rows: [][]driver.Value{{"id", "workspace"}}}}, execSteps: []deploymentExecStep{{err: wantErr}}}},
		{name: "delete rows", state: &deploymentDBState{querySteps: []deploymentQueryStep{{columns: []string{"id", "workspace_id"}, rows: [][]driver.Value{{"id", "workspace"}}}}, execSteps: []deploymentExecStep{{rowsErr: wantErr}}}},
	} {
		store, closeDB := scriptedDeploymentStore(base, test.state)
		if _, err := store.deleteExpiredReceiptBatch(t.Context(), idempotencyReceiptTable{table: "receipts"}, "worker", 1, validNow, 1); err == nil {
			t.Fatalf("delete stage=%s succeeded", test.name)
		}
		closeDB()
	}
	store, closeDB := scriptedDeploymentStore(base, &deploymentDBState{querySteps: []deploymentQueryStep{{columns: []string{"id", "workspace_id"}}}})
	deleted, err := store.deleteExpiredReceiptBatch(t.Context(), idempotencyReceiptTable{table: "receipts"}, "worker", 1, validNow, 1)
	closeDB()
	if err != nil || deleted != 0 {
		t.Fatalf("empty delete=%d err=%v", deleted, err)
	}
}

func TestCleanupRunLoopAndCompletionStages(t *testing.T) {
	base := openDeploymentFailureStore(t)
	wantErr := errors.New("injected cleanup run failure")
	token := deploymentQueryStep{columns: []string{"fencing_token"}, rows: [][]driver.Value{{int64(7)}}}
	empty := deploymentQueryStep{columns: []string{"id", "workspace_id"}}

	for _, batchSize := range []int{0, 5001} {
		queries := []deploymentQueryStep{token, empty, empty, empty, empty, empty}
		store, closeDB := scriptedDeploymentStore(base, &deploymentDBState{
			querySteps: queries,
			execSteps:  []deploymentExecStep{{rows: 1}, {rows: 1}, {rows: 1}},
		})
		result, err := store.RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: "worker", BatchSize: batchSize})
		closeDB()
		if err != nil || !result.Acquired || result.Deleted != 0 {
			t.Fatalf("batch=%d result=%#v err=%v", batchSize, result, err)
		}
	}

	store, closeDB := scriptedDeploymentStore(base, &deploymentDBState{
		querySteps: []deploymentQueryStep{
			token,
			{columns: []string{"id", "workspace_id"}, rows: [][]driver.Value{{"receipt", "workspace-primary"}}},
			{columns: []string{"id"}, rows: [][]driver.Value{{idempotencyCleanupLeaseID}}},
		},
		execSteps: []deploymentExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}},
	})
	result, err := store.RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: "worker", BatchSize: 1})
	closeDB()
	if err != nil || result.Deleted != 1 {
		t.Fatalf("bounded result=%#v err=%v", result, err)
	}

	store, closeDB = scriptedDeploymentStore(base, &deploymentDBState{
		querySteps: []deploymentQueryStep{token, {err: wantErr}},
		execSteps:  []deploymentExecStep{{rows: 1}, {rows: 1}, {rows: 1}},
	})
	result, err = store.RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: "worker", BatchSize: 1})
	closeDB()
	if !errors.Is(err, wantErr) || !result.Acquired {
		t.Fatalf("delete failure result=%#v err=%v", result, err)
	}

	queries := []deploymentQueryStep{token, empty, empty, empty, empty, empty}
	for _, test := range []struct {
		name string
		step deploymentExecStep
	}{
		{name: "exec", step: deploymentExecStep{err: wantErr}},
		{name: "rows", step: deploymentExecStep{rowsErr: wantErr}},
		{name: "lost", step: deploymentExecStep{rows: 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedDeploymentStore(base, &deploymentDBState{
				querySteps: append([]deploymentQueryStep(nil), queries...),
				execSteps:  []deploymentExecStep{{rows: 1}, {rows: 1}, test.step},
			})
			defer closeDB()
			if _, err := store.RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: "worker", BatchSize: 2}); err == nil {
				t.Fatal("completion failure ignored")
			}
		})
	}
}

func openDeploymentFailureStore(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func scriptedDeploymentStore(base *database.RuntimeStore, state *deploymentDBState) (RuntimeStatusStore, func()) {
	db := sql.OpenDB(deploymentConnector{state: state})
	store := NewRuntimeStatusStore(base)
	store.db = db
	return store, func() { _ = db.Close() }
}

type deploymentQueryStep struct {
	columns  []string
	rows     [][]driver.Value
	err      error
	nextErr  error
	closeErr error
}
type deploymentExecStep struct {
	rows    int64
	err     error
	rowsErr error
}
type deploymentDBState struct {
	querySteps []deploymentQueryStep
	execSteps  []deploymentExecStep
}
type deploymentConnector struct{ state *deploymentDBState }

func (c deploymentConnector) Connect(context.Context) (driver.Conn, error) {
	return &deploymentConn{state: c.state}, nil
}
func (deploymentConnector) Driver() driver.Driver { return deploymentDriver{} }

type deploymentDriver struct{}

func (deploymentDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type deploymentConn struct{ state *deploymentDBState }

func (*deploymentConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*deploymentConn) Close() error                        { return nil }
func (c *deploymentConn) Begin() (driver.Tx, error)         { return deploymentTx{}, nil }
func (c *deploymentConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return deploymentTx{}, nil
}
func (c *deploymentConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if len(c.state.querySteps) == 0 {
		return &deploymentRows{}, nil
	}
	step := c.state.querySteps[0]
	c.state.querySteps = c.state.querySteps[1:]
	if step.err != nil {
		return nil, step.err
	}
	return &deploymentRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr, closeErr: step.closeErr}, nil
}
func (c *deploymentConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if len(c.state.execSteps) == 0 {
		return deploymentResult{rows: 1}, nil
	}
	step := c.state.execSteps[0]
	c.state.execSteps = c.state.execSteps[1:]
	return deploymentResult{rows: step.rows, err: step.rowsErr}, step.err
}

type deploymentResult struct {
	rows int64
	err  error
}

func (deploymentResult) LastInsertId() (int64, error)   { return 0, nil }
func (r deploymentResult) RowsAffected() (int64, error) { return r.rows, r.err }

type deploymentTx struct{}

func (deploymentTx) Commit() error   { return nil }
func (deploymentTx) Rollback() error { return nil }

type deploymentRows struct {
	columns  []string
	rows     [][]driver.Value
	index    int
	nextErr  error
	closeErr error
}

func (r *deploymentRows) Columns() []string { return r.columns }
func (r *deploymentRows) Close() error      { return r.closeErr }
func (r *deploymentRows) Next(values []driver.Value) error {
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
