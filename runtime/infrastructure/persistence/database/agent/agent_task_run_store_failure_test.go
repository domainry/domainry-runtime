package agent

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	mysqldriver "github.com/go-sql-driver/mysql"
)

func validTaskRun(now time.Time) agentmodel.AgentTaskRun {
	return agentmodel.AgentTaskRun{ID: "run", WorkspaceID: "default", TaskKey: "task", TaskVersion: "1", Status: agentmodel.AgentTaskRunPending, IdempotencyKey: "idem", MaxAttempts: 2, CreatedAt: now, UpdatedAt: now, Revision: 1}
}

func taskRunRow(t *testing.T, run agentmodel.AgentTaskRun) agentStateQueryStep {
	return agentStateQueryStep{columns: []string{"payload_json"}, rows: [][]driver.Value{{mustJSON(t, run)}}}
}

func taskState(step agentStateQueryStep, extra ...error) *agentStateDBState {
	return &agentStateDBState{execErrors: taskSchemaExecs(extra...), querySteps: []agentStateQueryStep{step}}
}

func TestAgentTaskRunStoreSchemaCreateGetAndListFailureMatrix(t *testing.T) {
	base, now, wantErr := openAgentStateBaseStore(t), time.Unix(10, 0).UTC(), errors.New("task store failure")
	if err := NewAgentTaskRunStore(nil).EnsureSchema(t.Context()); err == nil {
		t.Fatal("nil store schema accepted")
	}
	if err := (&AgentTaskRunStore{store: base}).EnsureSchema(t.Context()); err == nil {
		t.Fatal("nil db schema accepted")
	}
	mysqlBase := openAgentStateBaseStore(t)
	if err := mysqlBase.SetDialectForTesting("mysql"); err != nil {
		t.Fatal(err)
	}
	mysqlState := &agentStateDBState{execErrors: []error{nil, wantErr, wantErr, wantErr}}
	mysqlRepo, mysqlClose := scriptedAgentTaskStore(mysqlBase, mysqlState)
	if err := mysqlRepo.EnsureSchema(t.Context()); !errors.Is(err, wantErr) || !strings.Contains(mysqlState.queries[0], "VARCHAR(255)") {
		t.Fatalf("mysql=%v err=%v", mysqlState.queries, err)
	}
	mysqlClose()
	duplicateIndex := &mysqldriver.MySQLError{Number: 1061, Message: "duplicate key name"}
	mysqlDuplicateState := &agentStateDBState{execErrors: []error{nil, duplicateIndex, duplicateIndex, duplicateIndex}}
	mysqlDuplicateRepo, mysqlDuplicateClose := scriptedAgentTaskStore(mysqlBase, mysqlDuplicateState)
	if err := mysqlDuplicateRepo.EnsureSchema(t.Context()); err != nil {
		t.Fatalf("mysql duplicate index=%v err=%v", mysqlDuplicateState.queries, err)
	}
	mysqlDuplicateClose()
	indexRepo, indexClose := scriptedAgentTaskStore(base, &agentStateDBState{execErrors: []error{nil, wantErr}})
	if err := indexRepo.EnsureSchema(t.Context()); !errors.Is(err, wantErr) {
		t.Fatalf("index=%v", err)
	}
	indexClose()

	run := validTaskRun(now)
	badRun := run
	badRun.Input = map[string]any{"bad": make(chan int)}
	for _, test := range []struct {
		name              string
		run               agentmodel.AgentTaskRun
		state             *agentStateDBState
		replay, wantError bool
	}{
		{"schema", run, &agentStateDBState{execErrors: []error{wantErr}}, false, true},
		{"marshal", badRun, &agentStateDBState{execErrors: taskSchemaExecs()}, false, true},
		{"insert missing", run, taskState(agentStateQueryStep{columns: []string{"payload_json"}}, wantErr), false, true},
		{"insert query", run, taskState(agentStateQueryStep{err: wantErr}, wantErr), false, true},
		{"replay", run, taskState(taskRunRow(t, run), wantErr), true, false},
	} {
		t.Run("create "+test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			got, replay, err := repo.Create(t.Context(), test.run)
			if replay != test.replay || test.wantError != (err != nil) || replay && got.ID != run.ID {
				t.Fatalf("got=%#v replay=%v err=%v", got, replay, err)
			}
		})
	}

	for _, test := range []struct {
		name             string
		state            *agentStateDBState
		found, wantError bool
	}{
		{"schema", &agentStateDBState{execErrors: []error{wantErr}}, false, true},
		{"missing", taskState(agentStateQueryStep{columns: []string{"payload_json"}}), false, false},
		{"query", taskState(agentStateQueryStep{err: wantErr}), false, true},
		{"scan", taskState(agentStateQueryStep{columns: []string{"payload_json", "extra"}, rows: [][]driver.Value{{mustJSON(t, run), "x"}}}), false, true},
		{"json", taskState(agentStateQueryStep{columns: []string{"payload_json"}, rows: [][]driver.Value{{[]byte("{")}}}), false, true},
		{"success", taskState(taskRunRow(t, run)), true, false},
	} {
		t.Run("get "+test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			got, found, err := repo.Get(t.Context(), "default", "run")
			if found != test.found || test.wantError != (err != nil) || found && got.ID != "run" {
				t.Fatalf("got=%#v found=%v err=%v", got, found, err)
			}
		})
	}

	for _, test := range []struct {
		name      string
		filter    agentrepository.AgentTaskRunFilter
		state     *agentStateDBState
		wantLen   int
		wantError bool
	}{
		{"schema", agentrepository.AgentTaskRunFilter{}, &agentStateDBState{execErrors: []error{wantErr}}, 0, true},
		{"query", agentrepository.AgentTaskRunFilter{}, taskState(agentStateQueryStep{err: wantErr}), 0, true},
		{"scan", agentrepository.AgentTaskRunFilter{}, taskState(agentStateQueryStep{columns: []string{"payload_json", "extra"}, rows: [][]driver.Value{{mustJSON(t, run), "x"}}}), 0, true},
		{"json", agentrepository.AgentTaskRunFilter{}, taskState(agentStateQueryStep{columns: []string{"payload_json"}, rows: [][]driver.Value{{[]byte("{")}}}), 0, true},
		{"rows", agentrepository.AgentTaskRunFilter{}, taskState(agentStateQueryStep{columns: []string{"payload_json"}, nextErr: wantErr}), 0, true},
		{"filters", agentrepository.AgentTaskRunFilter{ProcessID: " process ", TaskKey: " task ", Statuses: []agentmodel.AgentTaskRunStatus{agentmodel.AgentTaskRunPending}, Limit: 1}, taskState(taskRunRow(t, run)), 1, false},
		{"large limit", agentrepository.AgentTaskRunFilter{Limit: 501}, taskState(agentStateQueryStep{columns: []string{"payload_json"}}), 0, false},
	} {
		t.Run("list "+test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			runs, err := repo.List(t.Context(), "default", test.filter)
			if len(runs) != test.wantLen || test.wantError != (err != nil) {
				t.Fatalf("runs=%#v err=%v", runs, err)
			}
		})
	}
}

