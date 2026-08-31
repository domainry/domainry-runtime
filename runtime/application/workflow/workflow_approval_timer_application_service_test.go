package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowApprovalTimerProbe struct {
	requests []WorkflowApprovalDeadlineTimerRequest
	err      error
}

func (p *workflowApprovalTimerProbe) ScheduleWorkflowApprovalDeadlineTimer(_ context.Context, request WorkflowApprovalDeadlineTimerRequest) (string, error) {
	p.requests = append(p.requests, request)
	return request.TaskID + ":" + request.Phase, p.err
}

func TestWorkflowApprovalDeadlineSchedulesDurableReminderAndEscalationTimers(t *testing.T) {
	store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	probe := &workflowApprovalTimerProbe{}
	identity := workflowApprovalIdentityStub{users: map[string]identitysdk.User{"approver": {ID: "approver", Status: identitysdk.UserStatusActive}}}
	engine := NewWorkflowProcessEngine(WorkflowDependencies{Processes: store, Identity: identity, ApprovalDeadlineTimers: probe})
	process := workflowEngineProcess(nil, nil)
	node := definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
		DueSeconds: 60, ReminderActionKey: "notify", EscalationSeconds: 120,
		Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"approver"}}},
	}}}
	if count, err := engine.createApprovalTasks(t.Context(), process, node, workflowmodel.WorkflowNodeInstance{ID: "node"}, workflowProcessQueryPrincipal()); err != nil || count != 1 {
		t.Fatalf("approval tasks=%d err=%v", count, err)
	}
	if len(probe.requests) != 2 || probe.requests[0].Phase != "reminder" || probe.requests[1].Phase != "escalation" || probe.requests[1].DueAt.Sub(probe.requests[0].DueAt) != 120*time.Second {
		t.Fatalf("approval timer requests=%+v", probe.requests)
	}
}

func TestWorkflowApprovalDeadlineSchedulingFailureAndDisabledEdges(t *testing.T) {
	process := workflowEngineProcess(nil, nil)
	task := workflowmodel.WorkflowTask{ID: "task", NodeID: "approval", DueAt: "2026-07-27T12:00:00Z"}
	createdAt := time.Date(2026, 7, 27, 11, 0, 0, 0, time.UTC)

	noTimers := NewWorkflowProcessEngine(WorkflowDependencies{})
	if err := noTimers.scheduleApprovalDeadlineTimers(t.Context(), process, task, definitionmodel.WorkflowApprovalNodeContract{
		ReminderActionKey: "notify",
	}, createdAt); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("missing scheduler error=%v", err)
	}

	probe := &workflowApprovalTimerProbe{}
	engine := NewWorkflowProcessEngine(WorkflowDependencies{ApprovalDeadlineTimers: probe})
	invalidDue := task
	invalidDue.DueAt = "not-a-time"
	if err := engine.scheduleApprovalDeadlineTimers(t.Context(), process, invalidDue, definitionmodel.WorkflowApprovalNodeContract{
		ReminderActionKey: "notify",
	}, createdAt); apperror.CodeOf(err) != "backend.workflow.approval_due_at_invalid" {
		t.Fatalf("invalid due error=%v", err)
	}
	if err := engine.scheduleApprovalDeadlineTimers(t.Context(), process, task, definitionmodel.WorkflowApprovalNodeContract{}, createdAt); err != nil {
		t.Fatalf("disabled deadline error=%v", err)
	}
	if err := engine.scheduleApprovalDeadlineTimers(t.Context(), process, task, definitionmodel.WorkflowApprovalNodeContract{
		ReminderActionKey: "notify",
	}, createdAt); err != nil || len(probe.requests) != 1 || probe.requests[0].Phase != "reminder" {
		t.Fatalf("reminder-only requests=%+v err=%v", probe.requests, err)
	}

	failing := &workflowApprovalTimerProbe{err: errors.New("schedule")}
	failingEngine := NewWorkflowProcessEngine(WorkflowDependencies{ApprovalDeadlineTimers: failing})
	if err := failingEngine.scheduleApprovalDeadlineTimers(t.Context(), process, task, definitionmodel.WorkflowApprovalNodeContract{
		ReminderActionKey: "notify",
	}, createdAt); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("reminder failure=%v", err)
	}
	if err := failingEngine.scheduleApprovalDeadlineTimers(t.Context(), process, task, definitionmodel.WorkflowApprovalNodeContract{
		EscalationSeconds: 60,
	}, createdAt); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("escalation failure=%v", err)
	}

	store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{
		processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{},
	}}
	identity := workflowApprovalIdentityStub{users: map[string]identitysdk.User{"approver": {ID: "approver", Status: identitysdk.UserStatusActive}}}
	taskEngine := NewWorkflowProcessEngine(WorkflowDependencies{Processes: store, Identity: identity, ApprovalDeadlineTimers: failing})
	node := definitionmodel.WorkflowGraphNode{
		ID: "approval", Type: "approval",
		Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
			DueSeconds: 60, ReminderActionKey: "notify",
			Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"approver"}}},
		}},
	}
	if _, err := taskEngine.createApprovalTasks(t.Context(), process, node, workflowmodel.WorkflowNodeInstance{ID: "node"}, workflowProcessQueryPrincipal()); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("task timer failure=%v", err)
	}
}

