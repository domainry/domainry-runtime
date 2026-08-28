package workflow

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type workflowDeadlineWorkerEdgeStub struct {
	workflowcontract.WorkflowWorkerStore
	events   []workflowmodel.WorkflowProcessEvent
	eventErr error
}

func (s *workflowDeadlineWorkerEdgeStub) InsertProcessEvent(_ context.Context, _ string, event workflowmodel.WorkflowProcessEvent) error {
	s.events = append(s.events, event)
	return s.eventErr
}

type workflowNotificationProcessEdgeStub struct {
	workflowExecutionProcessStub
	events        []workflowmodel.WorkflowProcessEvent
	eventErr      error
	insertNodeErr error
}

func (s *workflowNotificationProcessEdgeStub) InsertEvent(_ context.Context, _ string, event workflowmodel.WorkflowProcessEvent) error {
	s.events = append(s.events, event)
	return s.eventErr
}

func (s *workflowNotificationProcessEdgeStub) InsertNode(ctx context.Context, workspaceID string, node workflowmodel.WorkflowNodeInstance) error {
	if s.insertNodeErr != nil {
		return s.insertNodeErr
	}
	return s.workflowExecutionProcessStub.InsertNode(ctx, workspaceID, node)
}

type workflowIdentityEdgeStub struct {
	workflowDirectoryTestStub
	user  identitysdk.User
	found bool
	err   error
}

func (s workflowIdentityEdgeStub) FindUser(context.Context, identitysdk.UserLookup) (identitysdk.User, bool, error) {
	return s.user, s.found, s.err
}

func (s workflowIdentityEdgeStub) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	if s.err != nil {
		return nil, s.err
	}
	if !s.found {
		return nil, nil
	}
	if s.user.ID == "" {
		assignee := s.user
		assignee.ID = "assignee"
		user := s.user
		user.ID = "user"
		return []identitysdk.User{assignee, user}, nil
	}
	return []identitysdk.User{s.user}, nil
}

func workflowDeadlineService(worker *workflowDeadlineWorkerEdgeStub, invoke func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error)) *WorkflowApplicationService {
	dependencies := WorkflowDependencies{
		Schema:       workflowSchemaProviderEdgeStub{},
		InvokeAction: invoke,
		Worker:       workerplatform.NewDependencies("workflow-deadline-test"),
	}
	service := NewWorkflowApplicationService(dependencies)
	service.workerRepo = worker
	return service
}

func TestWorkflowTaskLifecycleNotificationAdvancesAcrossRecoveredDecisions(t *testing.T) {
	compile := func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{ID: intent.ID, SourceEventID: intent.SourceEventID, GroupKey: intent.GroupKey}, nil
	}
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace", WorkflowName: "Refund approval"}
	first := workflowmodel.WorkflowTask{ID: "task", UpdatedAt: "2026-07-30T01:00:00Z"}
	second := first
	second.UpdatedAt = "2026-07-30T01:01:00Z"
	firstEvent, err := compileWorkflowTaskLifecycleNotification(compile, process, first, "workflow.task.completed", "completed", "", first.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	recoveredEvent, err := compileWorkflowTaskLifecycleNotification(compile, process, second, "workflow.task.completed", "completed", "", second.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if firstEvent.ID == recoveredEvent.ID || firstEvent.SourceEventID == recoveredEvent.SourceEventID || firstEvent.GroupKey != recoveredEvent.GroupKey {
		t.Fatalf("first=%+v recovered=%+v", firstEvent, recoveredEvent)
	}
}

func TestWorkflowTaskReminderProviderAndEventOutcomes(t *testing.T) {
	actor := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "actor", WorkspaceID: "workspace"}}
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", ObjectKey: "order", RecordID: "record", Variables: map[string]any{"name": "Ada"}, DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow"}}
	node := definitionmodel.WorkflowGraphNode{ID: "approval"}
	task := workflowmodel.WorkflowTask{ID: "task", AssigneeUserID: "approver"}
	contract := definitionmodel.WorkflowApprovalNodeContract{ReminderActionKey: "notify", ReminderInput: map[string]any{"subject": "Hello $payload.name"}}
	worker := &workflowDeadlineWorkerEdgeStub{}
	var invocation WorkflowBusinessActionInvocation
	service := workflowDeadlineService(worker, func(_ context.Context, value WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		invocation = value
		return WorkflowBusinessActionInvocationResult{InvocationID: "invocation", OutboxIDs: []string{"outbox"}}, nil
	})
	if err := service.queueWorkflowTaskReminder(t.Context(), process, node, task, contract, actor); err != nil {
		t.Fatal(err)
	}
	if invocation.ActionKey != "notify" || invocation.Input["subject"] != "Hello Ada" || len(worker.events) != 1 || worker.events[0].ActorID != "actor" {
		t.Fatalf("invocation=%+v events=%+v", invocation, worker.events)
	}
	providerErr := errors.New("provider")
	service = workflowDeadlineService(&workflowDeadlineWorkerEdgeStub{}, func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		return WorkflowBusinessActionInvocationResult{}, providerErr
	})
	if err := service.queueWorkflowTaskReminder(t.Context(), process, node, task, contract, actor); !errors.Is(err, providerErr) {
		t.Fatalf("provider error=%v", err)
	}
	eventErr := errors.New("event")
	worker = &workflowDeadlineWorkerEdgeStub{eventErr: eventErr}
	service = workflowDeadlineService(worker, func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		return WorkflowBusinessActionInvocationResult{}, nil
	})
	if err := service.queueWorkflowTaskReminder(t.Context(), process, node, task, contract, actor); !errors.Is(err, eventErr) {
		t.Fatalf("event error=%v", err)
	}
}