func TestAgentTaskRunStoreClaimFailureMatrix(t *testing.T) {
	base, now, wantErr := openAgentStateBaseStore(t), time.Unix(10, 0).UTC(), errors.New("claim failure")
	run := validTaskRun(now)
	row := agentStateQueryStep{columns: []string{"run_id", "payload_json", "fencing_token"}, rows: [][]driver.Value{{run.ID, mustJSON(t, run), int64(2)}}}
	successState := func(extra ...error) *agentStateDBState { return taskState(row, extra...) }
	rowsErr := successState(nil)
	rowsErr.resultErrors = []error{nil, nil, nil, nil, wantErr}
	rowsMiss := successState(nil)
	rowsMiss.execRows = []int64{1, 1, 1, 1, 0}
	commitErr := successState(nil)
	commitErr.commitErrors = []error{wantErr}
	for _, test := range []struct {
		name             string
		state            *agentStateDBState
		found, wantError bool
	}{
		{"schema", &agentStateDBState{execErrors: []error{wantErr}}, false, true},
		{"begin", &agentStateDBState{execErrors: taskSchemaExecs(), beginErrors: []error{wantErr}}, false, true},
		{"missing", taskState(agentStateQueryStep{columns: row.columns}), false, false},
		{"query", taskState(agentStateQueryStep{err: wantErr}), false, true},
		{"scan", taskState(agentStateQueryStep{columns: append(row.columns, "extra"), rows: [][]driver.Value{{run.ID, mustJSON(t, run), int64(2), "x"}}}), false, true},
		{"json", taskState(agentStateQueryStep{columns: row.columns, rows: [][]driver.Value{{run.ID, []byte("{"), int64(2)}}}), false, true},
		{"update", successState(wantErr), false, true},
		{"rows error", rowsErr, false, true},
		{"race", rowsMiss, false, false},
		{"commit", commitErr, false, true},
		{"success", successState(nil), true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, close := scriptedAgentTaskStore(base, test.state)
			defer close()
			claim, found, err := repo.ClaimNext(t.Context(), "default", "worker", now, time.Minute)
			if found != test.found || test.wantError != (err != nil) || found && claim.Lease.FencingToken != 3 {
				t.Fatalf("claim=%#v found=%v err=%v", claim, found, err)
			}
		})
	}
	if !strings.Contains(eligibleWithOffset(base, 9), base.Placeholder(10)) {
		t.Fatal("eligible placeholders")
	}
	if timeMillis(nil) != 0 {
		t.Fatal("nil time")
	}
	value := now
	if timeMillis(&value) != now.UnixMilli() {
		t.Fatal("time millis")
	}
	if ok, err := agentTaskExactlyOneRow(agentStateResult{rows: 1}); err != nil || !ok {
		t.Fatalf("rows=%v/%v", ok, err)
	}
	if ok, err := agentTaskExactlyOneRow(agentStateResult{err: wantErr}); !errors.Is(err, wantErr) || ok {
		t.Fatalf("rows error=%v/%v", ok, err)
	}
}