func TestWorkflowApprovalDeadlineTimerExecutesOnceAndSkipsCompletedTask(t *testing.T) {
	contract := definitionmodel.WorkflowApprovalNodeContract{
		ReminderActionKey: "notify", EscalationSeconds: 120,
		EscalationResolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"manager"}}},
	}
	node := definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &contract}}
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", ObjectKey: "request", RecordID: "record", DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow", Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{node}}}}
	task := workflowmodel.WorkflowTask{ID: "task", ProcessID: process.ID, NodeID: node.ID, Status: "open", AssigneeUserID: "approver"}
	store := &workflowProcessStoreEdgeStub{
		workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{process.ID: process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}},
		getTaskValue:                 task, getTaskFound: true,
	}
	worker := &workflowDeadlineWorkerEdgeStub{}
	invocations := 0
	service := NewWorkflowApplicationService(WorkflowDependencies{
		Processes: store, Workers: worker, Identity: workflowApprovalIdentityStub{}, Schema: workflowSchemaProviderEdgeStub{},
		InvokeAction: func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
			invocations++
			return WorkflowBusinessActionInvocationResult{InvocationID: "reminder"}, nil
		},
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "runtime-scheduler", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if err := service.ProcessApprovalDeadlineTimer(t.Context(), "workspace", task.ID, "reminder", principal); err != nil || invocations != 1 {
		t.Fatalf("reminder invocations=%d err=%v", invocations, err)
	}
	store.events = []workflowmodel.WorkflowProcessEvent{{TaskID: task.ID, Event: "task_reminder_queued"}}
	if err := service.ProcessApprovalDeadlineTimer(t.Context(), "workspace", task.ID, "reminder", principal); err != nil || invocations != 1 {
		t.Fatalf("duplicate reminder invocations=%d err=%v", invocations, err)
	}
	store.events = nil
	if err := service.ProcessApprovalDeadlineTimer(t.Context(), "workspace", task.ID, "escalation", principal); err != nil || len(store.updatedTasks) != 1 || store.updatedTasks[0].AssigneeUserID != "manager" {
		t.Fatalf("escalation tasks=%+v err=%v", store.updatedTasks, err)
	}
	store.getTaskValue.Status = "approved"
	if err := service.ProcessApprovalDeadlineTimer(t.Context(), "workspace", task.ID, "escalation", principal); err != nil || len(store.updatedTasks) != 1 {
		t.Fatalf("completed task should skip tasks=%+v err=%v", store.updatedTasks, err)
	}
	if err := service.ProcessApprovalDeadlineTimer(t.Context(), "workspace", task.ID, "unknown", principal); err != nil {
		t.Fatalf("completed task must remain a no-op before phase validation: %v", err)
	}
	store.getTaskValue.Status = "open"
	if err := service.ProcessApprovalDeadlineTimer(t.Context(), "workspace", task.ID, "unknown", principal); apperror.CodeOf(err) != "backend.workflow.approval_deadline_phase_invalid" {
		t.Fatalf("invalid phase=%v", err)
	}
}

