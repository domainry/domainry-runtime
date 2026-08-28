package workflow

import (
	"context"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func workflowDecisionEdgeTask(task workflowmodel.WorkflowTask) workflowmodel.WorkflowTask {
	task.Status = "approved"
	task.Decision = "approved"
	task.CompletedBy = "manager"
	task.CompletedAt = "v2"
	task.UpdatedAt = "v2"
	return task
}

func workflowDecisionEdgeCommit(task workflowmodel.WorkflowTask) transactionmodel.WorkflowDecisionCommit {
	return transactionmodel.WorkflowDecisionCommit{
		WorkspaceID: "default", DecidedTask: workflowDecisionEdgeTask(task), ExpectedTaskStatus: "open", ExpectedAssigneeID: "manager",
	}
}

func TestWorkflowDecisionRejectsWorkspaceBeginAndDecisionFailures(t *testing.T) {
	store, _, _, _, task := workflowDecisionStoreFixture(t)
	decisionStore := NewWorkflowDecisionStore(store)
	if committed, err := decisionStore.CommitWorkflowDecision(t.Context(), transactionmodel.WorkflowDecisionCommit{WorkspaceID: " "}); err == nil || committed {
		t.Fatalf("blank decision committed=%v error=%v", committed, err)
	}
	if err := decisionStore.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: " "}); err == nil {
		t.Fatal("blank state workspace accepted")
	}
	if _, err := store.DB().ExecContext(t.Context(), `DROP TABLE workflow_tasks`); err != nil {
		t.Fatal(err)
	}
	if committed, err := decisionStore.CommitWorkflowDecision(t.Context(), workflowDecisionEdgeCommit(task)); err == nil || committed {
		t.Fatalf("missing task table committed=%v error=%v", committed, err)
	}
	_ = store.Close()
	if committed, err := decisionStore.CommitWorkflowDecision(t.Context(), workflowDecisionEdgeCommit(task)); err == nil || committed {
		t.Fatalf("closed decision committed=%v error=%v", committed, err)
	}
	if err := decisionStore.CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "default"}); err == nil {
		t.Fatal("closed state transaction began")
	}
}

func TestWorkflowDecisionRollsBackEachTransactionalWriteFailure(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*testing.T, *workflowDecisionEdgeFixture, *transactionmodel.WorkflowDecisionCommit)
	}{
		{name: "record mutation", configure: func(_ *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			commit.RecordMutations = []transactionmodel.RecordMutationCommit{{Operation: "unsupported", Object: fixture.object, Record: recordmodel.Record{ID: "business_1"}}}
		}},
		{name: "insert node", configure: func(_ *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			commit.InsertNodes = []workflowmodel.WorkflowNodeInstance{fixture.node}
		}},
		{name: "update node", configure: func(_ *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			node := fixture.node
			node.ID = "missing-node"
			commit.UpdateNodes = []workflowmodel.WorkflowNodeInstance{node}
		}},
		{name: "insert task", configure: func(_ *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			commit.InsertTasks = []workflowmodel.WorkflowTask{fixture.task}
		}},
		{name: "update task", configure: func(_ *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			task := fixture.task
			task.ID = "missing-task"
			commit.UpdateTasks = []workflowmodel.WorkflowTask{task}
		}},
		{name: "update process", configure: func(_ *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			process := fixture.process
			process.ID = "missing-process"
			commit.Process = &process
		}},
		{name: "insert event", configure: func(t *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			event := workflowmodel.WorkflowProcessEvent{ID: "duplicate-event", ProcessID: fixture.process.ID, Event: "decided", CreatedAt: "v2"}
			if err := NewWorkflowProcessStore(fixture.store).InsertEvent(t.Context(), "default", event); err != nil {
				t.Fatal(err)
			}
			commit.Events = []workflowmodel.WorkflowProcessEvent{event}
		}},
		{name: "insert execution encoding", configure: func(_ *testing.T, _ *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			commit.InsertExecutions = []workflowmodel.WorkflowExecution{{ID: "execution", Action: map[string]any{"invalid": make(chan int)}}}
		}},
		{name: "insert execution duplicate", configure: func(t *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			execution := workflowWorkerEdgeExecution("execution-duplicate")
			if err := NewWorkflowWorkerStore(fixture.store).InsertExecution(t.Context(), "default", execution); err != nil {
				t.Fatal(err)
			}
			commit.InsertExecutions = []workflowmodel.WorkflowExecution{execution}
		}},
		{name: "update execution encoding", configure: func(_ *testing.T, _ *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			execution := workflowWorkerEdgeExecution("execution")
			execution.Result = map[string]any{"invalid": make(chan int)}
			commit.WorkflowExecution = &execution
		}},
		{name: "update execution missing", configure: func(_ *testing.T, _ *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowDecisionCommit) {
			execution := workflowWorkerEdgeExecution("missing-execution")
			commit.WorkflowExecution = &execution
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowDecisionEdgeFixture(t)
			commit := workflowDecisionEdgeCommit(fixture.task)
			test.configure(t, fixture, &commit)
			committed, err := NewWorkflowDecisionStore(fixture.store).CommitWorkflowDecision(t.Context(), commit)
			if err == nil || committed {
				t.Fatalf("committed=%v error=%v", committed, err)
			}
			var status string
			if err := fixture.store.DB().QueryRowContext(t.Context(), `SELECT status FROM workflow_tasks WHERE workspace_id = ? AND id = ?`, "default", fixture.task.ID).Scan(&status); err != nil || status != "open" {
				t.Fatalf("rolled back task status=%q error=%v", status, err)
			}
		})
	}
}

