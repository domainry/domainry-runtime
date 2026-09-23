package workflow

import (
	"context"
	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	agentsdkfixture "github.com/domainry/domainry-runtime/testsupport/agentsdkfixture"
	metadatamodulefixture "github.com/domainry/domainry-runtime/testsupport/metadatamodulefixture"

	"path/filepath"
	"testing"

	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/notificationsdkfixture"
)

func newAgentWorkflowDecisionStore(store *database.RuntimeStore) WorkflowDecisionStore {
	return NewWorkflowDecisionStore(store)
}

func TestContextWorkflowDecisionCommitIsAtomic(t *testing.T) {
	for _, test := range []struct {
		name      string
		duplicate bool
	}{
		{name: "commit"},
		{name: "event failure rolls back task process node and record", duplicate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, object, process, node, task := workflowDecisionStoreFixture(t)
			defer store.Close()
			processStore := NewWorkflowProcessStore(store)
			event := workflowmodel.WorkflowProcessEvent{WorkspaceID: "workspace-primary", ID: "decision_event", ProcessID: process.ID, NodeID: node.NodeID, TaskID: task.ID, Event: "task_approved", ActorID: "manager", Summary: "workflow.event.task.approved", CreatedAt: "v2"}
			if test.duplicate {
				if err := processStore.InsertEvent(t.Context(), "workspace-primary", event); err != nil {
					t.Fatal(err)
				}
			}
			decided := task
			decided.Status = "approved"
			decided.Decision = "approved"
			decided.CompletedBy = "manager"
			decided.CompletedAt = "v2"
			decided.UpdatedAt = "v2"
			completedNode := node
			completedNode.Status = "completed"
			completedNode.CompletedAt = "v2"
			completedProcess := process
			completedProcess.Status = "completed"
			completedProcess.CurrentNodeIDs = nil
			completedProcess.CompletedAt = "v2"
			completedProcess.UpdatedAt = "v2"
			commit := transactionmodel.WorkflowDecisionCommit{
				WorkspaceID: "workspace-primary",
				DecidedTask: decided, ExpectedTaskStatus: "open", ExpectedAssigneeID: "manager",
				Process: &completedProcess, UpdateNodes: []workflowmodel.WorkflowNodeInstance{completedNode}, Events: []workflowmodel.WorkflowProcessEvent{event},
				RecordMutations: []transactionmodel.RecordMutationCommit{{
					Operation: "update", Object: object, Record: recordmodel.Record{ID: "business_1", CreatedAt: "v1", UpdatedAt: "v2", Data: map[string]any{"status": "approved"}}, ExpectedUpdatedAt: "v1",
					Audit: &auditmodel.AuditEvent{ID: "decision_audit", Family: auditmodel.EventFamilyRuntimeWorkflow, Event: "workflow_task_decided", ObjectKey: object.Key, RecordID: "business_1", ActorID: "manager", CreatedAt: "v2"},
				}},
			}
			committed, err := newAgentWorkflowDecisionStore(store).CommitWorkflowDecision(t.Context(), commit)
			if test.duplicate {
				if err == nil || committed {
					t.Fatalf("expected event failure, committed=%v err=%v", committed, err)
				}
				assertWorkflowDecisionState(t, store, object, "open", "waiting", "waiting", "pending")
				assertWorkflowDecisionAuditCount(t, store, 0)
				return
			}
			if err != nil || !committed {
				t.Fatalf("commit decision: committed=%v err=%v", committed, err)
			}
			assertWorkflowDecisionState(t, store, object, "approved", "completed", "completed", "approved")
			assertWorkflowDecisionAuditCount(t, store, 1)
		})
	}
}

func TestWorkflowStateCommitRollsBackRecoveryWhenEventWriteFails(t *testing.T) {
	store, object, process, node, task := workflowDecisionStoreFixture(t)
	defer store.Close()
	event := workflowmodel.WorkflowProcessEvent{WorkspaceID: "workspace-primary", ID: "state_event", ProcessID: process.ID, NodeID: node.NodeID, TaskID: task.ID, Event: "process_failure_resolved", ActorID: "manager", CreatedAt: "v2"}
	if err := NewWorkflowProcessStore(store).InsertEvent(t.Context(), "workspace-primary", event); err != nil {
		t.Fatal(err)
	}
	process.Status, process.UpdatedAt = "resolved", "v2"
	node.Status, node.CompletedAt = "failed", "v2"
	task.Status, task.UpdatedAt = "cancelled", "v2"
	err := newAgentWorkflowDecisionStore(store).CommitWorkflowState(t.Context(), transactionmodel.WorkflowStateCommit{
		WorkspaceID: "workspace-primary",
		Process:     &process, UpdateNodes: []workflowmodel.WorkflowNodeInstance{node}, UpdateTasks: []workflowmodel.WorkflowTask{task}, Events: []workflowmodel.WorkflowProcessEvent{event},
	})
	if err == nil {
		t.Fatal("expected duplicate recovery event failure")
	}
	assertWorkflowDecisionState(t, store, object, "open", "waiting", "waiting", "pending")
}

