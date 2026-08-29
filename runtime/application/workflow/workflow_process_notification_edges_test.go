package workflow

import (
	"context"
	"errors"
	"testing"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowTaskNotificationCommitProbe struct {
	taskErr, openingErr, reminderErr, escalationErr         error
	taskCalls, openingCalls, reminderCalls, escalationCalls int
}

func (p *workflowTaskNotificationCommitProbe) CommitWorkflowTaskNotification(context.Context, string, workflowmodel.WorkflowTask, notificationmodel.NotificationEvent) error {
	p.taskCalls++
	return p.taskErr
}
func (p *workflowTaskNotificationCommitProbe) CommitWorkflowTaskOpeningNotification(context.Context, string, workflowmodel.WorkflowTask, notificationmodel.NotificationEvent) error {
	p.openingCalls++
	return p.openingErr
}
func (p *workflowTaskNotificationCommitProbe) CommitWorkflowTaskReminderNotification(context.Context, string, workflowmodel.WorkflowProcessEvent, notificationmodel.NotificationEvent) error {
	p.reminderCalls++
	return p.reminderErr
}
func (p *workflowTaskNotificationCommitProbe) CommitWorkflowTaskEscalationNotification(context.Context, string, workflowmodel.WorkflowTask, workflowmodel.WorkflowProcessEvent, []notificationmodel.NotificationEvent) error {
	p.escalationCalls++
	return p.escalationErr
}

func workflowNotificationCompiler(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
	return notificationmodel.NotificationEvent{ID: intent.ID, ActionState: intent.ActionState}, nil
}

func TestInsertApprovalTaskNotificationDependencyAndCompilerBoundaries(t *testing.T) {
	store := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace", WorkflowName: "Flow"}
	pending := workflowmodel.WorkflowTask{ID: "pending", Status: "pending"}
	if err := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store}).ProcessEngine().insertApprovalTaskWithNotification(t.Context(), process, pending, ""); err != nil {
		t.Fatal(err)
	}
	open := workflowmodel.WorkflowTask{ID: "open", Status: "open", AssigneeUserID: "user"}
	if err := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store}).ProcessEngine().insertApprovalTaskWithNotification(t.Context(), process, open, ""); err != nil {
		t.Fatal(err)
	}
	committer := &workflowTaskNotificationCommitProbe{}
	for _, dependencies := range []WorkflowDependencies{
		{Processes: store, TaskNotificationCommit: committer},
		{Processes: store, CompileNotification: workflowNotificationCompiler},
	} {
		if err := NewWorkflowProcessRuntime(dependencies).ProcessEngine().insertApprovalTaskWithNotification(t.Context(), process, open, ""); err == nil {
			t.Fatal("incomplete dependencies accepted")
		}
	}
	compileErr := errors.New("compile")
	engine := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, TaskNotificationCommit: committer, CompileNotification: func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, compileErr
	}}).ProcessEngine()
	if err := engine.insertApprovalTaskWithNotification(t.Context(), process, open, ""); !errors.Is(err, compileErr) {
		t.Fatalf("compile err=%v", err)
	}
	committer.taskErr = errors.New("commit")
	engine = NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, TaskNotificationCommit: committer, CompileNotification: workflowNotificationCompiler}).ProcessEngine()
	if err := engine.insertApprovalTaskWithNotification(t.Context(), process, open, "en"); !errors.Is(err, committer.taskErr) || committer.taskCalls != 1 {
		t.Fatalf("commit err=%v calls=%d", err, committer.taskCalls)
	}
}

