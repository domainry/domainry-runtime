package operations

import (
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func operationsReceiptFixture(now time.Time) operationsmodel.OperationsReceipt {
	return operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{
		ID: "operation", Kind: "retention.cleanup", ActionKey: "runtime.operations.run_lifecycle_cleanup_job",
		Scope:          operationsmodel.OperationsScope{WorkspaceID: "workspace", ResourceType: "retention_policy", ResourceID: "policy"},
		IdempotencyKey: "key", RequestFingerprint: "fingerprint", RequestedBy: "operator", Reason: "reason",
		Status: operationsmodel.OperationsStatusCreated, CreatedAt: now, UpdatedAt: now,
	}, StatusURL: "/operations/operation", Result: []byte(`{"ok":true}`), RelatedIDs: []string{"related"}, Evidence: []string{"evidence"}}
}

func operationsReceiptRow(receipt operationsmodel.OperationsReceipt, startedAt, finishedAt string) []driver.Value {
	resultJSON, relatedJSON, evidenceJSON := operationsReceiptJSON(receipt)
	command := receipt.Command
	return []driver.Value{command.ID, command.Scope.WorkspaceID, command.Scope.SystemPurpose, command.Kind, command.ActionKey,
		command.Scope.ResourceType, command.Scope.ResourceID, command.IdempotencyKey, command.RequestFingerprint, command.RequestedBy,
		command.Reason, command.Reference, string(command.Status), receipt.StatusURL, resultJSON, receipt.ErrorCode,
		string(receipt.FailureClass), receipt.NextAction, relatedJSON, receipt.Correlation, evidenceJSON,
		command.CreatedAt.Format(time.RFC3339Nano), startedAt, finishedAt, command.UpdatedAt.Format(time.RFC3339Nano)}
}

func TestOperationsStoreSQLFailureStagesAndScopeEdges(t *testing.T) {
	now := time.Now().UTC()
	receipt := operationsReceiptFixture(now)
	for _, test := range []struct {
		name  string
		state operationsSQLState
	}{
		{name: "begin", state: operationsSQLState{beginErr: errOperationsSQL}},
		{name: "insert", state: operationsSQLState{execSteps: []operationsSQLExecStep{{err: errOperationsSQL}}, querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}}},
		{name: "commit", state: operationsSQLState{commitErr: errOperationsSQL}},
	} {
		t.Run("register-"+test.name, func(t *testing.T) {
			store := scriptedOperationsStore(t, &test.state)
			if _, _, err := store.RegisterOperationsCommand(t.Context(), receipt); !errors.Is(err, errOperationsSQL) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	store := scriptedOperationsStore(t, &operationsSQLState{execSteps: []operationsSQLExecStep{{err: errOperationsSQL}}, querySteps: []operationsSQLQueryStep{{}}})
	if _, _, err := store.RegisterOperationsCommand(t.Context(), receipt); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("missing replay error=%v", err)
	}

	zero := OperationsStore{}
	if zero.database() != nil {
		t.Fatal("zero store returned a database")
	}
	for index, call := range []func() error{
		func() error {
			_, _, err := zero.RegisterOperationsCommand(t.Context(), receipt)
			return err
		},
		func() error {
			_, _, err := zero.GetOperationsReceipt(t.Context(), receipt.Command.Scope, receipt.Command.ID)
			return err
		},
		func() error {
			_, _, err := zero.GetOperationsReceiptByKey(t.Context(), receipt.Command.Scope, receipt.Command.Kind, receipt.Command.IdempotencyKey)
			return err
		},
		func() error {
			_, err := zero.ListOperationsReceipts(t.Context(), receipt.Command.Scope, "", 10)
			return err
		},
	} {
		if err := call(); err == nil {
			t.Fatalf("unavailable operation %d succeeded", index)
		}
	}

	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{err: errOperationsSQL}}})
	if _, err := store.ListOperationsReceipts(t.Context(), receipt.Command.Scope, operationsmodel.OperationsStatusCreated, 10); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("list query error=%v", err)
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{columns: operationsReceiptColumns(), rows: [][]driver.Value{{"short"}}}}})
	if _, err := store.ListOperationsReceipts(t.Context(), receipt.Command.Scope, "", 10); err == nil {
		t.Fatal("list scan failure swallowed")
	}
	store = scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{{columns: operationsReceiptColumns(), nextErr: errOperationsSQL}}})
	if _, err := store.ListOperationsReceipts(t.Context(), receipt.Command.Scope, "", 10); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("list terminal error=%v", err)
	}

	systemScope := operationsmodel.OperationsScope{SystemPurpose: "maintenance"}
	if where, args := store.scopePredicate(systemScope, 2); where == "1 = 0" || len(args) != 1 {
		t.Fatalf("system scope where=%q args=%v", where, args)
	}
	if where, args := store.scopePredicate(operationsmodel.OperationsScope{}, 1); where != "1 = 0" || len(args) != 0 {
		t.Fatalf("invalid scope where=%q args=%v", where, args)
	}
	withNilFaults := NewOperationsStoreWithFaults(store.store, nil)
	if withNilFaults.faults == nil {
		t.Fatal("nil fault injector was not normalized")
	}
}