func TestWorkflowStateCommitAtomicallyCreatesAgentNodeCorrelation(t *testing.T) {
	for _, duplicateEvent := range []bool{false, true} {
		store, _, process, _, _ := workflowDecisionStoreFixture(t)
		processStore := NewWorkflowProcessStore(store)
		event := workflowmodel.WorkflowProcessEvent{WorkspaceID: "workspace-primary", ID: "agent_waiting", ProcessID: process.ID, NodeID: "agent", Event: "agent_task_waiting", ActorID: "user", CreatedAt: "v2"}
		if duplicateEvent {
			if err := processStore.InsertEvent(t.Context(), "workspace-primary", event); err != nil {
				t.Fatal(err)
			}
		}
		commit := transactionmodel.WorkflowStateCommit{
			WorkspaceID: "workspace-primary",
			InsertNodes: []workflowmodel.WorkflowNodeInstance{{WorkspaceID: "workspace-primary", ID: "agent-node", ProcessID: process.ID, NodeID: "agent", NodeType: "agent_task", Iteration: 1, Status: "waiting", Input: map[string]any{"agent_task_run_id": "agent-run"}, Output: map[string]any{}, StartedAt: "v2"}},
			Events:      []workflowmodel.WorkflowProcessEvent{event},
		}
		err := newAgentWorkflowDecisionStore(store).CommitWorkflowState(t.Context(), commit)
		if duplicateEvent && err == nil {
			t.Fatal("expected duplicate event to roll back Agent node correlation")
		}
		if !duplicateEvent && err != nil {
			t.Fatal(err)
		}
		want := 1
		if duplicateEvent {
			want = 0
		}
		var count int
		query := "SELECT COUNT(*) FROM " + store.TableIdentifier("_workflow_node_instances") + " WHERE " + store.Identifier("id") + " = " + store.Placeholder(1)
		if err := store.DB().QueryRowContext(t.Context(), query, "agent-node").Scan(&count); err != nil || count != want {
			t.Fatalf("node count=%d want=%d err=%v", count, want, err)
		}
		store.Close()
	}
}