type workflowDecisionEdgeFixture struct {
	store   *database.RuntimeStore
	object  definitionmodel.ObjectSchema
	process workflowmodel.WorkflowProcessInstance
	node    workflowmodel.WorkflowNodeInstance
	task    workflowmodel.WorkflowTask
}

func newWorkflowDecisionEdgeFixture(t *testing.T) *workflowDecisionEdgeFixture {
	t.Helper()
	store, object, process, node, task := workflowDecisionStoreFixture(t)
	t.Cleanup(func() { _ = store.Close() })
	return &workflowDecisionEdgeFixture{store: store, object: object, process: process, node: node, task: task}
}

func TestWorkflowStateRollsBackEachTransactionalWriteFailure(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*testing.T, *workflowDecisionEdgeFixture, *transactionmodel.WorkflowStateCommit)
	}{
		{name: "update node", configure: func(_ *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowStateCommit) {
			node := fixture.node
			node.ID = "missing-node"
			commit.UpdateNodes = []workflowmodel.WorkflowNodeInstance{node}
		}},
		{name: "update task", configure: func(_ *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowStateCommit) {
			task := fixture.task
			task.ID = "missing-task"
			commit.UpdateTasks = []workflowmodel.WorkflowTask{task}
		}},
		{name: "update process", configure: func(_ *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowStateCommit) {
			process := fixture.process
			process.ID = "missing-process"
			commit.Process = &process
		}},
		{name: "insert event", configure: func(t *testing.T, fixture *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowStateCommit) {
			event := workflowmodel.WorkflowProcessEvent{ID: "duplicate-state-event", ProcessID: fixture.process.ID, Event: "state", CreatedAt: "v2"}
			if err := NewWorkflowProcessStore(fixture.store).InsertEvent(t.Context(), "default", event); err != nil {
				t.Fatal(err)
			}
			commit.Events = []workflowmodel.WorkflowProcessEvent{event}
		}},
		{name: "update execution encoding", configure: func(_ *testing.T, _ *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowStateCommit) {
			execution := workflowWorkerEdgeExecution("execution")
			execution.Action = map[string]any{"invalid": make(chan int)}
			commit.WorkflowExecution = &execution
		}},
		{name: "update execution missing", configure: func(_ *testing.T, _ *workflowDecisionEdgeFixture, commit *transactionmodel.WorkflowStateCommit) {
			execution := workflowWorkerEdgeExecution("missing-execution")
			commit.WorkflowExecution = &execution
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkflowDecisionEdgeFixture(t)
			commit := transactionmodel.WorkflowStateCommit{WorkspaceID: "default"}
			test.configure(t, fixture, &commit)
			if err := NewWorkflowDecisionStore(fixture.store).CommitWorkflowState(t.Context(), commit); err == nil {
				t.Fatal("state failure committed")
			}
		})
	}
}