func TestOperationsStoreSearchFailureAndSummaryEdges(t *testing.T) {
	zero := OperationsStore{}
	if _, err := zero.SearchOperationsReceipts(t.Context(), operationsmodel.OperationsScope{WorkspaceID: "workspace"}, operationsmodel.OperationsReceiptFilter{Limit: 10}); err == nil {
		t.Fatal("unavailable search succeeded")
	}

	count := operationsSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}
	summaryColumns := []string{"status", "failure_class", "count"}
	receipt := operationsReceiptFixture(time.Now().UTC())
	itemColumns := operationsReceiptColumns()
	itemRow := operationsReceiptRow(receipt, "", "")
	tests := []struct {
		name  string
		steps []operationsSQLQueryStep
	}{
		{name: "count", steps: []operationsSQLQueryStep{{err: errOperationsSQL}}},
		{name: "summary query", steps: []operationsSQLQueryStep{count, {err: errOperationsSQL}}},
		{name: "summary scan", steps: []operationsSQLQueryStep{count, {columns: summaryColumns, rows: [][]driver.Value{{"started"}}}}},
		{name: "summary iteration", steps: []operationsSQLQueryStep{count, {columns: summaryColumns, nextErr: errOperationsSQL}}},
		{name: "items query", steps: []operationsSQLQueryStep{count, {columns: summaryColumns}, {err: errOperationsSQL}}},
		{name: "items scan", steps: []operationsSQLQueryStep{count, {columns: summaryColumns}, {columns: itemColumns, rows: [][]driver.Value{{"short"}}}}},
		{name: "items iteration", steps: []operationsSQLQueryStep{count, {columns: summaryColumns}, {columns: itemColumns, nextErr: errOperationsSQL}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := scriptedOperationsStore(t, &operationsSQLState{querySteps: test.steps})
			if _, err := store.SearchOperationsReceipts(t.Context(), receipt.Command.Scope, operationsmodel.OperationsReceiptFilter{Limit: 10}); err == nil {
				t.Fatal("search failure swallowed")
			}
		})
	}

	store := scriptedOperationsStore(t, &operationsSQLState{querySteps: []operationsSQLQueryStep{
		count,
		{columns: summaryColumns, rows: [][]driver.Value{
			{string(operationsmodel.OperationsStatusCreated), "", int64(1)},
			{string(operationsmodel.OperationsStatusStarted), "", int64(2)},
			{string(operationsmodel.OperationsStatusSucceeded), "", int64(3)},
		}},
		{columns: itemColumns, rows: [][]driver.Value{itemRow}},
	}})
	page, err := store.SearchOperationsReceipts(t.Context(), receipt.Command.Scope, operationsmodel.OperationsReceiptFilter{Limit: 10})
	if err != nil || page.Count != 1 || page.Summary.Created != 1 || page.Summary.Started != 2 || page.Summary.Succeeded != 3 || len(page.Items) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestOperationsUpdateAndReceiptScanEdges(t *testing.T) {
	now := time.Now().UTC()
	receipt := operationsReceiptFixture(now)
	started, finished := now.Add(time.Minute), now.Add(2*time.Minute)
	receipt.Command.StartedAt, receipt.Command.FinishedAt = &started, &finished
	for _, test := range []struct {
		name  string
		state operationsSQLState
	}{
		{name: "exec", state: operationsSQLState{execSteps: []operationsSQLExecStep{{err: errOperationsSQL}}}},
		{name: "rows", state: operationsSQLState{execSteps: []operationsSQLExecStep{{rowsErr: errOperationsSQL}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := scriptedOperationsStore(t, &test.state)
			if _, err := store.UpdateOperationsReceipt(t.Context(), receipt, operationsmodel.OperationsStatusCreated); !errors.Is(err, errOperationsSQL) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	nilTimes := operationsReceiptFixture(now)
	store := scriptedOperationsStore(t, &operationsSQLState{execSteps: []operationsSQLExecStep{{rows: 1}}})
	if changed, err := store.UpdateOperationsReceipt(t.Context(), nilTimes, operationsmodel.OperationsStatusCreated); err != nil || !changed {
		t.Fatalf("nil-time update changed=%v err=%v", changed, err)
	}

	base := operationsReceiptRow(receipt, started.Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano))
	if scanned, err := operationsScanReceipt(scannerValues(base)); err != nil || scanned.Command.StartedAt == nil || scanned.Command.FinishedAt == nil {
		t.Fatalf("scanned=%+v err=%v", scanned, err)
	}
	for _, test := range []struct {
		name  string
		index int
		value driver.Value
	}{
		{name: "related", index: 18, value: "{"},
		{name: "evidence", index: 20, value: "{"},
		{name: "created", index: 21, value: "invalid"},
		{name: "updated", index: 24, value: "invalid"},
		{name: "started", index: 22, value: "invalid"},
		{name: "finished", index: 23, value: "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := append([]driver.Value(nil), base...)
			row[test.index] = test.value
			if _, err := operationsScanReceipt(scannerValues(row)); err == nil {
				t.Fatal("corrupt receipt accepted")
			}
		})
	}
}

type scannerValues []driver.Value

func (values scannerValues) Scan(destinations ...any) error {
	if len(values) != len(destinations) {
		return errOperationsSQL
	}
	for index, destination := range destinations {
		switch target := destination.(type) {
		case *string:
			value, ok := values[index].(string)
			if !ok {
				return errOperationsSQL
			}
			*target = value
		case *int64:
			value, ok := values[index].(int64)
			if !ok {
				return errOperationsSQL
			}
			*target = value
		default:
			return errOperationsSQL
		}
	}
	return nil
}