func TestAgentTaskClaimRetryBudgetAndCancellation(t *testing.T) {
	for _, message := range []string{
		"database is locked",
		"database table is locked",
		"SQLITE_BUSY",
		"deadlock",
		"Error 1213",
		"SQLSTATE 40001",
		"serialization failure",
		"could not serialize access",
		"lock wait timeout",
	} {
		if !agentTaskClaimRetryable(errors.New(message)) {
			t.Fatalf("retry marker %q was not classified", message)
		}
	}
	if agentTaskClaimRetryable(nil) || agentTaskClaimRetryable(errors.New("permanent failure")) {
		t.Fatal("non-retryable claim error was classified as retryable")
	}

	base := openAgentStateBaseStore(t)
	retryErrors := make([]error, 16)
	for index := range retryErrors {
		retryErrors[index] = errors.New("database is locked")
	}
	repository, closeDB := scriptedAgentTaskStore(base, &agentStateDBState{beginErrors: retryErrors})
	repository.schemaOnce.Do(func() {})
	if _, _, err := repository.ClaimNext(t.Context(), "default", "worker", time.Unix(10, 0).UTC(), time.Minute); err == nil || !strings.Contains(err.Error(), "retry exhausted") {
		t.Fatalf("retry exhaustion err=%v", err)
	}
	closeDB()

	ctx, cancel := context.WithCancel(t.Context())
	repository, closeDB = scriptedAgentTaskStore(base, &agentStateDBState{
		beginErrors: []error{errors.New("SQLSTATE 40001")},
		beginHook:   cancel,
	})
	repository.schemaOnce.Do(func() {})
	if _, _, err := repository.ClaimNext(ctx, "default", "worker", time.Unix(10, 0).UTC(), time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retry err=%v", err)
	}
	closeDB()
}
