package integrationtest

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"path/filepath"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"

	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkflowApprovalSkipsWhenResolverFindsNobody(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Key: "approval_skip", Name: "Approval Skip", Enabled: true, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "submitted", Type: "trigger", Name: "Submitted"},
			{ID: "manager", Type: "approval", Name: "Manager", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
				Mode: "any", ResolverMode: "first_match", EmptyAssigneePolicy: "skip",
				Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "manager_of", UserField: "employee_user"}},
			}}},
		},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "submitted-manager", Source: "submitted", Target: "manager"}},
	}}
	store, records := workflowPolicyTestRuntime(t, workflow)
	defer store.Close()
	process, err := runWorkflowProcess(t, store, records, workflow.Key, map[string]any{"employee_user": "employee_without_manager"}, workflowPolicyPrincipal("employee_without_manager"))
	if err != nil || process.Status != "completed" {
		t.Fatalf("expected skipped approval to complete, process=%#v err=%v", process, err)
	}
	tasks, _ := workflowProcessStore(store).ListTasks(t.Context(), "workspace-primary", process.ID, "", "", 10)
	if len(tasks) != 0 {
		t.Fatalf("skip policy must not forge tasks: %#v", tasks)
	}
	nodes, _ := workflowProcessStore(store).ListNodes(t.Context(), "workspace-primary", process.ID)
	managerSkipped := false
	for _, node := range nodes {
		if node.NodeID == "manager" && node.Status == "skipped" {
			managerSkipped = true
			break
		}
	}
	if len(nodes) != 2 || !managerSkipped {
		t.Fatalf("expected skipped node evidence, got %#v", nodes)
	}
}

func TestWorkflowApprovalDoesNotAssignInactiveManager(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Key: "inactive_manager", Name: "Inactive Manager", Enabled: true, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{{ID: "submitted", Type: "trigger", Name: "Submitted"}, {ID: "manager", Type: "approval", Name: "Manager", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "any", EmptyAssigneePolicy: "fail", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "manager_of", UserField: "employee_user"}}}}}},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "submitted-manager", Source: "submitted", Target: "manager"}},
	}}
	workflow.Trigger = map[string]any{"type": "manual"}
	workflow.TriggerContract = &definitionmodel.WorkflowTriggerContract{Type: "manual"}
	workflow.Action = map[string]any{"type": "workflow_graph"}
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "inactive-manager.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	identity := newIntegrationTestIdentityDirectory()
	managerID := "inactive_manager"
	identity.upsertUser(identitysdk.User{ID: "employee", Status: identitysdk.UserStatusActive})
	manager := identitysdk.User{ID: managerID, Status: "disabled"}
	identity.upsertUser(manager)
	mustUpsertWorkforceReportingLine(t, identity, managerID, "employee")
	records := newWorkflowProcessTestService(t, store, workflow, identity)
	process, startErr := runWorkflowProcess(t, store, records, workflow.Key, map[string]any{"employee_user": "employee"}, workflowPolicyPrincipal("employee"))
	if startErr != nil || process.ErrorCode != "backend.workflow.approval_assignee_not_found" || process.Status != "configuration_error" {
		t.Fatalf("inactive manager must not receive an approval task: process=%#v err=%v", process, startErr)
	}
	tasks, _ := workflowProcessStore(store).ListTasks(t.Context(), "workspace-primary", process.ID, "", "", 10)
	if len(tasks) != 0 {
		t.Fatalf("inactive manager received tasks: %#v", tasks)
	}
	manager.Status = identitysdk.UserStatusActive
	identity.upsertUser(manager)
	operator := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{"workflow.process.operate"}})
	retried, retryErr := records.Applications().Workflows.RetryWorkflowProcess(t.Context(), process.ID, operator)
	if retryErr != nil || retried.Status != "waiting" || workflowpolicy.WorkflowRetryCount(retried.Result["retry_count"]) != 1 {
		t.Fatalf("retry after fixing manager: process=%#v err=%v", retried, retryErr)
	}
	tasks, _ = workflowProcessStore(store).ListTasks(t.Context(), "workspace-primary", process.ID, managerID, "open", 10)
	if len(tasks) != 1 {
		t.Fatalf("retry did not resume only the failed approval node: %#v", tasks)
	}
}

func TestWorkflowActionContinuePolicyRecordsFailureAndContinues(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Key: "action_continue", Name: "Action Continue", Enabled: true, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "submitted", Type: "trigger", Name: "Submitted"},
			{ID: "notify", Type: "action", Name: "Notify", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{
				ActionKey: "missing.notification", ObjectKey: "leave_request", OnError: "continue",
			}}},
		},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "submitted-notify", Source: "submitted", Target: "notify"}},
	}}
	store, records := workflowPolicyTestRuntime(t, workflow)
	defer store.Close()
	process, err := runWorkflowProcess(t, store, records, workflow.Key, map[string]any{"object_key": "leave_request", "record_id": "leave_1"}, workflowPolicyPrincipal("employee"))
	if err != nil || process.Status != "completed" {
		t.Fatalf("expected continue policy to finish process, process=%#v err=%v", process, err)
	}
	nodes, _ := workflowProcessStore(store).ListNodes(t.Context(), "workspace-primary", process.ID)
	if len(nodes) != 2 || nodes[1].Status != "failed" || nodes[1].ErrorCode != "backend.action.not_found" {
		t.Fatalf("expected failed action evidence, got %#v", nodes)
	}
}

func workflowPolicyTestRuntime(t *testing.T, workflow definitionmodel.WorkflowSchema) (*persistence.RuntimeStore, *RuntimeServices) {
	t.Helper()
	if workflow.Trigger == nil {
		workflow.Trigger = map[string]any{"type": "manual"}
		workflow.TriggerContract = &definitionmodel.WorkflowTriggerContract{Type: "manual"}
	}
	if workflow.Action == nil {
		workflow.Action = map[string]any{"type": "workflow_graph"}
	}
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workflow-policy.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	identity := newIntegrationTestIdentityDirectory()
	identity.upsertUser(identitysdk.User{ID: "employee_without_manager", Status: identitysdk.UserStatusActive})
	return store, newWorkflowProcessTestService(t, store, workflow, identity)
}

func workflowPolicyPrincipal(userID string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: userID, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "employee", Permissions: []string{"workflow.run"}})
}
