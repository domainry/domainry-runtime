package workflow

import (
	"database/sql/driver"
	"errors"
	"testing"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func workflowProcessFailureStore(t *testing.T, state *workflowSQLState) WorkflowProcessStore {
	t.Helper()
	base := openStoreForGeneratedListTest(t)
	db := openWorkflowScriptedDB(state)
	t.Cleanup(func() {
		_ = db.Close()
		_ = base.Close()
	})
	store := NewWorkflowProcessStore(base)
	store.db = db
	return store
}

func TestWorkflowProcessStoreSQLFailures(t *testing.T) {
	process := workflowmodel.WorkflowProcessInstance{ID: "process"}
	node := workflowmodel.WorkflowNodeInstance{ID: "node"}
	task := workflowmodel.WorkflowTask{ID: "task"}
	queryError := workflowSQLState{querySteps: []workflowSQLQueryStep{{err: errWorkflowSQL}}}
	rowError := workflowSQLState{querySteps: []workflowSQLQueryStep{{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}}}}
	terminalError := workflowSQLState{querySteps: []workflowSQLQueryStep{{nextErr: errWorkflowSQL}}}
	tests := []struct {
		name  string
		state workflowSQLState
		run   func(WorkflowProcessStore) error
	}{
		{"update process exec", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowProcessStore) error { return store.UpdateProcess(t.Context(), "workspace", process) }},
		{"update process rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowProcessStore) error { return store.UpdateProcess(t.Context(), "workspace", process) }},
		{"list process query", queryError, func(store WorkflowProcessStore) error {
			_, err := store.ListProcesses(t.Context(), "workspace", workflowmodel.WorkflowProcessFilter{})
			return err
		}},
		{"list process scan", rowError, func(store WorkflowProcessStore) error {
			_, err := store.ListProcesses(t.Context(), "workspace", workflowmodel.WorkflowProcessFilter{})
			return err
		}},
		{"list process rows", terminalError, func(store WorkflowProcessStore) error {
			_, err := store.ListProcesses(t.Context(), "workspace", workflowmodel.WorkflowProcessFilter{})
			return err
		}},
		{"list node query", queryError, func(store WorkflowProcessStore) error {
			_, err := store.ListNodes(t.Context(), "workspace", process.ID)
			return err
		}},
		{"list node scan", rowError, func(store WorkflowProcessStore) error {
			_, err := store.ListNodes(t.Context(), "workspace", process.ID)
			return err
		}},
		{"list node rows", terminalError, func(store WorkflowProcessStore) error {
			_, err := store.ListNodes(t.Context(), "workspace", process.ID)
			return err
		}},
		{"update node rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowProcessStore) error { return store.UpdateNode(t.Context(), "workspace", node) }},
		{"list task query", queryError, func(store WorkflowProcessStore) error {
			_, err := store.ListTasks(t.Context(), "workspace", process.ID, "", "", 10)
			return err
		}},
		{"list task scan", rowError, func(store WorkflowProcessStore) error {
			_, err := store.ListTasks(t.Context(), "workspace", process.ID, "", "", 10)
			return err
		}},
		{"list task rows", terminalError, func(store WorkflowProcessStore) error {
			_, err := store.ListTasks(t.Context(), "workspace", process.ID, "", "", 10)
			return err
		}},
		{"update task rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowProcessStore) error { return store.UpdateTask(t.Context(), "workspace", task) }},
		{"decide exec", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowProcessStore) error {
			_, _, err := store.DecideTask(t.Context(), "workspace", task.ID, "user", "approve", "", "now")
			return err
		}},
		{"decide rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowProcessStore) error {
			_, _, err := store.DecideTask(t.Context(), "workspace", task.ID, "user", "approve", "", "now")
			return err
		}},
		{"list event query", queryError, func(store WorkflowProcessStore) error {
			_, err := store.ListEvents(t.Context(), "workspace", process.ID, 10)
			return err
		}},
		{"list event scan", rowError, func(store WorkflowProcessStore) error {
			_, err := store.ListEvents(t.Context(), "workspace", process.ID, 10)
			return err
		}},
		{"list event rows", terminalError, func(store WorkflowProcessStore) error {
			_, err := store.ListEvents(t.Context(), "workspace", process.ID, 10)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(workflowProcessFailureStore(t, &test.state)); err == nil {
				t.Fatal("expected SQL failure")
			}
		})
	}
}

