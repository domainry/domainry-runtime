package integrationtest

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"path/filepath"
	"testing"

	schedulerprojection "github.com/domainry/domainry-runtime/runtime/domain/scheduler/projection"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"

	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkflowProcessWaitsForManagerAndResumesAfterRestart(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow-process.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	identityStore := newIntegrationTestIdentityDirectory()
	mustUpsertIdentityUser(t, identityStore, identitysdk.User{ID: "manager", Name: "Manager", Email: "manager@example.com"})
	mustUpsertIdentityUser(t, identityStore, identitysdk.User{ID: "employee", Name: "Employee", Email: "employee@example.com"})
	mustUpsertWorkforceReportingLine(t, identityStore, "manager", "employee")

	workflow := managerApprovalTestWorkflow()
	records := newWorkflowProcessTestService(t, store, workflow, identityStore)
	employee := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "employee", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "employee", Permissions: []string{"workflow.run"}})
	process, err := runWorkflowProcess(t, store, records, workflow.Key, map[string]any{"object_key": "leave_request", "record_id": "leave_1", "employee_user": "employee"}, employee)
	if err != nil {
		t.Fatalf("start process: %v", err)
	}
	if process.Status != "waiting" || len(process.CurrentNodeIDs) != 1 || process.CurrentNodeIDs[0] != "manager_approval" {
		t.Fatalf("expected waiting manager approval, got %#v", process)
	}
	tasks, err := workflowProcessStore(store).ListTasks(t.Context(), "workspace-primary", process.ID, "manager", "open", 10)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("expected manager task: tasks=%#v err=%v", tasks, err)
	}
	if tasks[0].AssigneeName != "Manager" || tasks[0].CandidateSource != "manager_of" || tasks[0].NodeDefinitionVersion != workflow.Graph.Version || len(tasks[0].ResolverSnapshot) != 1 {
		t.Fatalf("expected immutable assignee resolution evidence, got %#v", tasks[0])
	}

	modifiedWorkflow := managerApprovalTestWorkflow()
	modifiedWorkflow.Name = "Changed Definition"
	modifiedWorkflow.Graph.Nodes[2].Name = "Changed Notification"
	restarted := newWorkflowProcessTestService(t, store, modifiedWorkflow, identityStore)
	outsider := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "outsider", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "employee"})
	if _, err := restarted.Applications().Workflows.DecideTask(t.Context(), tasks[0].ID, workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, outsider); err == nil || serviceErrorCode(err) != "backend.workflow.task_assignee_required" {
		t.Fatalf("expected non-assignee denial, got %v", err)
	}
	assignedWithoutPermission := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "manager", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "restricted_manager"})
	if _, err := restarted.Applications().Workflows.DecideTask(t.Context(), tasks[0].ID, workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, assignedWithoutPermission); err == nil || serviceErrorCode(err) != "backend.workflow.task.act_permission_required" {
		t.Fatalf("expected task permission denial, got %v", err)
	}
	manager := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "manager", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "line_manager", Permissions: []string{"workflow.task.act"}})
	completed, err := restarted.Applications().Workflows.DecideTask(t.Context(), tasks[0].ID, workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved", Comment: "Approved"}, manager)
	if err != nil {
		t.Fatalf("approve after restart: %v", err)
	}
	if completed.Status != "completed" || completed.CompletedAt == "" {
		t.Fatalf("expected completed process, got %#v", completed)
	}
	if _, err := restarted.Applications().Workflows.DecideTask(t.Context(), tasks[0].ID, workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, manager); err == nil || serviceErrorCode(err) != "backend.workflow.task_already_decided" {
		t.Fatalf("expected duplicate decision conflict, got %v", err)
	}
	detail, err := restarted.Applications().Workflows.WorkflowProcess(t.Context(), process.ID, employee)
	if err != nil {
		t.Fatalf("process detail: %v", err)
	}
	if len(detail.Nodes) != 3 || len(detail.Tasks) != 1 || len(detail.Events) < 6 {
		t.Fatalf("expected persisted process evidence, got nodes=%d tasks=%d events=%d", len(detail.Nodes), len(detail.Tasks), len(detail.Events))
	}
	if detail.Process.DefinitionSnapshot.Name != "Leave Approval" || detail.Process.DefinitionSnapshot.Graph.Nodes[2].Name != "Notify Employee" {
		t.Fatalf("expected immutable definition snapshot, got %#v", detail.Process.DefinitionSnapshot)
	}
}

