package record

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func recordMutationExecutionFixture(now time.Time) recordmodel.RecordMutationExecution {
	return recordmodel.RecordMutationExecution{ID: "execution", WorkspaceID: "workspace", Operation: "create", ObjectKey: "customer", TargetID: "record", IdempotencyKey: "key", RequestFingerprint: "fingerprint", Status: "processing", LeaseOwner: "worker", LeaseExpiresAt: now.Add(-time.Minute).Format(time.RFC3339Nano), FencingToken: 1, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)}
}

func recordMutationExecutionQueryStep(value recordmodel.RecordMutationExecution, resultJSON string) recordSQLQueryStep {
	values := recordMutationExecutionValues(value, resultJSON)
	row := make([]driver.Value, len(values))
	for index, value := range values {
		row[index] = value
	}
	return recordSQLQueryStep{columns: recordMutationExecutionColumns(), rows: [][]driver.Value{row}}
}

func TestRecordMutationClaimSQLFailureAndRetryEdges(t *testing.T) {
	now := time.Now().UTC()
	request := recordmodel.RecordMutationClaimRequest{Execution: recordMutationExecutionFixture(now), RequestFingerprint: "fingerprint", LeaseOwner: "worker", Now: now}
	request.Execution.ID = ""
	request.Execution.Status = ""
	request.Execution.LeaseExpiresAt = ""
	if _, err := scriptedRecordStore(t, &recordSQLState{}).TryBeginRecordMutation(t.Context(), recordmodel.RecordMutationClaimRequest{}); err == nil {
		t.Fatal("empty workspace claim accepted")
	}
	defaultClock := request
	defaultClock.Now, defaultClock.LeaseTTL = time.Time{}, 0
	store := scriptedRecordStore(t, &recordSQLState{})
	if claim, err := store.TryBeginRecordMutation(t.Context(), defaultClock); err != nil || claim.Execution.LeaseExpiresAt == "" {
		t.Fatalf("default clock claim=%+v err=%v", claim, err)
	}

	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}, querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, err := store.TryBeginRecordMutation(t.Context(), request); !errors.Is(err, errRecordSQL) {
		t.Fatalf("find error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}, querySteps: []recordSQLQueryStep{{}}})
	if _, err := store.TryBeginRecordMutation(t.Context(), request); err == nil {
		t.Fatal("missing conflicting receipt accepted")
	}

	current := recordMutationExecutionFixture(now)
	for name, state := range map[string]recordSQLState{
		"update":         {execSteps: []recordSQLExecStep{{err: errRecordSQL}, {err: errRecordSQL}}, querySteps: []recordSQLQueryStep{recordMutationExecutionQueryStep(current, `{}`)}},
		"rows":           {execSteps: []recordSQLExecStep{{err: errRecordSQL}, {rowsErr: errRecordSQL}}, querySteps: []recordSQLQueryStep{recordMutationExecutionQueryStep(current, `{}`)}},
		"reread":         {execSteps: []recordSQLExecStep{{err: errRecordSQL}, {rows: 1}}, querySteps: []recordSQLQueryStep{recordMutationExecutionQueryStep(current, `{}`), {err: errRecordSQL}}},
		"missing-reread": {execSteps: []recordSQLExecStep{{err: errRecordSQL}, {rows: 1}}, querySteps: []recordSQLQueryStep{recordMutationExecutionQueryStep(current, `{}`), {}}},
	} {
		t.Run(name, func(t *testing.T) {
			store := scriptedRecordStore(t, &state)
			if _, err := store.TryBeginRecordMutation(t.Context(), request); err == nil {
				t.Fatal("reclaim failure swallowed")
			}
		})
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}, {rows: 0}}, querySteps: []recordSQLQueryStep{recordMutationExecutionQueryStep(current, `{}`), recordMutationExecutionQueryStep(current, `{}`)}})
	if claim, err := store.TryBeginRecordMutation(t.Context(), request); err != nil || claim.Decision != "in_progress" {
		t.Fatalf("lost reclaim claim=%+v err=%v", claim, err)
	}

	if store.store.IsTransientError(nil) || store.store.IsTransientError(errors.New("ordinary")) || !store.store.IsTransientError(errors.New("database is locked")) {
		t.Fatal("SQLite busy classification changed")
	}
	if err := store.store.SetEngineForTesting("mysql"); err != nil {
		t.Fatal(err)
	}
	if store.store.IsTransientError(errors.New("database is locked")) {
		t.Fatal("non-SQLite busy error was retried")
	}
	if err := store.store.SetEngineForTesting("sqlite"); err != nil {
		t.Fatal(err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errors.New("database is locked")}}})
	if claim, err := store.TryBeginRecordMutation(t.Context(), request); err != nil || claim.Execution.ID == "" {
		t.Fatalf("busy retry claim=%+v err=%v", claim, err)
	}
	busySteps := make([]recordSQLExecStep, 50)
	for index := range busySteps {
		busySteps[index].err = errors.New("database is locked")
	}
	ctx, cancel := context.WithCancel(t.Context())
	store = scriptedRecordStore(t, &recordSQLState{execSteps: busySteps, execHook: func() { time.AfterFunc(100*time.Microsecond, cancel) }})
	if _, err := store.TryBeginRecordMutation(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled busy retry=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: busySteps})
	if _, err := store.TryBeginRecordMutation(t.Context(), request); err == nil {
		t.Fatal("exhausted busy retry accepted")
	}
}