func TestWorkflowProcessStatusFilterFinalConditions(t *testing.T) {
	store := workflowProcessFailureStore(t, &workflowSQLState{})
	for _, statuses := range [][]string{{"", "open"}, {""}} {
		if _, err := store.ListProcesses(t.Context(), "workspace", workflowmodel.WorkflowProcessFilter{Statuses: statuses}); err != nil {
			t.Fatalf("statuses=%v error=%v", statuses, err)
		}
	}
}

func TestWorkflowWorkerStoreSQLFailures(t *testing.T) {
	execution := workflowmodel.WorkflowExecution{ID: "execution"}
	task := workflowmodel.WorkflowTask{ID: "task"}
	queryError := workflowSQLState{querySteps: []workflowSQLQueryStep{{err: errWorkflowSQL}}}
	rowError := workflowSQLState{querySteps: []workflowSQLQueryStep{{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}}}}
	terminalError := workflowSQLState{querySteps: []workflowSQLQueryStep{{nextErr: errWorkflowSQL}}}
	tests := []struct {
		name  string
		state workflowSQLState
		run   func(WorkflowWorkerStore) error
	}{
		{"list execution query", queryError, func(store WorkflowWorkerStore) error {
			_, err := store.ListExecutions(t.Context(), "workspace", 10)
			return err
		}},
		{"list execution scan", rowError, func(store WorkflowWorkerStore) error {
			_, err := store.ListExecutions(t.Context(), "workspace", 10)
			return err
		}},
		{"list execution rows", terminalError, func(store WorkflowWorkerStore) error {
			_, err := store.ListExecutions(t.Context(), "workspace", 10)
			return err
		}},
		{"conditional exec", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowWorkerStore) error {
			_, err := store.UpdateExecutionWhere(t.Context(), "workspace", execution, nil)
			return err
		}},
		{"conditional rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowWorkerStore) error {
			_, err := store.UpdateExecutionWhere(t.Context(), "workspace", execution, nil)
			return err
		}},
		{"list task query", queryError, func(store WorkflowWorkerStore) error {
			_, err := store.ListTasks(t.Context(), "workspace", "", "", "", 10)
			return err
		}},
		{"list task scan", rowError, func(store WorkflowWorkerStore) error {
			_, err := store.ListTasks(t.Context(), "workspace", "", "", "", 10)
			return err
		}},
		{"list task rows", terminalError, func(store WorkflowWorkerStore) error {
			_, err := store.ListTasks(t.Context(), "workspace", "", "", "", 10)
			return err
		}},
		{"list event query", queryError, func(store WorkflowWorkerStore) error {
			_, err := store.ListProcessEvents(t.Context(), "workspace", "process", 10)
			return err
		}},
		{"list event scan", rowError, func(store WorkflowWorkerStore) error {
			_, err := store.ListProcessEvents(t.Context(), "workspace", "process", 10)
			return err
		}},
		{"list event rows", terminalError, func(store WorkflowWorkerStore) error {
			_, err := store.ListProcessEvents(t.Context(), "workspace", "process", 10)
			return err
		}},
		{"insert event", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowWorkerStore) error {
			return store.InsertProcessEvent(t.Context(), "workspace", workflowmodel.WorkflowProcessEvent{})
		}},
		{"update row exec", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowWorkerStore) error { return store.UpdateTask(t.Context(), "workspace", task) }},
		{"update row rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowWorkerStore) error { return store.UpdateTask(t.Context(), "workspace", task) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(workflowWorkerFailureStore(t, &test.state)); err == nil {
				t.Fatal("expected SQL failure")
			}
		})
	}
}