func TestWorkflowSimulationUsesProcessEngineAndResolvesManager(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow-simulation.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	identityStore := newIntegrationTestIdentityDirectory()
	mustUpsertIdentityUser(t, identityStore, identitysdk.User{ID: "manager", Name: "Manager", Email: "manager@example.com"})
	mustUpsertIdentityUser(t, identityStore, identitysdk.User{ID: "employee", Name: "Employee", Email: "employee@example.com"})
	mustUpsertWorkforceReportingLine(t, identityStore, "manager", "employee")
	workflow := managerApprovalTestWorkflow()
	records := newWorkflowProcessTestService(t, store, workflow, identityStore)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "employee", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"ops.workflow.simulate"}})
	result, err := records.Applications().Workflows.SimulateWorkflow(t.Context(), workflow.Key, map[string]any{"object_key": "leave_request", "record_id": "leave_1", "employee_user": "employee"}, principal)
	if err != nil {
		t.Fatalf("simulate Workflow: %v", err)
	}
	if !result.WouldExecute || len(result.Nodes) != 4 {
		t.Fatalf("expected graph node preview, got %#v", result)
	}
	if result.Nodes[1].NodeID != "manager_approval" || len(result.Nodes[1].ResolvedAssignees) != 1 || result.Nodes[1].ResolvedAssignees[0] != "manager" {
		t.Fatalf("expected resolved manager preview, got %#v", result.Nodes[1])
	}
	processes, err := listWorkflowProcesses(t, store, "", "", "", 10)
	if err != nil || len(processes) != 0 {
		t.Fatalf("simulation must not persist process state, processes=%#v err=%v", processes, err)
	}
}

func managerApprovalTestWorkflow() definitionmodel.WorkflowSchema {
	return definitionmodel.WorkflowSchema{Key: "leave_request_approval", Name: "Leave Approval", Enabled: true, Trigger: map[string]any{"type": "manual"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Condition: map[string]any{}, ConditionContract: &definitionmodel.WorkflowConditionContract{Type: "always"}, Action: map[string]any{"type": "workflow_graph"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "trigger", Type: "trigger", Name: "Submitted"},
			{ID: "manager_approval", Type: "approval", Name: "Manager Approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "any", ResolverMode: "first_match", EmptyAssigneePolicy: "fail", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "manager_of", UserField: "employee_user"}}}}},
			{ID: "notify", Type: "condition", Name: "Notify Employee", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}},
			{ID: "rejected", Type: "condition", Name: "Rejected", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}},
		},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "trigger-approval", Source: "trigger", Target: "manager_approval"}, {ID: "approval-notify", Source: "manager_approval", Target: "notify", Branch: "approved"}, {ID: "approval-rejected", Source: "manager_approval", Target: "rejected", Branch: "rejected"}},
	}}
}

func newWorkflowProcessTestService(t *testing.T, store *persistence.RuntimeStore, workflow definitionmodel.WorkflowSchema, identityStore identitysdk.Directory) *RuntimeServices {
	t.Helper()
	return runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{TemplateID: "workflow-process-test", TemplateVersion: "1", Name: "Workflow Process Test", Objects: recordtimerprojection.RecordTimerSystemObjects(), Actions: nil, Workflows: []definitionmodel.WorkflowSchema{workflow}, AutomationRules: nil, Dictionaries: nil, Integrations: connectormodel.IntegrationSchema{}, Reports: nil, Skills: nil, Agents: nil, Store: store, IdentityDirectory: identityStore, WorkflowProcesses: workflowpersistence.NewWorkflowProcessStore(store), WorkflowDecisions: workflowpersistence.NewWorkflowDecisionStore(store), WorkflowWorker: workflowpersistence.NewWorkflowWorkerStore(store)})
}
