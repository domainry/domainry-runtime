package workflow

import (
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestTerminalWorkflowDecisionPropagatesNotificationCompilationAcrossAllCommitShapes(t *testing.T) {
	principal := workflowAdminPrincipal()
	request := workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}
	compileErr := errors.New("compile notification")
	compiler := func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, compileErr
	}
	run := func(t *testing.T, process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask, tasks []workflowmodel.WorkflowTask, identity identitysdk.Directory) {
		t.Helper()
		store := &workflowProcessStoreEdgeStub{
			workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: task.NodeID, Status: "waiting"}}}},
			getTaskValue:                 task, getTaskFound: true, tasks: tasks,
		}
		runtime := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, Decisions: &workflowStateDecisionEdgeStub{committed: true}, Identity: identity, Schema: workflowSchemaProviderEdgeStub{}, CompileNotification: compiler})
		if _, handled, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, task.ID, request, principal); !handled || !errors.Is(err, compileErr) {
			t.Fatalf("handled=%v err=%v", handled, err)
		}
	}

	t.Run("incomplete approval", func(t *testing.T) {
		process, task := workflowTerminalFixture("all", nil)
		other := task
		other.ID, other.AssigneeUserID = "other-task", "other"
		run(t, process, task, []workflowmodel.WorkflowTask{task, other}, nil)
	})
	t.Run("prepared next approval", func(t *testing.T) {
		contract := definitionmodel.WorkflowApprovalNodeContract{Mode: "any", EmptyAssigneePolicy: "fail", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"next-user"}}}}
		next := definitionmodel.WorkflowGraphNode{ID: "next", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &contract}}
		process, task := workflowTerminalFixture("any", &next)
		run(t, process, task, []workflowmodel.WorkflowTask{task}, workflowApprovalIdentityStub{users: map[string]identitysdk.User{"next-user": {ID: "next-user"}}})
	})
	t.Run("queued continuation", func(t *testing.T) {
		next := definitionmodel.WorkflowGraphNode{ID: "next", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "run"}}}
		process, task := workflowTerminalFixture("any", &next)
		run(t, process, task, []workflowmodel.WorkflowTask{task}, nil)
	})
	t.Run("terminal process", func(t *testing.T) {
		process, task := workflowTerminalFixture("any", nil)
		run(t, process, task, []workflowmodel.WorkflowTask{task}, nil)
	})
}