func TestPrepareWorkflowDecisionNotificationsRemainingOutcomes(t *testing.T) {
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace", WorkflowName: "Flow"}
	task := workflowmodel.WorkflowTask{ID: "task", AssigneeUserID: "user", UpdatedAt: "now"}
	completed, err := compileWorkflowTaskLifecycleNotification(workflowNotificationCompiler, process, task, "completed", "completed", "", "now")
	if err != nil || completed.ActionState != notificationmodel.NotificationActionCompleted {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	cancelled, err := compileWorkflowTaskLifecycleNotification(workflowNotificationCompiler, process, task, "cancelled", "cancelled", "", "now")
	if err != nil || cancelled.ActionState != notificationmodel.NotificationActionCancelled {
		t.Fatalf("cancelled=%+v err=%v", cancelled, err)
	}
	commit := &transactionmodel.WorkflowDecisionCommit{}
	if err := appendWorkflowTaskNotification(commit, nil, process, task, "", "", "", ""); err != nil || len(commit.NotificationEvents) != 0 {
		t.Fatalf("nil compiler commit=%+v err=%v", commit, err)
	}
	compileErr := errors.New("compile")
	if err := appendWorkflowTaskNotification(commit, func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, compileErr
	}, process, task, "", "", "", ""); !errors.Is(err, compileErr) {
		t.Fatalf("append err=%v", err)
	}
	records := NewWorkflowProcessRuntime(WorkflowDependencies{})
	if err := prepareWorkflowDecisionNotifications(records, commit, process, "now"); err != nil {
		t.Fatal(err)
	}
	records.dependencies.CompileNotification = workflowNotificationCompiler
	commit = &transactionmodel.WorkflowDecisionCommit{DecidedTask: task, UpdateTasks: []workflowmodel.WorkflowTask{{ID: "open", Status: "open"}, {ID: "ignored", Status: "pending"}}, InsertTasks: []workflowmodel.WorkflowTask{{ID: "cancelled", Status: "cancelled"}}}
	if err := prepareWorkflowDecisionNotifications(records, commit, process, "now"); err != nil || len(commit.NotificationEvents) != 3 {
		t.Fatalf("events=%+v err=%v", commit.NotificationEvents, err)
	}
	for failAt := 1; failAt <= 3; failAt++ {
		calls := 0
		records.dependencies.CompileNotification = func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
			calls++
			if calls == failAt {
				return notificationmodel.NotificationEvent{}, compileErr
			}
			return workflowNotificationCompiler(intent)
		}
		candidate := &transactionmodel.WorkflowDecisionCommit{DecidedTask: task, UpdateTasks: []workflowmodel.WorkflowTask{{ID: "open", Status: "open"}}, InsertTasks: []workflowmodel.WorkflowTask{{ID: "cancelled", Status: "cancelled"}}}
		if err := prepareWorkflowDecisionNotifications(records, candidate, process, "now"); !errors.Is(err, compileErr) {
			t.Fatalf("failAt=%d err=%v", failAt, err)
		}
	}
}

func TestOpenSequentialApprovalTaskNotificationBoundaries(t *testing.T) {
	store := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace", WorkflowName: "Flow"}
	task := workflowmodel.WorkflowTask{ID: "task", AssigneeUserID: "user"}
	if err := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store}).ProcessEngine().openSequentialApprovalTask(t.Context(), process, task); err != nil {
		t.Fatal(err)
	}
	committer := &workflowTaskNotificationCommitProbe{}
	for _, dependencies := range []WorkflowDependencies{{Processes: store, TaskNotificationCommit: committer}, {Processes: store, CompileNotification: workflowNotificationCompiler}} {
		if err := NewWorkflowProcessRuntime(dependencies).ProcessEngine().openSequentialApprovalTask(t.Context(), process, task); err == nil {
			t.Fatal("incomplete dependencies accepted")
		}
	}
	for _, identity := range []identitysdk.Directory{
		workflowIdentityEdgeStub{err: errors.New("identity")}, workflowIdentityEdgeStub{}, workflowIdentityEdgeStub{found: true, user: identitysdk.User{Locale: "zh-CN"}},
	} {
		engine := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, Identity: identity, TaskNotificationCommit: committer, CompileNotification: workflowNotificationCompiler}).ProcessEngine()
		if err := engine.openSequentialApprovalTask(t.Context(), process, task); err != nil {
			t.Fatal(err)
		}
	}
	compileErr := errors.New("compile")
	engine := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, Identity: workflowIdentityEdgeStub{}, TaskNotificationCommit: committer, CompileNotification: func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, compileErr
	}}).ProcessEngine()
	if err := engine.openSequentialApprovalTask(t.Context(), process, task); !errors.Is(err, compileErr) {
		t.Fatalf("compile err=%v", err)
	}
	committer.openingErr = errors.New("opening")
	engine = NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, Identity: workflowIdentityEdgeStub{}, TaskNotificationCommit: committer, CompileNotification: workflowNotificationCompiler}).ProcessEngine()
	if err := engine.openSequentialApprovalTask(t.Context(), process, task); !errors.Is(err, committer.openingErr) {
		t.Fatalf("opening err=%v", err)
	}
}