func TestWorkflowApprovalDeadlineTimerRemainingOutcomes(t *testing.T) {
	ctx := t.Context()
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "runtime-scheduler", WorkspaceID: "workspace"}}
	if err := NewWorkflowApplicationService(WorkflowDependencies{}).ProcessApprovalDeadlineTimer(ctx, "workspace", "task", "reminder", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	if err := NewWorkflowApplicationService(WorkflowDependencies{}).ProcessApprovalDeadlineTimer(ctx, "workspace", "task", "reminder", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("nil repositories error=%v", err)
	}

	contract := definitionmodel.WorkflowApprovalNodeContract{
		ReminderActionKey: "notify", EscalationSeconds: 120,
		EscalationResolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"manager"}}},
	}
	node := definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &contract}}
	process := workflowmodel.WorkflowProcessInstance{
		ID: "process", WorkspaceID: "workspace", ObjectKey: "request", RecordID: "record",
		DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow", Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{node}}},
	}
	task := workflowmodel.WorkflowTask{ID: "task", ProcessID: process.ID, NodeID: node.ID, Status: "open", AssigneeUserID: "approver"}
	store := &workflowProcessStoreEdgeStub{
		workflowExecutionProcessStub: workflowExecutionProcessStub{
			processes: map[string]workflowmodel.WorkflowProcessInstance{process.ID: process},
			nodes:     map[string][]workflowmodel.WorkflowNodeInstance{},
		},
		getTaskValue: task,
		getTaskFound: true,
	}
	if err := NewWorkflowApplicationService(WorkflowDependencies{Processes: store}).ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "reminder", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("nil worker repository error=%v", err)
	}
	worker := &workflowDeadlineWorkerEdgeStub{}
	newService := func() *WorkflowApplicationService {
		return NewWorkflowApplicationService(WorkflowDependencies{
			Processes: store, Workers: worker, Schema: workflowSchemaProviderEdgeStub{},
			InvokeAction: func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
				return WorkflowBusinessActionInvocationResult{InvocationID: "reminder"}, nil
			},
		})
	}

	fault := errors.New("fault")
	store.getTaskErr = fault
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "reminder", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("get task error=%v", err)
	}
	store.getTaskErr = nil
	store.getTaskFound = false
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "reminder", principal); err != nil {
		t.Fatalf("missing task error=%v", err)
	}
	store.getTaskFound = true

	store.getProcessErr = fault
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "reminder", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("get process error=%v", err)
	}
	store.getProcessErr = nil
	delete(store.processes, process.ID)
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "reminder", principal); err != nil {
		t.Fatalf("missing process error=%v", err)
	}
	store.processes[process.ID] = process

	missingNode := process
	missingNode.DefinitionSnapshot.Graph = &definitionmodel.WorkflowGraphSchema{Version: 2}
	store.processes[process.ID] = missingNode
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "reminder", principal); err != nil {
		t.Fatalf("missing node error=%v", err)
	}
	store.processes[process.ID] = process

	store.listEventsErr = fault
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "reminder", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("list events error=%v", err)
	}
	store.listEventsErr = nil

	emptyReminder := process
	emptyReminderContract := contract
	emptyReminderContract.ReminderActionKey = ""
	emptyReminder.DefinitionSnapshot.Graph = &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{
		ID: node.ID, Type: node.Type, Contract: &definitionmodel.WorkflowNodeContract{Approval: &emptyReminderContract},
	}}}
	store.processes[process.ID] = emptyReminder
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "reminder", principal); err != nil {
		t.Fatalf("empty reminder error=%v", err)
	}

	noEscalation := process
	noEscalationContract := contract
	noEscalationContract.EscalationSeconds = 0
	noEscalation.DefinitionSnapshot.Graph = &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{
		ID: node.ID, Type: node.Type, Contract: &definitionmodel.WorkflowNodeContract{Approval: &noEscalationContract},
	}}}
	store.processes[process.ID] = noEscalation
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "escalation", principal); err != nil {
		t.Fatalf("disabled escalation error=%v", err)
	}
	store.processes[process.ID] = process
	store.events = []workflowmodel.WorkflowProcessEvent{{TaskID: task.ID, Event: "task_escalated"}}
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "escalation", principal); err != nil {
		t.Fatalf("duplicate escalation error=%v", err)
	}
	store.events = nil

	invalidEscalation := process
	invalidContract := contract
	invalidContract.EscalationResolvers = []definitionmodel.WorkflowAssigneeResolver{{Type: "invalid"}}
	invalidEscalation.DefinitionSnapshot.Graph = &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{
		ID: node.ID, Type: node.Type, Contract: &definitionmodel.WorkflowNodeContract{Approval: &invalidContract},
	}}}
	store.processes[process.ID] = invalidEscalation
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "escalation", principal); apperror.CodeOf(err) != "backend.workflow.approval_resolver_invalid" {
		t.Fatalf("escalation error=%v", err)
	}
	store.processes[process.ID] = process
	store.updateTaskErr = fault
	if err := newService().ProcessApprovalDeadlineTimer(ctx, "workspace", task.ID, "escalation", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update task error=%v", err)
	}
}