func TestWorkflowDecisionSkipsDuplicateDecidedTaskUpdate(t *testing.T) {
	fixture := newWorkflowDecisionEdgeFixture(t)
	commit := workflowDecisionEdgeCommit(fixture.task)
	commit.UpdateTasks = []workflowmodel.WorkflowTask{workflowDecisionEdgeTask(fixture.task)}
	committed, err := NewWorkflowDecisionStore(fixture.store).CommitWorkflowDecision(t.Context(), commit)
	if err != nil || !committed {
		t.Fatalf("committed=%v error=%v", committed, err)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	commit = workflowDecisionEdgeCommit(fixture.task)
	if committed, err := NewWorkflowDecisionStore(fixture.store).CommitWorkflowDecision(cancelled, commit); err == nil || committed {
		t.Fatalf("cancelled committed=%v error=%v", committed, err)
	}
}

func TestWorkflowDecisionCommitsAllOptionalWorkflowWrites(t *testing.T) {
	fixture := newWorkflowDecisionEdgeFixture(t)
	processStore := NewWorkflowProcessStore(fixture.store)
	secondTask := fixture.task
	secondTask.ID = "task-second"
	secondTask.Sequence = 2
	if err := processStore.InsertTask(t.Context(), "default", secondTask); err != nil {
		t.Fatal(err)
	}
	existingExecution := workflowWorkerEdgeExecution("execution-existing")
	if err := NewWorkflowWorkerStore(fixture.store).InsertExecution(t.Context(), "default", existingExecution); err != nil {
		t.Fatal(err)
	}

	updatedNode := fixture.node
	updatedNode.Status = "completed"
	updatedNode.CompletedAt = "v2"
	insertedNode := fixture.node
	insertedNode.ID = "node-inserted"
	insertedNode.NodeID = "archive"
	insertedNode.Status = "waiting"
	updatedSecondTask := secondTask
	updatedSecondTask.Status = "cancelled"
	insertedTask := fixture.task
	insertedTask.ID = "task-inserted"
	insertedTask.NodeInstanceID = insertedNode.ID
	insertedTask.NodeID = insertedNode.NodeID
	insertedTask.Sequence = 3
	updatedProcess := fixture.process
	updatedProcess.Status = "waiting"
	updatedProcess.UpdatedAt = "v2"
	existingExecution.Status = "succeeded"
	existingExecution.UpdatedAt = "v2"
	commit := workflowDecisionEdgeCommit(fixture.task)
	commit.ExpectedTaskStatus = ""
	commit.InsertNodes = []workflowmodel.WorkflowNodeInstance{insertedNode}
	commit.UpdateNodes = []workflowmodel.WorkflowNodeInstance{updatedNode}
	commit.InsertTasks = []workflowmodel.WorkflowTask{insertedTask}
	commit.UpdateTasks = []workflowmodel.WorkflowTask{updatedSecondTask}
	commit.Process = &updatedProcess
	commit.Events = []workflowmodel.WorkflowProcessEvent{{ID: "event-optional", ProcessID: fixture.process.ID, Event: "optional", CreatedAt: "v2"}}
	commit.InsertExecutions = []workflowmodel.WorkflowExecution{workflowWorkerEdgeExecution("execution-inserted")}
	commit.WorkflowExecution = &existingExecution
	committed, err := NewWorkflowDecisionStore(fixture.store).CommitWorkflowDecision(t.Context(), commit)
	if err != nil || !committed {
		t.Fatalf("committed=%v error=%v", committed, err)
	}

	if err := NewWorkflowDecisionStore(fixture.store).CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{WorkspaceID: "default"}); err != nil {
		t.Fatalf("empty state commit=%v", err)
	}
}