func TestWorkflowDecisionStoreTransactionFailures(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = base.Close() })
	tests := []struct {
		name  string
		state workflowSQLState
		run   func(WorkflowDecisionStore) error
	}{
		{"decision begin", workflowSQLState{beginErr: errWorkflowSQL}, func(store WorkflowDecisionStore) error {
			_, err := store.CommitWorkflowDecision(t.Context(), transactionmodel.WorkflowDecisionCommit{WorkspaceID: "workspace"})
			return err
		}},
		{"decision commit", workflowSQLState{commitErr: errWorkflowSQL, execSteps: []workflowSQLExecStep{{rows: 1}}}, func(store WorkflowDecisionStore) error {
			_, err := store.CommitWorkflowDecision(t.Context(), transactionmodel.WorkflowDecisionCommit{WorkspaceID: "workspace", DecidedTask: workflowmodel.WorkflowTask{ID: "task", AssigneeUserID: "user"}})
			return err
		}},
		{"state begin", workflowSQLState{beginErr: errWorkflowSQL}, func(store WorkflowDecisionStore) error {
			return store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "workspace"})
		}},
		{"state update exec", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDecisionStore) error {
			return store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "workspace", UpdateNodes: []workflowmodel.WorkflowNodeInstance{{ID: "node"}}})
		}},
		{"state update rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowDecisionStore) error {
			return store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "workspace", UpdateNodes: []workflowmodel.WorkflowNodeInstance{{ID: "node"}}})
		}},
		{"state insert node", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDecisionStore) error {
			return store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "workspace", InsertNodes: []workflowmodel.WorkflowNodeInstance{{ID: "node"}}})
		}},
		{"state insert agent task", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDecisionStore) error {
			return store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "workspace", InsertAgentTasks: []transactionmodel.WorkflowAgentTaskCommit{{RunID: "run"}}})
		}},
		{"state update agent task exec", workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}}, func(store WorkflowDecisionStore) error {
			return store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "workspace", UpdateAgentTasks: []transactionmodel.WorkflowAgentTaskCommit{{RunID: "run"}}})
		}},
		{"state update agent task rows", workflowSQLState{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}}, func(store WorkflowDecisionStore) error {
			return store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "workspace", UpdateAgentTasks: []transactionmodel.WorkflowAgentTaskCommit{{RunID: "run"}}})
		}},
		{"state insert execution encoding", workflowSQLState{}, func(store WorkflowDecisionStore) error {
			return store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "workspace", InsertExecutions: []workflowmodel.WorkflowExecution{{ID: "execution", Action: map[string]any{"bad": make(chan int)}}}})
		}},
		{"state commit", workflowSQLState{commitErr: errWorkflowSQL}, func(store WorkflowDecisionStore) error {
			return store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "workspace"})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openWorkflowScriptedDB(&test.state)
			t.Cleanup(func() { _ = db.Close() })
			store := NewWorkflowDecisionStore(base)
			store.db = db
			err := test.run(store)
			if test.name == "state insert execution encoding" {
				if err == nil {
					t.Fatal("expected encoding error")
				}
			} else if !errors.Is(err, errWorkflowSQL) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestWorkflowStateCommitSuccessfulOptionalWrites(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	db := openWorkflowScriptedDB(&workflowSQLState{})
	t.Cleanup(func() {
		_ = db.Close()
		_ = base.Close()
	})
	store := NewWorkflowDecisionStore(base)
	store.db = db
	execution := workflowmodel.WorkflowExecution{ID: "execution"}
	if err := store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{
		WorkspaceID:       "workspace",
		Events:            []workflowmodel.WorkflowProcessEvent{{ID: "event"}},
		WorkflowExecution: &execution,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowStateCommitAgentSchemaFailure(t *testing.T) {
	base := openStoreForGeneratedListTest(t)
	store := NewWorkflowDecisionStore(base)
	db := openWorkflowScriptedDB(&workflowSQLState{})
	defer db.Close()
	store.db = db
	if err := base.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "workspace", InsertAgentTasks: []transactionmodel.WorkflowAgentTaskCommit{{RunID: "run"}}}); err == nil {
		t.Fatal("closed Agent schema store accepted")
	}
}

func TestWorkflowProcessStoreLimitExtremes(t *testing.T) {
	taskStore := workflowProcessFailureStore(t, &workflowSQLState{})
	if _, err := taskStore.ListTasks(t.Context(), "workspace", "", "", "", 501); err != nil {
		t.Fatal(err)
	}
	eventStore := workflowProcessFailureStore(t, &workflowSQLState{})
	if _, err := eventStore.ListEvents(t.Context(), "workspace", "process", 0); err != nil {
		t.Fatal(err)
	}
}

type workflowExecutionScannerFunc func(...any) error

func (fn workflowExecutionScannerFunc) Scan(dest ...any) error { return fn(dest...) }

func TestScanWorkflowExecutionNormalizesNilJSONMaps(t *testing.T) {
	execution, err := scanWorkflowExecution(workflowExecutionScannerFunc(func(dest ...any) error {
		*dest[7].(*string), *dest[8].(*string), *dest[9].(*string) = "null", "null", "null"
		return nil
	}))
	if err != nil || execution.Action == nil || execution.Payload == nil || execution.Result == nil {
		t.Fatalf("execution=%#v err=%v", execution, err)
	}
}