func TestWorkflowTaskEscalationRecipientAndEventOutcomes(t *testing.T) {
	actor := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "actor", WorkspaceID: "workspace"}}
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow"}}
	node := definitionmodel.WorkflowGraphNode{ID: "approval"}
	worker := &workflowDeadlineWorkerEdgeStub{}
	service := workflowDeadlineService(worker, nil)
	service.processEngine = NewWorkflowProcessRuntime(WorkflowDependencies{}).ProcessEngine()
	task := workflowmodel.WorkflowTask{ID: "task", AssigneeUserID: "old"}
	contract := definitionmodel.WorkflowApprovalNodeContract{EscalationResolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"new", "new"}}}}
	if err := service.escalateWorkflowTask(t.Context(), process, node, &task, contract, actor); err != nil {
		t.Fatal(err)
	}
	if task.AssigneeUserID != "new" || task.AssigneeName != "" || len(worker.events) != 1 {
		t.Fatalf("task=%+v events=%+v", task, worker.events)
	}
	service.identity = workflowIdentityEdgeStub{user: identitysdk.User{ID: "new", Name: "New User"}, found: true}
	namedTask := workflowmodel.WorkflowTask{ID: "named", AssigneeUserID: "old"}
	if err := service.escalateWorkflowTask(t.Context(), process, node, &namedTask, contract, actor); err != nil || namedTask.AssigneeName != "New User" {
		t.Fatalf("named task=%+v err=%v", namedTask, err)
	}
	service.identity = workflowIdentityEdgeStub{err: errors.New("identity")}
	unnamedTask := workflowmodel.WorkflowTask{ID: "unnamed", AssigneeUserID: "old"}
	if err := service.escalateWorkflowTask(t.Context(), process, node, &unnamedTask, contract, actor); err != nil || unnamedTask.AssigneeName != "" {
		t.Fatalf("unnamed task=%+v err=%v", unnamedTask, err)
	}
	service.identity = workflowIdentityEdgeStub{}
	missingNameTask := workflowmodel.WorkflowTask{ID: "missing-name", AssigneeUserID: "old"}
	if err := service.escalateWorkflowTask(t.Context(), process, node, &missingNameTask, contract, actor); err != nil || missingNameTask.AssigneeName != "" {
		t.Fatalf("missing name task=%+v err=%v", missingNameTask, err)
	}
	missing := definitionmodel.WorkflowApprovalNodeContract{EscalationResolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users"}}}
	if err := service.escalateWorkflowTask(t.Context(), process, node, &workflowmodel.WorkflowTask{}, missing, actor); apperror.CodeOf(err) != "backend.workflow.escalation_assignee_not_found" {
		t.Fatalf("missing error=%v", err)
	}
	invalid := definitionmodel.WorkflowApprovalNodeContract{EscalationResolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "invalid"}}}
	if err := service.escalateWorkflowTask(t.Context(), process, node, &workflowmodel.WorkflowTask{}, invalid, actor); apperror.CodeOf(err) != "backend.workflow.approval_resolver_invalid" {
		t.Fatalf("invalid error=%v", err)
	}
	worker.eventErr = errors.New("event")
	if err := service.escalateWorkflowTask(t.Context(), process, node, &workflowmodel.WorkflowTask{}, contract, actor); err == nil {
		t.Fatal("expected event error")
	}
	if !workflowTaskEventExists([]workflowmodel.WorkflowProcessEvent{{TaskID: "other", Event: "event"}, {TaskID: "task", Event: "other"}, {TaskID: "task", Event: "event"}}, "task", "event") || workflowTaskEventExists(nil, "task", "event") {
		t.Fatal("task event lookup mismatch")
	}
	worker.eventErr = nil
	if err := service.recordWorkflowTaskDeadlineEvent(t.Context(), "workspace", "process", "node", "task", "event", "summary", "", map[string]any{"value": true}); err != nil {
		t.Fatal(err)
	}
	if worker.events[len(worker.events)-1].ActorID != "system" {
		t.Fatalf("event=%+v", worker.events[len(worker.events)-1])
	}
}