func TestWorkflowDeadlineNotificationTransactionBoundaries(t *testing.T) {
	store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	worker := &workflowDeadlineWorkerEdgeStub{}
	committer := &workflowTaskNotificationCommitProbe{}
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", WorkflowName: "Flow"}
	node := definitionmodel.WorkflowGraphNode{ID: "node"}
	task := workflowmodel.WorkflowTask{ID: "task", AssigneeUserID: "new", UpdatedAt: "now"}
	actor := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "actor"}}
	newService := func(compiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error), commit WorkflowTaskNotificationCommitter) *WorkflowApplicationService {
		service := NewWorkflowApplicationService(WorkflowDependencies{Processes: store, CompileNotification: compiler, TaskNotificationCommit: commit, Worker: workerplatform.NewDependencies("workflow-notification-edges")})
		service.workerRepo = worker
		return service
	}
	for _, service := range []*WorkflowApplicationService{newService(nil, committer), newService(workflowNotificationCompiler, nil)} {
		if err := service.queueWorkflowTaskReminder(t.Context(), process, node, task, definitionmodel.WorkflowApprovalNodeContract{}, actor); err == nil {
			t.Fatal("incomplete reminder dependencies accepted")
		}
		if err := service.commitWorkflowTaskEscalation(t.Context(), process, node, task, "old", actor); err == nil {
			t.Fatal("incomplete escalation dependencies accepted")
		}
	}
	compileErr := errors.New("compile")
	service := newService(func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, compileErr
	}, committer)
	if err := service.queueWorkflowTaskReminder(t.Context(), process, node, task, definitionmodel.WorkflowApprovalNodeContract{}, actor); !errors.Is(err, compileErr) {
		t.Fatalf("reminder compile err=%v", err)
	}
	if err := service.commitWorkflowTaskEscalation(t.Context(), process, node, task, "old", actor); !errors.Is(err, compileErr) {
		t.Fatalf("old escalation compile err=%v", err)
	}
	calls := 0
	service = newService(func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		calls++
		if calls == 2 {
			return notificationmodel.NotificationEvent{}, compileErr
		}
		return workflowNotificationCompiler(intent)
	}, committer)
	if err := service.commitWorkflowTaskEscalation(t.Context(), process, node, task, "old", actor); !errors.Is(err, compileErr) {
		t.Fatalf("new escalation compile err=%v", err)
	}
	committer.reminderErr = errors.New("reminder commit")
	committer.escalationErr = errors.New("escalation commit")
	service = newService(workflowNotificationCompiler, committer)
	if err := service.queueWorkflowTaskReminder(t.Context(), process, node, task, definitionmodel.WorkflowApprovalNodeContract{}, actor); !errors.Is(err, committer.reminderErr) {
		t.Fatalf("reminder commit err=%v", err)
	}
	if err := service.commitWorkflowTaskEscalation(t.Context(), process, node, task, "old", actor); !errors.Is(err, committer.escalationErr) {
		t.Fatalf("escalation commit err=%v", err)
	}
	committer.reminderErr, committer.escalationErr = nil, nil
	service = newService(workflowNotificationCompiler, committer)
	if err := service.queueWorkflowTaskReminder(t.Context(), process, node, task, definitionmodel.WorkflowApprovalNodeContract{}, actor); err != nil {
		t.Fatal(err)
	}
	if err := service.commitWorkflowTaskEscalation(t.Context(), process, node, task, "old", actor); err != nil {
		t.Fatal(err)
	}
	service = newService(nil, nil)
	worker.eventErr = errors.New("event")
	if err := service.commitWorkflowTaskEscalation(t.Context(), process, node, task, "old", actor); !errors.Is(err, worker.eventErr) {
		t.Fatalf("event err=%v", err)
	}
	worker.eventErr = nil
	store.updateTaskErr = errors.New("update")
	if err := service.commitWorkflowTaskEscalation(t.Context(), process, node, task, "old", actor); err == nil {
		t.Fatal("update error ignored")
	}
}