func assertWorkflowDecisionAuditCount(t *testing.T, store *database.RuntimeStore, want int) {
	t.Helper()
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _audit_events WHERE id = ?`, "decision_audit").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("decision audit count=%d want=%d", count, want)
	}
}

func openStoreForGeneratedListTest(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow.db")})
	if err != nil {
		t.Fatal(err)
	}
	metadatamodulefixture.EnsureBinding(t.Context(), store)
	binding, err := agentsdkfixture.Open(t.Context(), store, "workflow-decision-store-test")
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	return store
}

func TestContextWorkflowDecisionConflictDoesNotApplySideEffects(t *testing.T) {
	store, object, process, node, task := workflowDecisionStoreFixture(t)
	defer store.Close()
	decided := task
	decided.Status = "approved"
	decided.Decision = "approved"
	decided.CompletedBy = "other-user"
	decided.CompletedAt = "v2"
	decided.UpdatedAt = "v2"
	process.Status = "completed"
	committed, err := newAgentWorkflowDecisionStore(store).CommitWorkflowDecision(t.Context(), transactionmodel.WorkflowDecisionCommit{
		WorkspaceID: "workspace-primary",
		DecidedTask: decided, ExpectedTaskStatus: "open", ExpectedAssigneeID: "other-user", Process: &process,
		Events:          []workflowmodel.WorkflowProcessEvent{{WorkspaceID: "workspace-primary", ID: "must_not_exist", ProcessID: process.ID, Event: "task_approved", ActorID: "other-user", CreatedAt: "v2"}},
		RecordMutations: []transactionmodel.RecordMutationCommit{{Operation: "update", Object: object, Record: recordmodel.Record{ID: "business_1", CreatedAt: "v1", UpdatedAt: "v2", Data: map[string]any{"status": "approved"}}}},
	})
	if err != nil || committed {
		t.Fatalf("expected idempotency conflict without error, committed=%v err=%v", committed, err)
	}
	assertWorkflowDecisionState(t, store, object, "open", "waiting", node.Status, "pending")
}

func TestWorkflowDecisionNotificationEventCommitsAndRollsBackWithTask(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		name := "commit"
		if duplicate {
			name = "notification failure rolls back task"
		}
		t.Run(name, func(t *testing.T) {
			store, object, _, _, task := workflowDecisionStoreFixture(t)
			defer store.Close()
			event := workflowTaskNotificationEvent("notification-decision", "task-1:completed:v2")
			event.WorkspaceID = "workspace-primary"
			event.EventType, event.ActionState, event.Snapshot.Actions = "workflow.task.completed", "completed", nil
			if duplicate {
				writer := notificationpersistence.NewInboxEventWriter(store)
				if err := writer.InsertEventTx(t.Context(), store.DB(), event); err != nil {
					t.Fatal(err)
				}
			}
			decided := task
			decided.Status, decided.Decision, decided.CompletedBy, decided.CompletedAt, decided.UpdatedAt = "approved", "approved", "manager", "v2", "v2"
			committed, err := newAgentWorkflowDecisionStore(store).CommitWorkflowDecision(t.Context(), transactionmodel.WorkflowDecisionCommit{
				WorkspaceID: "workspace-primary", DecidedTask: decided, ExpectedTaskStatus: "open", ExpectedAssigneeID: "manager",
				NotificationEvents: []notificationmodel.NotificationEvent{event},
			})
			if duplicate {
				if err == nil || committed {
					t.Fatalf("expected notification failure, committed=%v err=%v", committed, err)
				}
				assertWorkflowDecisionState(t, store, object, "open", "waiting", "waiting", "pending")
				return
			}
			if err != nil || !committed {
				t.Fatalf("commit decision notification: committed=%v err=%v", committed, err)
			}
			var count int
			if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.TableIdentifier("_notification_events")+" WHERE "+store.Identifier("workspace_id")+" = ?", "workspace-primary").Scan(&count); err != nil || count != 1 {
				t.Fatalf("notification event count=%d err=%v", count, err)
			}
		})
	}
}

func workflowDecisionStoreFixture(t *testing.T) (*database.RuntimeStore, definitionmodel.ObjectSchema, workflowmodel.WorkflowProcessInstance, workflowmodel.WorkflowNodeInstance, workflowmodel.WorkflowTask) {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		store.Close()
		t.Fatal(err)
	}
	object := definitionmodel.ObjectSchema{Key: "decision_business", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}
	if _, err := store.DB().Exec(`CREATE TABLE decision_business (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, status TEXT, UNIQUE (workspace_id, id))`); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := recordpersistence.NewRecordStore(store).InsertRecord(t.Context(), "workspace-primary", object, recordmodel.Record{ID: "business_1", CreatedAt: "v1", UpdatedAt: "v1", Data: map[string]any{"status": "pending"}}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace-primary", ID: "process_1", WorkflowKey: "approval", WorkflowName: "Approval", DefinitionVersion: 1, DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "approval"}, InitiatorID: "requester", Status: "waiting", CurrentNodeIDs: []string{"approve"}, Variables: map[string]any{}, Result: map[string]any{}, CreatedAt: "v1", UpdatedAt: "v1"}
	node := workflowmodel.WorkflowNodeInstance{WorkspaceID: "workspace-primary", ID: "node_1", ProcessID: process.ID, NodeID: "approve", NodeType: "approval", Iteration: 1, Status: "waiting", Input: map[string]any{}, Output: map[string]any{}, StartedAt: "v1"}
	task := workflowmodel.WorkflowTask{WorkspaceID: "workspace-primary", ID: "task_1", ProcessID: process.ID, NodeInstanceID: node.ID, NodeID: node.NodeID, Title: "Approve", AssigneeUserID: "manager", Sequence: 1, Status: "open", CreatedAt: "v1", UpdatedAt: "v1"}
	processStore := NewWorkflowProcessStore(store)
	if err := processStore.InsertProcess(t.Context(), "workspace-primary", process); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := processStore.InsertNode(t.Context(), "workspace-primary", node); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := processStore.InsertTask(t.Context(), "workspace-primary", task); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store, object, process, node, task
}

func assertWorkflowDecisionState(t *testing.T, store *database.RuntimeStore, object definitionmodel.ObjectSchema, taskStatus, processStatus, nodeStatus, recordStatus string) {
	t.Helper()
	processStore := NewWorkflowProcessStore(store)
	task, ok, err := processStore.GetTask(t.Context(), "workspace-primary", "task_1")
	if err != nil || !ok || task.Status != taskStatus {
		t.Fatalf("task status=%q ok=%v err=%v, want %q", task.Status, ok, err, taskStatus)
	}
	process, ok, err := processStore.GetProcess(t.Context(), "workspace-primary", "process_1")
	if err != nil || !ok || process.Status != processStatus {
		t.Fatalf("process status=%q ok=%v err=%v, want %q", process.Status, ok, err, processStatus)
	}
	nodes, err := processStore.ListNodes(t.Context(), "workspace-primary", process.ID)
	if err != nil || len(nodes) != 1 || nodes[0].Status != nodeStatus {
		t.Fatalf("nodes=%#v err=%v, want status %q", nodes, err, nodeStatus)
	}
	record, ok, err := recordpersistence.NewRecordStore(store).GetRecord(t.Context(), "workspace-primary", object, "business_1")
	if err != nil || !ok || record.Data["status"] != recordStatus {
		t.Fatalf("record=%#v ok=%v err=%v, want status %q", record, ok, err, recordStatus)
	}
}
