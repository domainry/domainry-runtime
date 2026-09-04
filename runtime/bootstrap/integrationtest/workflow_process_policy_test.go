package integrationtest

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"path/filepath"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

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
	process, err := runWorkflowProcess(t, store, records, workflow.Key, map[string]any{"object_key": "leave_request", "record_id": "leave_1"}, workflowPolicyPrincipal("employee", workflow.Key))
	if err != nil || process.Status != "completed" {
		t.Fatalf("expected continue policy to finish process, process=%#v err=%v", process, err)
	}
	nodes, _ := workflowProcessStore(store).ListNodes(t.Context(), "workspace-primary", process.ID)
	if len(nodes) != 2 || nodes[1].Status != "failed" || nodes[1].ErrorCode != "backend.action.not_found" {
		t.Fatalf("expected failed action evidence, got %#v", nodes)
	}
}

func TestWorkflowManagerResolverUsesIdentityUserReportingLine(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Key: "manager_approval", Name: "Manager Approval", Enabled: true, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "submitted", Type: "trigger", Name: "Submitted"},
			{ID: "manager", Type: "approval", Name: "Manager Approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
				Mode: "any", ResolverMode: "first_match", EmptyAssigneePolicy: "fail",
				Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "manager_of", UserField: "employee_user"}},
			}}},
		},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "submitted-manager", Source: "submitted", Target: "manager"}},
	}}
	store, records, identity := workflowPolicyTestRuntimeWithIdentity(t, workflow)
	defer store.Close()
	mustUpsertIdentityUser(t, identity, identitysdk.User{ID: "line_manager"})
	mustUpsertIdentityUser(t, identity, identitysdk.User{ID: "employee", ManagerUserID: "line_manager", ReportingPath: "/line_manager/employee"})

	process, err := runWorkflowProcess(t, store, records, workflow.Key, map[string]any{"employee_user": "employee"}, workflowPolicyPrincipal("employee", workflow.Key))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := workflowProcessStore(store).ListTasks(t.Context(), "workspace-primary", process.ID, "line_manager", "open", 10)
	if err != nil || len(tasks) != 1 || tasks[0].AssigneeUserID != "line_manager" {
		t.Fatalf("expected reporting-line manager approval task, tasks=%#v err=%v", tasks, err)
	}
}

func workflowPolicyTestRuntime(t *testing.T, workflow definitionmodel.WorkflowSchema) (*persistence.RuntimeStore, *RuntimeServices) {
	t.Helper()
	store, services, _ := workflowPolicyTestRuntimeWithIdentity(t, workflow)
	return store, services
}

func workflowPolicyTestRuntimeWithIdentity(t *testing.T, workflow definitionmodel.WorkflowSchema) (*persistence.RuntimeStore, *RuntimeServices, *integrationTestIdentityProjection) {
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
	identity := newIntegrationTestIdentityProjection()
	identity.upsertUser(identitysdk.User{ID: "employee_without_manager", Status: identitysdk.UserStatusActive})
	return store, newWorkflowProcessTestService(t, store, workflow, identity), identity
}

func workflowPolicyPrincipal(userID, workflowKey string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: userID, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "employee", Permissions: []string{"workflow." + workflowKey + ".run"}})
}
