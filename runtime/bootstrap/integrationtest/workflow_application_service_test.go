package integrationtest

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"path/filepath"
	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkflowApplicationServiceAtomicallyRejectsTerminalApproval(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow-decision.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	graph := &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}, {ID: "approve", Type: "approval"}}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "to-approve", Source: "start", Target: "approve"}}}
	workflow := definitionmodel.WorkflowSchema{Key: "terminal_approval", Name: "Terminal Approval", Enabled: true, Graph: graph}
	role := accessfixture.Bundle{Key: "manager", Permissions: integrationWorkflowTaskDecisionPermissions()}
	records := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{ProjectKey: "workflow-decision", SchemaVersion: "1", Name: "Workflow Decision", Objects: nil, Actions: nil, Workflows: []definitionmodel.WorkflowSchema{workflow}, AutomationRules: nil, Dictionaries: nil, Integrations: connectormodel.IntegrationSchema{}, Reports: nil, Skills: nil, Agents: nil, Store: store, WorkflowProcesses: workflowpersistence.NewWorkflowProcessStore(store), WorkflowDecisions: workflowpersistence.NewWorkflowDecisionStore(store), WorkflowWorker: workflowpersistence.NewWorkflowWorkerStore(store)})
	process := workflowmodel.WorkflowProcessInstance{ID: "process_terminal", WorkflowKey: workflow.Key, WorkflowName: workflow.Name, DefinitionVersion: 1, DefinitionSnapshot: workflow, InitiatorID: "requester", Status: "waiting", CurrentNodeIDs: []string{"approve"}, Variables: map[string]any{}, Result: map[string]any{}, CreatedAt: "v1", UpdatedAt: "v1"}
	node := workflowmodel.WorkflowNodeInstance{ID: "node_terminal", ProcessID: process.ID, NodeID: "approve", NodeType: "approval", Iteration: 1, Status: "waiting", Input: map[string]any{}, Output: map[string]any{}, StartedAt: "v1"}
	task := workflowmodel.WorkflowTask{ID: "task_terminal", ProcessID: process.ID, NodeInstanceID: node.ID, NodeID: node.NodeID, Title: "Approve", AssigneeUserID: "manager", Sequence: 1, Status: "open", CreatedAt: "v1", UpdatedAt: "v1"}
	execution := workflowmodel.WorkflowExecution{ID: process.ID, WorkflowKey: workflow.Key, Name: workflow.Name, Trigger: "manual", Status: "waiting", ActionType: "workflow_graph", Action: map[string]any{}, Payload: map[string]any{}, Result: map[string]any{}, ProcessID: process.ID, ActorID: "requester", Attempt: 1, MaxAttempts: 3, Message: "waiting", CreatedAt: "v1", UpdatedAt: "v1"}
	for _, insert := range []func() error{
		func() error {
			return workflowProcessStore(store).InsertProcess(t.Context(), "workspace-primary", process)
		},
		func() error { return workflowProcessStore(store).InsertNode(t.Context(), "workspace-primary", node) },
		func() error { return workflowProcessStore(store).InsertTask(t.Context(), "workspace-primary", task) },
		func() error {
			return workflowWorkerStore(store).InsertExecution(t.Context(), "workspace-primary", execution)
		},
	} {
		if err := insert(); err != nil {
			t.Fatal(err)
		}
	}

	result, err := records.Applications().Workflows.DecideTask(t.Context(), task.ID, workflowmodel.WorkflowTaskDecisionRequest{Decision: "rejected", Comment: "Not approved"}, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "manager", WorkspaceID: "workspace-primary"}}, role))
	if err != nil {
		t.Fatalf("reject task: %v", err)
	}
	if result.Status != "rejected" {
		t.Fatalf("expected rejected process, got %#v", result)
	}
	persistedExecution, ok, err := workflowWorkerStore(store).GetExecution(t.Context(), "workspace-primary", process.ID)
	if err != nil || !ok || persistedExecution.Status != "rejected" {
		t.Fatalf("execution not committed with decision: %#v ok=%v err=%v", persistedExecution, ok, err)
	}
	events, err := workflowProcessStore(store).ListEvents(t.Context(), "workspace-primary", process.ID, 20)
	if err != nil || len(events) != 3 {
		t.Fatalf("expected task, approval, and process events in commit, events=%#v err=%v", events, err)
	}
}