func TestWorkflowCCNodeValidationRecipientProviderAndSuccessOutcomes(t *testing.T) {
	actor := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "actor", WorkspaceID: "workspace"}}
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", ObjectKey: "order", RecordID: "record", Variables: map[string]any{"name": "Ada"}, DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow"}}
	for name, contract := range map[string]definitionmodel.WorkflowCCNodeContract{
		"missing action":    {},
		"missing recipient": {NotificationActionKey: "notify", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users"}}},
		"invalid resolver":  {NotificationActionKey: "notify", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "invalid"}}},
	} {
		t.Run(name, func(t *testing.T) {
			engine := NewWorkflowProcessRuntime(WorkflowDependencies{Schema: workflowSchemaProviderEdgeStub{}}).ProcessEngine()
			node := definitionmodel.WorkflowGraphNode{ID: "cc", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &contract}}
			if _, err := engine.executeCCNode(t.Context(), process, node, actor); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	contract := definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "notify", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"user-b", "user-a", "user-a"}}}, Input: map[string]any{"subject": "$payload.name"}}
	node := definitionmodel.WorkflowGraphNode{ID: "cc", Name: "Notify", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &contract}}
	processStore := &workflowNotificationProcessEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	var invoked WorkflowBusinessActionInvocation
	runtime := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processStore, Schema: workflowSchemaProviderEdgeStub{}, InvokeAction: func(_ context.Context, invocation WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		invoked = invocation
		return WorkflowBusinessActionInvocationResult{InvocationID: "invocation"}, nil
	}})
	result, err := runtime.ProcessEngine().executeCCNode(t.Context(), process, node, actor)
	if err != nil || result["notified"] != true || invoked.Input["subject"] != "Ada" || len(processStore.events) != 1 {
		t.Fatalf("result=%v invocation=%+v events=%v err=%v", result, invoked, processStore.events, err)
	}
	providerErr := errors.New("provider")
	runtime = NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processStore, Schema: workflowSchemaProviderEdgeStub{}, InvokeAction: func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		return WorkflowBusinessActionInvocationResult{}, providerErr
	}})
	if _, err := runtime.ProcessEngine().executeCCNode(t.Context(), process, node, actor); !errors.Is(err, providerErr) {
		t.Fatalf("provider error=%v", err)
	}
}

func TestWorkflowRecordFailedNodeIgnoresPersistenceFailure(t *testing.T) {
	processStore := &workflowNotificationProcessEdgeStub{
		workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}},
		insertNodeErr:                errors.New("insert node"),
	}
	engine := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processStore}).ProcessEngine()
	engine.recordFailedNode(t.Context(), workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace"}, definitionmodel.WorkflowGraphNode{ID: "node"}, nil, errors.New("failure"), "actor")
	if len(processStore.events) != 0 {
		t.Fatalf("unexpected events=%v", processStore.events)
	}
}
