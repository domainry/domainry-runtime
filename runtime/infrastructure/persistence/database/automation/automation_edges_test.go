package automation

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

func TestAutomationExecutionEncodingScanAndFilterEdges(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAutomationExecutionStore(store)
	if _, err := repository.InsertExecution(t.Context(), "default", automationmodel.AutomationRuleExecution{Candidate: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("candidate marshal succeeded")
	}
	if _, err := repository.InsertExecution(t.Context(), "default", automationmodel.AutomationRuleExecution{Trace: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("trace marshal succeeded")
	}
	if _, err := repository.InsertExecutionSeed(t.Context(), "default", automationmodel.AutomationRuleExecution{}); err == nil {
		t.Fatal("empty seed id accepted")
	}
	if _, err := repository.InsertExecutionSeed(t.Context(), "default", automationmodel.AutomationRuleExecution{ID: "bad-candidate", Candidate: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("seed candidate marshal succeeded")
	}
	if _, err := repository.InsertExecutionSeed(t.Context(), "default", automationmodel.AutomationRuleExecution{ID: "bad-trace", Trace: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("seed trace marshal succeeded")
	}
	if _, err := repository.InsertExecutionSeed(t.Context(), "default", automationmodel.AutomationRuleExecution{ID: "fixed-time", CreatedAt: "created", UpdatedAt: "updated"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDialectForTesting("mysql"); err != nil {
		t.Fatal(err)
	}
	mysql := NewAutomationExecutionStore(store)
	mysqlDB := sql.OpenDB(automationConnector{state: &automationDBState{}})
	defer mysqlDB.Close()
	mysql.db = mysqlDB
	if _, err := mysql.InsertExecutionSeed(t.Context(), "default", automationmodel.AutomationRuleExecution{ID: "mysql-seed"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDialectForTesting("sqlite"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ListExecutions(t.Context(), "default", automationmodel.AutomationExecutionFilter{RuleKey: "x", ObjectKey: "x", RecordID: "x", Phase: "x", Status: "x", ConnectorKey: "x", From: "a", To: "z", Limit: 501}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ListExecutions(t.Context(), "default", automationmodel.AutomationExecutionFilter{}); err != nil {
		t.Fatal(err)
	}

	columns := []string{"id", "workspace", "rule", "object", "record", "phase", "operation", "status", "actor", "role", "request", "correlation", "event", "duration", "error", "candidate", "trace", "created", "updated"}
	valid := []driver.Value{"id", "default", "rule", "object", "record", "after", "update", "ok", "actor", "role", "request", "correlation", "event", int64(1), "", "{}", "{}", "created", "updated"}
	for _, test := range []struct {
		name string
		step automationQueryStep
	}{
		{"query", automationQueryStep{err: errors.New("query")}},
		{"scan", automationQueryStep{columns: append(columns, "extra"), rows: [][]driver.Value{append(valid, "extra")}}},
		{"candidate json", automationQueryStep{columns: columns, rows: [][]driver.Value{replaceAutomationValue(valid, 15, "{")}}},
		{"trace json", automationQueryStep{columns: columns, rows: [][]driver.Value{replaceAutomationValue(valid, 16, "{")}}},
		{"rows", automationQueryStep{columns: columns, nextErr: errors.New("rows")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := repository
			db := sql.OpenDB(automationConnector{state: &automationDBState{querySteps: []automationQueryStep{test.step}}})
			copy.db = db
			defer db.Close()
			if _, err := copy.ListExecutions(t.Context(), "default", automationmodel.AutomationExecutionFilter{Limit: 1}); err == nil {
				t.Fatal("list failure ignored")
			}
		})
	}
	if _, err := scanAutomationRuleExecution(automationScanner{err: sql.ErrNoRows}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("no rows=%v", err)
	}
}

func TestAutomationWorkerInputRetryAndCodecEdges(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAutomationWorkerStore(store)
	if err := waitAutomationClaimRetry(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitAutomationClaimRetry(cancelled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait=%v", err)
	}
	if _, _, err := repository.ClaimInstruction(t.Context(), "default", automationmodel.AutomationInstructionExecution{}, "worker", "", ""); err == nil {
		t.Fatal("empty key accepted")
	}
	if _, _, err := repository.ClaimInstruction(t.Context(), "default", automationmodel.AutomationInstructionExecution{IdempotencyKey: "key"}, "", "", ""); err == nil {
		t.Fatal("empty owner accepted")
	}
	if _, _, err := repository.ClaimInstruction(t.Context(), "default", automationmodel.AutomationInstructionExecution{IdempotencyKey: "bad", Result: map[string]any{"bad": make(chan int)}}, "worker", "", ""); err == nil {
		t.Fatal("bad result accepted")
	}
	claim, won, err := repository.ClaimInstruction(t.Context(), "default", automationmodel.AutomationInstructionExecution{IdempotencyKey: "defaults"}, "worker", "", "")
	if err != nil || !won || claim.Result == nil {
		t.Fatalf("claim=%#v won=%v err=%v", claim, won, err)
	}
	if _, err := repository.CompleteInstruction(t.Context(), "default", "defaults", "worker", claim.FencingToken, "ok", map[string]any{"bad": make(chan int)}, "", "now"); err == nil {
		t.Fatal("bad completion result accepted")
	}
	if _, err := repository.CompleteInstruction(t.Context(), "default", "defaults", "worker", claim.FencingToken, "ok", nil, "", " "); err == nil {
		t.Fatal("empty completion time accepted")
	}
	if _, err := repository.HeartbeatInstruction(t.Context(), "default", "defaults", "worker", claim.FencingToken, "later", " "); err == nil {
		t.Fatal("empty heartbeat time accepted")
	}
	if _, found, err := repository.find(t.Context(), "default", "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	if _, _, err := repository.find(t.Context(), "", "missing"); err == nil {
		t.Fatal("invalid find workspace accepted")
	}
	if !repository.isSQLiteBusyError(errors.New("SQLITE_BUSY")) || !repository.isSQLiteBusyError(errors.New("database is locked")) || !repository.isSQLiteBusyError(errors.New("database table is locked")) || repository.isSQLiteBusyError(errors.New("other")) || repository.isSQLiteBusyError(nil) {
		t.Fatal("busy classification mismatch")
	}
	nonSQLite := repository
	nonSQLite.driver = "mysql"
	if nonSQLite.isSQLiteBusyError(errors.New("database is locked")) {
		t.Fatal("non-SQLite error classified as busy")
	}
	if _, err := scanAutomationInstructionExecution(automationScanner{values: automationInstructionRow("{")}); err == nil {
		t.Fatal("bad instruction JSON accepted")
	}

	busy := errors.New("database is locked")
	state := &automationDBState{execSteps: []automationExecStep{{err: busy}, {rows: 1}}}
	worker, closeDB := scriptedAutomationWorker(repository, state)
	worker.wait = func(context.Context, time.Duration) error { return nil }
	if _, won, err := worker.ClaimInstruction(t.Context(), "default", automationmodel.AutomationInstructionExecution{IdempotencyKey: "retry"}, "worker", "now", "later"); err != nil || !won {
		t.Fatalf("retry won=%v err=%v", won, err)
	}
	closeDB()
	steps := make([]automationExecStep, 50)
	for index := range steps {
		steps[index].err = busy
	}
	worker, closeDB = scriptedAutomationWorker(repository, &automationDBState{execSteps: steps})
	worker.wait = func(context.Context, time.Duration) error { return nil }
	if _, _, err := worker.ClaimInstruction(t.Context(), "default", automationmodel.AutomationInstructionExecution{IdempotencyKey: "exhaust"}, "worker", "now", "later"); err == nil {
		t.Fatal("busy exhaustion succeeded")
	}
	closeDB()
	worker, closeDB = scriptedAutomationWorker(repository, &automationDBState{execSteps: []automationExecStep{{err: busy}}})
	worker.wait = func(context.Context, time.Duration) error { return context.Canceled }
	if _, _, err := worker.ClaimInstruction(t.Context(), "default", automationmodel.AutomationInstructionExecution{IdempotencyKey: "cancel"}, "worker", "now", "later"); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error=%v", err)
	}
	closeDB()
}

type automationScanner struct {
	values []driver.Value
	err    error
}

func (s automationScanner) Scan(dest ...any) error {
	if s.err != nil {
		return s.err
	}
	for index, value := range s.values {
		if err := automationAssign(dest[index], value); err != nil {
			return err
		}
	}
	return nil
}
func automationAssign(dest any, value driver.Value) error {
	switch target := dest.(type) {
	case *string:
		*target = value.(string)
	case *int:
		*target = int(value.(int64))
	case *int64:
		*target = value.(int64)
	default:
		return errors.New("unsupported destination")
	}
	return nil
}
func replaceAutomationValue(values []driver.Value, index int, value driver.Value) []driver.Value {
	out := append([]driver.Value(nil), values...)
	out[index] = value
	return out
}
func automationInstructionRow(result string) []driver.Value {
	return []driver.Value{"id", "default", "key", "rule", "object", "record", "version", "operation", "instruction", "processing", result, "", "worker", "later", int64(1), "created", "updated"}
}