func TestWorkflowDecisionQueuesAndWorkerResumesGraphContinuation(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow-continuation.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	graph := &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}, {ID: "approve", Type: "approval"}, {ID: "finish", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}}}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "to-approve", Source: "start", Target: "approve"}, {ID: "to-finish", Source: "approve", Target: "finish", Branch: "approved"}}}
	workflow := definitionmodel.WorkflowSchema{Key: "continued_approval", Name: "Continued Approval", Enabled: true, Graph: graph}
	role := accessfixture.Bundle{Key: "manager", Permissions: integrationWorkflowTaskDecisionPermissions()}
	records := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{ProjectKey: "workflow-continuation", SchemaVersion: "1", Name: "Workflow Continuation", Objects: nil, Actions: nil, Workflows: []definitionmodel.WorkflowSchema{workflow}, AutomationRules: nil, Dictionaries: nil, Integrations: connectormodel.IntegrationSchema{}, Reports: nil, Skills: nil, Agents: nil, Store: store, WorkflowProcesses: workflowpersistence.NewWorkflowProcessStore(store), WorkflowDecisions: workflowpersistence.NewWorkflowDecisionStore(store), WorkflowWorker: workflowpersistence.NewWorkflowWorkerStore(store)})
	process := workflowmodel.WorkflowProcessInstance{ID: "process_continued", WorkflowKey: workflow.Key, WorkflowName: workflow.Name, DefinitionVersion: 1, DefinitionSnapshot: workflow, InitiatorID: "requester", Status: "waiting", CurrentNodeIDs: []string{"approve"}, Variables: map[string]any{}, Result: map[string]any{}, CreatedAt: "v1", UpdatedAt: "v1"}
	node := workflowmodel.WorkflowNodeInstance{ID: "node_continued", ProcessID: process.ID, NodeID: "approve", NodeType: "approval", Iteration: 1, Status: "waiting", Input: map[string]any{}, Output: map[string]any{}, StartedAt: "v1"}
	task := workflowmodel.WorkflowTask{ID: "task_continued", ProcessID: process.ID, NodeInstanceID: node.ID, NodeID: node.NodeID, Title: "Approve", AssigneeUserID: "manager", Sequence: 1, Status: "open", CreatedAt: "v1", UpdatedAt: "v1"}
	for _, insert := range []func() error{func() error {
		return workflowProcessStore(store).InsertProcess(t.Context(), "workspace-primary", process)
	}, func() error { return workflowProcessStore(store).InsertNode(t.Context(), "workspace-primary", node) }, func() error { return workflowProcessStore(store).InsertTask(t.Context(), "workspace-primary", task) }} {
		if err := insert(); err != nil {
			t.Fatal(err)
		}
	}
	completed, err := records.Applications().Workflows.DecideTask(t.Context(), task.ID, workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "manager", WorkspaceID: "workspace-primary"}}, role))
	if err != nil || completed.Status != "completed" {
		t.Fatalf("expected request-owned continuation to complete, process=%#v err=%v", completed, err)
	}
	intent, ok, err := workflowWorkerStore(store).GetExecution(t.Context(), "workspace-primary", process.ID)
	if err != nil || !ok || intent.Status != "completed" || intent.Result["resume_process_id"] != process.ID {
		t.Fatalf("expected committed and claimed continuation intent, execution=%#v ok=%v err=%v", intent, ok, err)
	}
}

func TestWorkflowDecisionCreatesNextApprovalTasksInSameCommit(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow-next-approval.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	graph := &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}, {ID: "first", Type: "approval"}, {ID: "second", Type: "approval", Name: "Second approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"manager_2"}}}}}}}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "to-first", Source: "start", Target: "first"}, {ID: "to-second", Source: "first", Target: "second", Branch: "approved"}}}
	workflow := definitionmodel.WorkflowSchema{Key: "two_approvals", Name: "Two approvals", Enabled: true, Graph: graph}
	role := accessfixture.Bundle{Key: "manager", Permissions: integrationWorkflowTaskDecisionPermissions()}
	identity := newIntegrationTestIdentityProjection()
	mustUpsertIdentityUser(t, identity, identitysdk.User{ID: "manager_2", Name: "Manager Two", Status: identitysdk.UserStatusActive})
	records := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{ProjectKey: "two-approvals", SchemaVersion: "1", Name: "Two Approvals", Objects: nil, Actions: nil, Workflows: []definitionmodel.WorkflowSchema{workflow}, AutomationRules: nil, Dictionaries: nil, Integrations: connectormodel.IntegrationSchema{}, Reports: nil, Skills: nil, Agents: nil, Store: store, WorkflowProcesses: workflowpersistence.NewWorkflowProcessStore(store), WorkflowDecisions: workflowpersistence.NewWorkflowDecisionStore(store), WorkflowWorker: workflowpersistence.NewWorkflowWorkerStore(store), IdentityProjection: identity})
	process := workflowmodel.WorkflowProcessInstance{ID: "process_two", WorkflowKey: workflow.Key, WorkflowName: workflow.Name, DefinitionVersion: 1, DefinitionSnapshot: workflow, InitiatorID: "requester", Status: "waiting", CurrentNodeIDs: []string{"first"}, Variables: map[string]any{}, Result: map[string]any{}, CreatedAt: "v1", UpdatedAt: "v1"}
	node := workflowmodel.WorkflowNodeInstance{ID: "node_first", ProcessID: process.ID, NodeID: "first", NodeType: "approval", Iteration: 1, Status: "waiting", Input: map[string]any{}, Output: map[string]any{}, StartedAt: "v1"}
	task := workflowmodel.WorkflowTask{ID: "task_first", ProcessID: process.ID, NodeInstanceID: node.ID, NodeID: node.NodeID, Title: "First", AssigneeUserID: "manager_1", Sequence: 1, Status: "open", CreatedAt: "v1", UpdatedAt: "v1"}
	for _, insert := range []func() error{func() error {
		return workflowProcessStore(store).InsertProcess(t.Context(), "workspace-primary", process)
	}, func() error { return workflowProcessStore(store).InsertNode(t.Context(), "workspace-primary", node) }, func() error { return workflowProcessStore(store).InsertTask(t.Context(), "workspace-primary", task) }} {
		if err := insert(); err != nil {
			t.Fatal(err)
		}
	}
	result, err := records.Applications().Workflows.DecideTask(t.Context(), task.ID, workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "manager_1", WorkspaceID: "workspace-primary"}}, role))
	if err != nil || result.Status != "waiting" || len(result.CurrentNodeIDs) != 1 || result.CurrentNodeIDs[0] != "second" {
		t.Fatalf("expected next approval waiting in committed process, result=%#v err=%v", result, err)
	}
	tasks, err := workflowProcessStore(store).ListTasks(t.Context(), "workspace-primary", process.ID, "manager_2", "open", 10)
	if err != nil || len(tasks) != 1 || tasks[0].NodeID != "second" {
		t.Fatalf("expected next approval task in same commit, tasks=%#v err=%v", tasks, err)
	}
}
