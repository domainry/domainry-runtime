package integrationtest

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"errors"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"path/filepath"
	"sort"
	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkflowApprovalModesAggregateTasks(t *testing.T) {
	tests := []struct {
		name             string
		mode             string
		firstDecision    string
		wantAfterFirst   string
		wantSecondStatus string
		wantFinal        string
	}{
		{name: "any approves and cancels remaining", mode: "any", firstDecision: "approved", wantAfterFirst: "completed", wantSecondStatus: "cancelled", wantFinal: "completed"},
		{name: "all waits for every approval", mode: "all", firstDecision: "approved", wantAfterFirst: "waiting", wantSecondStatus: "open", wantFinal: "completed"},
		{name: "sequential opens next task", mode: "sequential", firstDecision: "approved", wantAfterFirst: "waiting", wantSecondStatus: "open", wantFinal: "completed"},
		{name: "rejection ends approval", mode: "all", firstDecision: "rejected", wantAfterFirst: "rejected", wantSecondStatus: "cancelled", wantFinal: "rejected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, records := approvalModeTestRuntime(t, test.mode)
			defer store.Close()
			initiator := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "initiator", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "employee", Permissions: []string{"workflow.run"}})
			process, err := runWorkflowProcess(t, store, records, "approval_modes", map[string]any{"record_id": "request_1"}, initiator)
			if err != nil {
				t.Fatalf("start: %v", err)
			}
			tasks, err := workflowProcessStore(store).ListTasks(t.Context(), "workspace-primary", process.ID, "", "", 10)
			if err != nil || len(tasks) != 2 {
				t.Fatalf("tasks=%#v err=%v", tasks, err)
			}
			sort.Slice(tasks, func(i, j int) bool { return tasks[i].Sequence < tasks[j].Sequence })
			if test.mode == "sequential" && (tasks[0].Status != "open" || tasks[1].Status != "pending") {
				t.Fatalf("expected sequential open/pending tasks, got %#v", tasks)
			}
			first := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: tasks[0].AssigneeUserID, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "approver", Permissions: integrationWorkflowTaskDecisionPermissions()})
			application := records.Applications().Workflows
			afterFirst, err := application.DecideTask(t.Context(), tasks[0].ID, workflowmodel.WorkflowTaskDecisionRequest{Decision: test.firstDecision}, first)
			if err != nil {
				t.Fatalf("first decision: %v (cause: %v)", err, errors.Unwrap(err))
			}
			if afterFirst.Status != test.wantAfterFirst {
				t.Fatalf("expected %s after first decision, got %#v", test.wantAfterFirst, afterFirst)
			}
			second, _, err := workflowProcessStore(store).GetTask(t.Context(), "workspace-primary", tasks[1].ID)
			if err != nil || second.Status != test.wantSecondStatus {
				t.Fatalf("expected second task %s, got %#v err=%v", test.wantSecondStatus, second, err)
			}
			if test.wantAfterFirst != "waiting" {
				return
			}
			secondPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: second.AssigneeUserID, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Key: "approver", Permissions: integrationWorkflowTaskDecisionPermissions()})
			final, err := application.DecideTask(t.Context(), second.ID, workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, secondPrincipal)
			if err != nil || final.Status != test.wantFinal {
				t.Fatalf("expected final %s, got %#v err=%v cause=%v", test.wantFinal, final, err, errors.Unwrap(err))
			}
		})
	}
}

func approvalModeTestRuntime(t *testing.T, mode string) (*persistence.RuntimeStore, *RuntimeServices) {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "approval-mode.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	workflow := definitionmodel.WorkflowSchema{Key: "approval_modes", Name: "Approval Modes", Enabled: true, Trigger: map[string]any{"type": "manual"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Condition: map[string]any{}, ConditionContract: &definitionmodel.WorkflowConditionContract{Type: "always"}, Action: map[string]any{"type": "workflow_graph"}, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger", Name: "Trigger"}, {ID: "approval", Type: "approval", Name: "Approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: mode, ResolverMode: "union", EmptyAssigneePolicy: "fail", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"approver_a", "approver_b"}}}}}}},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "trigger-approval", Source: "trigger", Target: "approval"}},
	}}
	identity := newIntegrationTestIdentityDirectory()
	for _, userID := range []string{"approver_a", "approver_b"} {
		identity.upsertUser(identitysdk.User{ID: userID, Status: identitysdk.UserStatusActive})
	}
	records := newWorkflowProcessTestService(t, store, workflow, identity)
	return store, records
}