func TestRecordMutationCompletionSQLFailureEdges(t *testing.T) {
	now := time.Now().UTC()
	completion := recordmodel.RecordMutationCompletion{WorkspaceID: "workspace", ExecutionID: "execution", LeaseOwner: "worker", FencingToken: 1, Result: map[string]any{"ok": true}, ExpiresAt: now.Add(time.Hour), Now: now}
	commit := transactionmodel.RecordMutationCommit{Operation: "create", Object: definitionmodel.ObjectSchema{Key: "customer"}, Record: recordmodel.Record{ID: "record"}}
	if _, err := scriptedRecordStore(t, &recordSQLState{}).CommitRecordMutationExecution(t.Context(), commit, recordmodel.RecordMutationCompletion{}); err == nil {
		t.Fatal("empty atomic completion accepted")
	}
	store := scriptedRecordStore(t, &recordSQLState{beginErr: errRecordSQL})
	if _, err := store.CommitRecordMutationExecution(t.Context(), commit, completion); !errors.Is(err, errRecordSQL) {
		t.Fatalf("begin error=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}})
	if _, err := store.CommitRecordMutationExecution(t.Context(), commit, completion); !errors.Is(err, errRecordSQL) {
		t.Fatalf("mutation error=%v", err)
	}
	badCompletion := completion
	badCompletion.Result = map[string]any{"bad": func() {}}
	store = scriptedRecordStore(t, &recordSQLState{})
	if _, err := store.CommitRecordMutationExecution(t.Context(), commit, badCompletion); err == nil {
		t.Fatal("invalid atomic result accepted")
	}
	zeroClock := completion
	zeroClock.Now = time.Time{}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rows: 1}, {err: errRecordSQL}}})
	if _, err := store.CommitRecordMutationExecution(t.Context(), commit, zeroClock); !errors.Is(err, errRecordSQL) {
		t.Fatalf("zero-clock atomic update=%v", err)
	}

	for name, state := range map[string]recordSQLState{
		"update": {execSteps: []recordSQLExecStep{{rows: 1}, {err: errRecordSQL}}},
		"rows":   {execSteps: []recordSQLExecStep{{rows: 1}, {rowsErr: errRecordSQL}}},
		"commit": {execSteps: []recordSQLExecStep{{rows: 1}, {rows: 1}}, commitErr: errRecordSQL},
		"find":   {execSteps: []recordSQLExecStep{{rows: 1}, {rows: 1}}, querySteps: []recordSQLQueryStep{{err: errRecordSQL}}},
	} {
		t.Run("atomic-"+name, func(t *testing.T) {
			store := scriptedRecordStore(t, &state)
			if _, err := store.CommitRecordMutationExecution(t.Context(), commit, completion); err == nil {
				t.Fatal("atomic completion failure swallowed")
			}
		})
	}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{rows: 1}, {rows: 0}}, querySteps: []recordSQLQueryStep{{err: errRecordSQL}}})
	if _, err := store.CommitRecordMutationExecution(t.Context(), commit, completion); err == nil {
		t.Fatal("atomic lost lease accepted")
	}

	if _, err := scriptedRecordStore(t, &recordSQLState{}).CompleteRecordMutationExecution(t.Context(), recordmodel.RecordMutationCompletion{}); err == nil {
		t.Fatal("empty completion accepted")
	}
	for name, state := range map[string]recordSQLState{
		"exec": {execSteps: []recordSQLExecStep{{err: errRecordSQL}}},
		"rows": {execSteps: []recordSQLExecStep{{rowsErr: errRecordSQL}}},
		"find": {execSteps: []recordSQLExecStep{{rows: 1}}, querySteps: []recordSQLQueryStep{{err: errRecordSQL}}},
	} {
		t.Run("complete-"+name, func(t *testing.T) {
			store := scriptedRecordStore(t, &state)
			if _, err := store.CompleteRecordMutationExecution(t.Context(), completion); err == nil {
				t.Fatal("completion failure swallowed")
			}
		})
	}
	zeroClock = completion
	zeroClock.Now = time.Time{}
	store = scriptedRecordStore(t, &recordSQLState{execSteps: []recordSQLExecStep{{err: errRecordSQL}}})
	if _, err := store.CompleteRecordMutationExecution(t.Context(), zeroClock); !errors.Is(err, errRecordSQL) {
		t.Fatalf("zero-clock completion=%v", err)
	}
	store = scriptedRecordStore(t, &recordSQLState{})
	if _, err := store.CompleteRecordMutationExecution(t.Context(), badCompletion); err == nil {
		t.Fatal("invalid result accepted")
	}

	corrupt := recordMutationExecutionQueryStep(recordMutationExecutionFixture(now), `{`)
	store = scriptedRecordStore(t, &recordSQLState{querySteps: []recordSQLQueryStep{corrupt}})
	if _, _, err := store.findRecordMutationExecution(t.Context(), recordMutationExecutionFixture(now)); err == nil {
		t.Fatal("corrupt execution accepted")
	}
	if _, err := store.findRecordMutationExecutionByID(t.Context(), "", "execution"); err == nil {
		t.Fatal("empty workspace lookup accepted")
	}
}
