package action

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	ormpostgres "github.com/domainry/domainry-orm/postgres"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

func TestActionExecutionClaimRetryWaitAndDatabaseStages(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	wantErr := errors.New("injected action execution failure")
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	request := actionmodel.ActionExecutionClaimRequest{Execution: actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-primary", ObjectKey: " object ", RecordID: " record ", ActionKey: " action ", IdempotencyKey: " idem "}, RequestFingerprint: " fingerprint ", LeaseOwner: " owner ", Now: now}

	candidate, closeDB := scriptedActionStore(base, &actionDBState{execSteps: []actionExecStep{{rows: 1}}})
	claim, err := candidate.TryBeginExecution(t.Context(), request)
	closeDB()
	if err != nil || claim.Decision != idempotency.DecisionAcquired || claim.Execution.WorkspaceID != "workspace-primary" || claim.Execution.LeaseExpiresAt != now.Add(30*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	candidate, closeDB = scriptedActionStore(base, &actionDBState{execSteps: []actionExecStep{{rows: 1}}})
	zeroTimeClaim, err := candidate.tryBeginExecutionOnce(t.Context(), actionmodel.ActionExecutionClaimRequest{Execution: request.Execution})
	closeDB()
	if err != nil || zeroTimeClaim.Execution.CreatedAt == "" {
		t.Fatalf("zero-time claim=%#v err=%v", zeroTimeClaim, err)
	}

	current := actionExecutionRow(now, string(idempotency.StatusFailedRetryable), "fingerprint")
	for _, test := range []struct {
		name      string
		state     *actionDBState
		wantError bool
		decision  idempotency.Decision
	}{
		{name: "insert then missing", state: &actionDBState{execSteps: []actionExecStep{{err: wantErr}}, querySteps: []actionQueryStep{{columns: actionExecutionColumns()}}}, wantError: true},
		{name: "wait query", state: &actionDBState{execSteps: []actionExecStep{{err: wantErr}}, querySteps: []actionQueryStep{{err: wantErr}}}, wantError: true},
		{name: "reclaim update", state: &actionDBState{execSteps: []actionExecStep{{err: wantErr}, {rows: 1}}, querySteps: []actionQueryStep{{columns: actionExecutionColumns(), rows: [][]driver.Value{current}}, {columns: actionExecutionColumns(), rows: [][]driver.Value{current}}}}, decision: idempotency.DecisionAcquired},
		{name: "reclaim update error", state: &actionDBState{execSteps: []actionExecStep{{err: wantErr}, {err: wantErr}}, querySteps: []actionQueryStep{{columns: actionExecutionColumns(), rows: [][]driver.Value{current}}}}, wantError: true},
		{name: "reclaim final query", state: &actionDBState{execSteps: []actionExecStep{{err: wantErr}, {rows: 1}}, querySteps: []actionQueryStep{{columns: actionExecutionColumns(), rows: [][]driver.Value{current}}, {err: wantErr}}}, wantError: true},
		{name: "reclaim final missing", state: &actionDBState{execSteps: []actionExecStep{{err: wantErr}, {rows: 1}}, querySteps: []actionQueryStep{{columns: actionExecutionColumns(), rows: [][]driver.Value{current}}, {columns: actionExecutionColumns()}}}, wantError: false},
		{name: "lost reclaim race", state: &actionDBState{execSteps: []actionExecStep{{err: wantErr}, {rows: 0}}, querySteps: []actionQueryStep{{columns: actionExecutionColumns(), rows: [][]driver.Value{current}}, {columns: actionExecutionColumns(), rows: [][]driver.Value{current}}}}, decision: idempotency.DecisionInProgress},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedActionStore(base, test.state)
			defer closeDB()
			store.waitAttempts = 1
			store.waitDelay = 0
			result, err := store.tryBeginExecutionOnce(t.Context(), request)
			if test.wantError && err == nil || !test.wantError && test.name != "reclaim final missing" && err != nil {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if test.name == "reclaim final missing" && err != nil {
				t.Fatalf("missing final read should preserve nil error: %v", err)
			}
			if test.decision != "" && result.Decision != test.decision {
				t.Fatalf("decision=%s want=%s", result.Decision, test.decision)
			}
		})
	}

	for _, message := range []string{"SQLITE_BUSY", "database is locked", "database table is locked"} {
		store := NewActionBusinessExecutionStore(base)
		if !store.store.IsTransientError(errors.New(message)) {
			t.Fatalf("busy marker %q not recognized", message)
		}
	}
	store := NewActionBusinessExecutionStore(base)
	if store.claimDelay(0) != time.Millisecond {
		t.Fatalf("default claim delay=%v", store.claimDelay(0))
	}
	if store.store.IsTransientError(nil) || store.store.IsTransientError(errors.New("other")) {
		t.Fatal("non-busy error classified as busy")
	}
	if err := store.store.SetEngineForTesting("postgres"); err != nil {
		t.Fatal(err)
	}
	if store.store.IsTransientError(errors.New("SQLITE_BUSY")) {
		t.Fatal("PostgreSQL error classified as SQLite busy")
	}
	if err := store.store.SetEngineForTesting("sqlite"); err != nil {
		t.Fatal(err)
	}
	nonBusyState := &actionDBState{execSteps: []actionExecStep{{err: wantErr}}, querySteps: []actionQueryStep{{err: wantErr}}}
	store, closeDB = scriptedActionStore(base, nonBusyState)
	store.waitAttempts, store.claimAttempts, store.waitDelay = 1, 1, 0
	if _, err := store.TryBeginExecution(t.Context(), request); !errors.Is(err, wantErr) {
		t.Fatalf("non-busy claim error=%v", err)
	}
	closeDB()

	busyState := &actionDBState{execSteps: []actionExecStep{{err: errors.New("SQLITE_BUSY")}}, querySteps: []actionQueryStep{{err: errors.New("SQLITE_BUSY")}}}
	store, closeDB = scriptedActionStore(base, busyState)
	store.waitAttempts, store.claimAttempts, store.waitDelay = 1, 1, 0
	store.claimDelay = func(int) time.Duration { return 0 }
	if _, err := store.TryBeginExecution(t.Context(), request); err == nil || !strings.Contains(err.Error(), "remained busy") {
		t.Fatalf("busy exhaustion=%v", err)
	}
	closeDB()

	ctx, cancel := context.WithCancel(t.Context())
	busyState = &actionDBState{execSteps: []actionExecStep{{err: errors.New("SQLITE_BUSY")}}, querySteps: []actionQueryStep{{err: errors.New("SQLITE_BUSY")}}}
	store, closeDB = scriptedActionStore(base, busyState)
	defer closeDB()
	store.waitAttempts, store.claimAttempts, store.waitDelay = 1, 1, 0
	store.claimDelay = func(int) time.Duration { cancel(); return time.Hour }
	if _, err := store.TryBeginExecution(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("busy cancellation=%v", err)
	}
}

func TestActionExecutionAtomicCommitStages(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	wantErr := errors.New("injected atomic action failure")
	now := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	completion := actionmodel.ActionExecutionCompletion{Execution: actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-primary"}, ExecutionID: "execution", LeaseOwner: "owner", FencingToken: 1, ExpiresAt: now.Add(time.Hour)}
	row := actionExecutionRow(now, string(idempotency.StatusSucceeded), "fingerprint")

	tests := []struct {
		name       string
		state      *actionDBState
		completion actionmodel.ActionExecutionCompletion
		wantOK     bool
	}{
		{name: "begin", state: &actionDBState{beginErrors: []error{wantErr}}, completion: completion},
		{name: "audit", state: &actionDBState{}, completion: func() actionmodel.ActionExecutionCompletion {
			value := completion
			value.AuditEvents = []auditmodel.AuditEvent{{}}
			return value
		}()},
		{name: "marshal", state: &actionDBState{}, completion: func() actionmodel.ActionExecutionCompletion {
			value := completion
			value.Result = map[string]any{"bad": make(chan int)}
			return value
		}()},
		{name: "update", state: &actionDBState{execSteps: []actionExecStep{{err: wantErr}}}, completion: completion},
		{name: "lease lost", state: &actionDBState{execSteps: []actionExecStep{{rows: 0}}, querySteps: []actionQueryStep{{columns: actionExecutionColumns(), rows: [][]driver.Value{row}}}}, completion: completion},
		{name: "commit", state: &actionDBState{execSteps: []actionExecStep{{rows: 1}}, commitErrors: []error{wantErr}}, completion: completion},
		{name: "deadlock commit", state: &actionDBState{execSteps: []actionExecStep{{rows: 1}}, commitErrors: []error{errors.New("deadlock detected")}}, completion: completion},
		{name: "final lookup", state: &actionDBState{execSteps: []actionExecStep{{rows: 1}}, querySteps: []actionQueryStep{{err: wantErr}}}, completion: completion},
		{name: "success zero time", state: &actionDBState{execSteps: []actionExecStep{{rows: 1}}, querySteps: []actionQueryStep{{columns: actionExecutionColumns(), rows: [][]driver.Value{row}}}}, completion: completion, wantOK: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedActionStore(base, test.state)
			defer closeDB()
			// The script driver exercises database/sql transaction failure stages.
			// SQLite's production-only BEGIN IMMEDIATE branch has a real-driver test.
			store.profile = ormpostgres.NewProfile()
			value, err := commitBusinessActionExecution(t.Context(), store, nil, test.completion)
			if test.wantOK && (err != nil || value.Status != string(idempotency.StatusSucceeded)) {
				t.Fatalf("value=%#v err=%v", value, err)
			}
			if !test.wantOK && err == nil {
				t.Fatal("atomic failure stage succeeded")
			}
			if test.name == "commit" && !mutation.IsTransactionCommitUnknown(err) {
				t.Fatalf("commit transport failure was not unknown: %v", err)
			}
			if test.name == "deadlock commit" && !mutation.IsTransactionTransient(err, mutation.TransactionTransientDeadlock) {
				t.Fatalf("deadlock commit was not classified: %v", err)
			}
		})
	}
}

func TestActionExecutionCompletionLeaseAndLookupStages(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	wantErr := errors.New("injected action completion failure")
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	completion := actionmodel.ActionExecutionCompletion{Execution: actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-primary"}, ExecutionID: "execution", LeaseOwner: " owner ", FencingToken: 1, ResponseStatus: 500, ErrorCode: " failed ", ExpiresAt: now.Add(time.Hour)}
	if _, err := NewActionBusinessExecutionStore(base).CompleteExecution(t.Context(), actionmodel.ActionExecutionCompletion{Result: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("unencodable completion result accepted")
	}

	row := actionExecutionRow(now, string(idempotency.StatusFailedTerminal), "fingerprint")
	for _, test := range []struct {
		name  string
		state *actionDBState
		ok    bool
	}{
		{name: "update", state: &actionDBState{execSteps: []actionExecStep{{err: wantErr}}}},
		{name: "lease lost lookup error", state: &actionDBState{execSteps: []actionExecStep{{rows: 0}}, querySteps: []actionQueryStep{{err: wantErr}}}},
		{name: "lease lost lookup success", state: &actionDBState{execSteps: []actionExecStep{{rows: 0}}, querySteps: []actionQueryStep{{columns: actionExecutionColumns(), rows: [][]driver.Value{row}}}}},
		{name: "final lookup", state: &actionDBState{execSteps: []actionExecStep{{rows: 1}}, querySteps: []actionQueryStep{{err: wantErr}}}},
		{name: "success", state: &actionDBState{execSteps: []actionExecStep{{rows: 1}}, querySteps: []actionQueryStep{{columns: actionExecutionColumns(), rows: [][]driver.Value{row}}}}, ok: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedActionStore(base, test.state)
			defer closeDB()
			value, err := store.CompleteExecution(t.Context(), completion)
			if test.ok && (err != nil || value.Status != string(idempotency.StatusFailedTerminal)) {
				t.Fatalf("value=%#v err=%v", value, err)
			}
			if !test.ok && err == nil {
				t.Fatal("completion failure stage succeeded")
			}
		})
	}

	store := NewActionBusinessExecutionStore(base)
	if err := store.actionLeaseMutationResult(t.Context(), false, wantErr, "execution"); !errors.Is(err, wantErr) {
		t.Fatalf("execution error=%v", err)
	}
	if integrationWorkspaceID(" ") != "" || integrationWorkspaceID(" workspace ") != "workspace" || nonNilMap(nil) == nil || nonNilMap(map[string]any{"ok": true})["ok"] != true {
		t.Fatal("normalization helpers failed")
	}
	projection := actionmodel.ActionBusinessExecution{ID: "execution", WorkspaceID: "workspace-primary", ObjectKey: "object", RecordID: "record", ActionKey: "action", IdempotencyKey: "idem", RequestFingerprint: "fingerprint", Status: string(idempotency.StatusSucceeded), CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}
	record := actionExecutionRecord(projection, json.RawMessage(`{`))
	value, err := actionBusinessExecution(record)
	if err != nil || value.Result == nil {
		t.Fatalf("invalid result projection=%#v err=%v", value, err)
	}
}

func TestActionExecutionWaitOutcomes(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	wantErr := errors.New("injected wait failure")
	now := time.Now().UTC()
	row := actionExecutionRow(now, string(idempotency.StatusProcessing), "fingerprint")
	for _, test := range []struct {
		name  string
		state *actionDBState
		found bool
		err   error
	}{
		{name: "found", state: &actionDBState{querySteps: []actionQueryStep{{columns: actionExecutionColumns(), rows: [][]driver.Value{row}}}}, found: true},
		{name: "missing", state: &actionDBState{querySteps: []actionQueryStep{{columns: actionExecutionColumns()}}}},
		{name: "error", state: &actionDBState{querySteps: []actionQueryStep{{err: wantErr}}}, err: wantErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, closeDB := scriptedActionStore(base, test.state)
			defer closeDB()
			store.waitAttempts, store.waitDelay = 1, 0
			_, found, err := store.waitForExecutionByScope(t.Context(), "workspace-primary", "object", "record", "action", "idem")
			if found != test.found || !errors.Is(err, test.err) {
				t.Fatalf("found=%v err=%v", found, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	store, closeDB := scriptedActionStore(base, &actionDBState{querySteps: []actionQueryStep{{columns: actionExecutionColumns(), hook: cancel}}})
	defer closeDB()
	store.waitAttempts, store.waitDelay = 1, time.Hour
	if _, _, err := store.waitForExecutionByScope(ctx, "workspace-primary", "object", "record", "action", "idem"); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait cancellation=%v", err)
	}
}

func TestActionExecutionTransactionConstructionAndInactiveEdges(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	wantErr := errors.New("injected transaction edge")

	if _, err := (ActionBusinessExecutionStore{}).BeginExecutionTransaction(t.Context()); err == nil || !strings.Contains(err.Error(), "database is required") {
		t.Fatalf("nil database error=%v", err)
	}

	store, closeDB := scriptedActionStore(base, &actionDBState{connectErr: wantErr})
	if _, err := store.BeginExecutionTransaction(t.Context()); !errors.Is(err, wantErr) {
		t.Fatalf("sqlite connection error=%v", err)
	}
	closeDB()

	store, closeDB = scriptedActionStore(base, &actionDBState{execSteps: []actionExecStep{{err: wantErr}}})
	if _, err := store.BeginExecutionTransaction(t.Context()); !errors.Is(err, wantErr) {
		t.Fatalf("sqlite begin error=%v", err)
	}
	closeDB()

	ctx := t.Context()
	var nilTransaction *actionExecutionTransaction
	if got := nilTransaction.Context(ctx); got != ctx {
		t.Fatal("nil transaction context changed")
	}
	if got := (&actionExecutionTransaction{}).Context(ctx); got != ctx {
		t.Fatal("executor-less transaction context changed")
	}
	if err := nilTransaction.RollBack(ctx); err != nil {
		t.Fatalf("nil rollback error=%v", err)
	}
	if err := (&actionExecutionTransaction{}).RollBack(ctx); err != nil {
		t.Fatalf("executor-less rollback error=%v", err)
	}
	if err := (&actionExecutionTransaction{executor: base.DB(), done: true}).RollBack(ctx); err != nil {
		t.Fatalf("completed rollback error=%v", err)
	}
	for name, transaction := range map[string]*actionExecutionTransaction{
		"nil":           nil,
		"executor-less": {},
		"completed":     {executor: base.DB(), done: true},
	} {
		t.Run("commit "+name, func(t *testing.T) {
			if _, err := transaction.Commit(t.Context(), nil, actionmodel.ActionExecutionCompletion{}); err == nil {
				t.Fatal("inactive transaction commit succeeded")
			}
		})
	}
}

func TestActionExecutionTransactionRollbackCommitAndResultEdges(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	defer base.Close()
	wantErr := errors.New("injected transaction edge")
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	completion := actionmodel.ActionExecutionCompletion{
		Execution:   actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-primary"},
		ExecutionID: "execution", LeaseOwner: "owner", FencingToken: 1,
		ErrorCode: "retryable", Retryable: true, ExpiresAt: now.Add(time.Hour), Now: now,
	}

	store, closeDB := scriptedActionStore(base, &actionDBState{execSteps: []actionExecStep{{}, {err: wantErr}}})
	transaction, err := store.BeginExecutionTransaction(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.RollBack(t.Context()); !errors.Is(err, wantErr) {
		t.Fatalf("sqlite rollback error=%v", err)
	}
	closeDB()

	store, closeDB = scriptedActionStore(base, &actionDBState{rollbackErrors: []error{sql.ErrTxDone}})
	store.profile = ormpostgres.NewProfile()
	transaction, err = store.BeginExecutionTransaction(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.RollBack(t.Context()); err != nil {
		t.Fatalf("already completed rollback error=%v", err)
	}
	closeDB()

	tests := []struct {
		name        string
		state       *actionDBState
		context     func() (context.Context, context.CancelFunc)
		wantKind    string
		wantContext error
	}{
		{
			name:  "rows affected",
			state: &actionDBState{execSteps: []actionExecStep{{rowsErr: wantErr}}},
		},
		{
			name:  "context canceled after update",
			state: &actionDBState{execSteps: []actionExecStep{{rows: 1}}},
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(t.Context())
				return ctx, cancel
			},
			wantContext: context.Canceled,
		},
		{
			name:        "commit canceled",
			state:       &actionDBState{execSteps: []actionExecStep{{rows: 1}}, commitErrors: []error{context.Canceled}},
			wantContext: context.Canceled,
		},
		{
			name:        "commit deadline",
			state:       &actionDBState{execSteps: []actionExecStep{{rows: 1}}, commitErrors: []error{context.DeadlineExceeded}},
			wantContext: context.DeadlineExceeded,
		},
		{
			name:        "commit already done",
			state:       &actionDBState{execSteps: []actionExecStep{{rows: 1}}, commitErrors: []error{sql.ErrTxDone}},
			wantContext: sql.ErrTxDone,
		},
		{
			name:     "commit mutation conflict",
			state:    &actionDBState{execSteps: []actionExecStep{{rows: 1}}, commitErrors: []error{mutation.MutationConflict("business_action_execution", "execution", mutation.MutationConflictUnique, nil)}},
			wantKind: "conflict",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, closeDB := scriptedActionStore(base, tc.state)
			defer closeDB()
			store.profile = ormpostgres.NewProfile()
			ctx := t.Context()
			if tc.context != nil {
				var cancel context.CancelFunc
				ctx, cancel = tc.context()
				tc.state.execSteps[0].hook = cancel
			}
			_, err := commitBusinessActionExecution(ctx, store, nil, completion)
			if err == nil {
				t.Fatal("transaction edge succeeded")
			}
			if tc.wantContext != nil && !errors.Is(err, tc.wantContext) {
				t.Fatalf("context error=%v want=%v", err, tc.wantContext)
			}
			if tc.wantKind == "conflict" && !mutation.IsMutationConflict(err, "") {
				t.Fatalf("mutation conflict error=%v", err)
			}
		})
	}

	store, closeDB = scriptedActionStore(base, &actionDBState{execSteps: []actionExecStep{{}, {err: wantErr}}})
	transaction, err = store.BeginExecutionTransaction(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sqliteTransaction := transaction.(*actionExecutionTransaction)
	if err := sqliteTransaction.commitSQL(t.Context()); !errors.Is(err, wantErr) {
		t.Fatalf("sqlite commit error=%v", err)
	}
	closeDB()
}

func scriptedActionStore(runtimeStore *database.RuntimeStore, state *actionDBState) (ActionBusinessExecutionStore, func()) {
	db := sql.OpenDB(actionConnector{state: state})
	repository := NewActionBusinessExecutionStore(runtimeStore)
	repository.db = db
	return repository, func() { _ = db.Close() }
}

func actionExecutionRow(now time.Time, status, fingerprint string) []driver.Value {
	value := actionmodel.ActionBusinessExecution{
		ID: "execution", WorkspaceID: "workspace-primary", ObjectKey: "object", RecordID: "record", ActionKey: "action", IdempotencyKey: "idem",
		RequestFingerprint: fingerprint, Status: status, LeaseOwner: "owner", LeaseExpiresAt: now.Add(time.Minute).Format(time.RFC3339Nano), FencingToken: 1,
		ResponseStatus: 200, ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano), ActorID: "actor", RoleKey: "role", CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano),
	}
	values := actionExecutionValues(value, `{}`)
	row := make([]driver.Value, len(values))
	for index, item := range values {
		row[index] = item
	}
	return row
}

func actionExecutionColumns() []string {
	return []string{"id", "workspace_id", "system_purpose", "owner", "kind", "action_key", "parent_id", "resource_type", "resource_id", "idempotency_key", "request_fingerprint", "requested_by", "reason", "reference", "status", "status_url", "result_json", "metadata_json", "error_code", "failure_class", "next_action", "related_ids_json", "correlation", "evidence_json", "lease_owner", "lease_expires_at", "fencing_token", "expires_at", "created_at", "started_at", "finished_at", "updated_at"}
}

func actionExecutionValues(value actionmodel.ActionBusinessExecution, resultJSON string) []any {
	record := actionExecutionRecord(value, json.RawMessage(resultJSON))
	return []any{
		record.ID, record.WorkspaceID, record.SystemPurpose, record.Owner, record.Kind, record.ActionKey, record.ParentID,
		record.ResourceType, record.ResourceID, record.IdempotencyKey, record.RequestFingerprint, record.RequestedBy,
		record.Reason, record.Reference, record.Status, record.StatusURL, string(record.ResultJSON), string(record.MetadataJSON),
		record.ErrorCode, record.FailureClass, record.NextAction, string(record.RelatedIDsJSON), record.Correlation,
		string(record.EvidenceJSON), record.LeaseOwner, timevalue.Millis(record.LeaseExpiresAt), record.FencingToken, timevalue.Millis(record.ExpiresAt),
		timevalue.Millis(record.CreatedAt), timevalue.Millis(record.StartedAt), timevalue.Millis(record.FinishedAt), timevalue.Millis(record.UpdatedAt),
	}
}

type actionExecStep struct {
	rows    int64
	err     error
	rowsErr error
	hook    func()
}
type actionQueryStep struct {
	columns []string
	rows    [][]driver.Value
	err     error
	nextErr error
	hook    func()
}
type actionDBState struct {
	execSteps      []actionExecStep
	querySteps     []actionQueryStep
	connectErr     error
	beginErrors    []error
	commitErrors   []error
	rollbackErrors []error
}

type actionConnector struct{ state *actionDBState }

func (c actionConnector) Connect(context.Context) (driver.Conn, error) {
	if c.state.connectErr != nil {
		return nil, c.state.connectErr
	}
	return &actionConn{state: c.state}, nil
}
func (actionConnector) Driver() driver.Driver { return actionDriver{} }

type actionDriver struct{}

func (actionDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type actionConn struct{ state *actionDBState }

func (*actionConn) Prepare(string) (driver.Stmt, error)                            { return nil, driver.ErrSkip }
func (*actionConn) Close() error                                                   { return nil }
func (c *actionConn) Begin() (driver.Tx, error)                                    { return c.begin() }
func (c *actionConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return c.begin() }
func (c *actionConn) begin() (driver.Tx, error) {
	if len(c.state.beginErrors) > 0 {
		err := c.state.beginErrors[0]
		c.state.beginErrors = c.state.beginErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	return &actionTx{state: c.state}, nil
}
func (c *actionConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	if len(c.state.execSteps) == 0 {
		return actionResult{rows: 1}, nil
	}
	step := c.state.execSteps[0]
	c.state.execSteps = c.state.execSteps[1:]
	if step.hook != nil {
		step.hook()
	}
	return actionResult{rows: step.rows, rowsErr: step.rowsErr}, step.err
}
func (c *actionConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if len(c.state.querySteps) == 0 {
		return &actionRows{columns: actionExecutionColumns()}, nil
	}
	step := c.state.querySteps[0]
	c.state.querySteps = c.state.querySteps[1:]
	if step.hook != nil {
		step.hook()
	}
	if step.err != nil {
		return nil, step.err
	}
	return &actionRows{columns: step.columns, rows: step.rows, nextErr: step.nextErr}, nil
}

type actionTx struct{ state *actionDBState }

func (tx *actionTx) Commit() error {
	if len(tx.state.commitErrors) == 0 {
		return nil
	}
	err := tx.state.commitErrors[0]
	tx.state.commitErrors = tx.state.commitErrors[1:]
	return err
}
func (tx *actionTx) Rollback() error {
	if len(tx.state.rollbackErrors) == 0 {
		return nil
	}
	err := tx.state.rollbackErrors[0]
	tx.state.rollbackErrors = tx.state.rollbackErrors[1:]
	return err
}

type actionResult struct {
	rows    int64
	rowsErr error
}

func (r actionResult) LastInsertId() (int64, error) { return 0, nil }
func (r actionResult) RowsAffected() (int64, error) { return r.rows, r.rowsErr }

type actionRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
	nextErr error
}

func (r *actionRows) Columns() []string { return r.columns }
func (*actionRows) Close() error        { return nil }
func (r *actionRows) Next(values []driver.Value) error {
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

type actionRowScanner struct {
	values []driver.Value
	err    error
}

func (r actionRowScanner) Scan(destinations ...any) error {
	if r.err != nil {
		return r.err
	}
	for index, value := range r.values {
		if err := convertAssignAction(destinations[index], value); err != nil {
			return err
		}
	}
	return nil
}
func convertAssignAction(destination any, value driver.Value) error {
	switch target := destination.(type) {
	case *string:
		text, ok := value.(string)
		if !ok {
			return errors.New("not string")
		}
		*target = text
	case *int:
		number, ok := value.(int64)
		if !ok {
			return errors.New("not int")
		}
		*target = int(number)
	case *int64:
		number, ok := value.(int64)
		if !ok {
			return errors.New("not int64")
		}
		*target = number
	default:
		return errors.New("unsupported destination")
	}
	return nil
}
