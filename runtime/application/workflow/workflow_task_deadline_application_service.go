package workflow

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

// ProcessApprovalDeadlineTimer executes one durable approval reminder or
// escalation. The Scheduler owns delivery and fencing; Workflow owns task
// state, recipients, action invocation, and idempotent event evidence.
func (s *WorkflowApplicationService) ProcessApprovalDeadlineTimer(ctx context.Context, workspaceID, taskID, phase string, principal principalmodel.Principal) error {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return err
	}
	ctx = requestcontext.WithWorkspaceID(ctx, workspaceID)
	if s.processRepo == nil || s.workerRepo == nil {
		return internalError("process workflow approval deadline timer", nil)
	}
	task, found, err := s.processRepo.GetTask(ctx, workspaceID, strings.TrimSpace(taskID))
	if err != nil {
		return internalError("get workflow approval deadline task", err)
	}
	if !found || task.Status != "open" {
		return nil
	}
	process, found, err := s.processRepo.GetProcess(ctx, workspaceID, task.ProcessID)
	if err != nil {
		return internalError("get workflow approval deadline process", err)
	}
	if !found {
		return nil
	}
	node, found := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, task.NodeID)
	if !found {
		return nil
	}
	contract := workflowpolicy.WorkflowApprovalNodeContract(node)
	events, err := s.processRepo.ListEvents(ctx, workspaceID, process.ProcessID, 2000)
	if err != nil {
		return internalError("list workflow approval deadline events", err)
	}
	switch strings.TrimSpace(phase) {
	case "reminder":
		if workflowTaskEventExists(events, task.TaskID, "task_reminder_queued") {
			return nil
		}
		return s.queueWorkflowTaskReminder(ctx, process, node, task, contract, principal)
	case "escalation":
		if contract.EscalationSeconds <= 0 || workflowTaskEventExists(events, task.TaskID, "task_escalated") {
			return nil
		}
		previousAssignee, err := s.prepareWorkflowTaskEscalation(ctx, process, &task, contract, principal)
		if err != nil {
			return err
		}
		return s.commitWorkflowTaskEscalation(ctx, process, node, task, previousAssignee, principal)
	default:
		return badRequest("backend.workflow.approval_deadline_phase_invalid", "phase", phase)
	}
}

func (s *WorkflowApplicationService) queueWorkflowTaskReminder(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, task workflowmodel.WorkflowTask, contract definitionmodel.WorkflowApprovalNodeContract, actor principalmodel.Principal) error {
	input := workflowpolicy.WorkflowCloneMap(process.Variables)
	for key, value := range renderWorkflowRecordData(ctx, contract.ReminderInput, process.Variables, actor) {
		input[key] = value
	}
	input["recipients"] = []string{task.AssigneeUserID}
	input["workflow_process_id"] = process.ProcessID
	input["workflow_node_id"] = node.ID
	input["workflow_task_id"] = task.TaskID
	invocation := WorkflowBusinessActionInvocationResult{}
	if strings.TrimSpace(contract.ReminderActionKey) != "" {
		actionPrincipal, err := s.workflowPrincipalForExecution(ctx, process.DefinitionSnapshot, actor, workflowPrincipalExecutionForProcess(process, task.TaskID))
		if err != nil {
			return err
		}
		invocation, err = s.invokeAction(ctx, WorkflowBusinessActionInvocation{
			ActionKey: contract.ReminderActionKey, ObjectKey: process.ObjectKey, RecordID: process.RecordID, Input: input,
			Principal: actionPrincipal, Actor: actor, Source: WorkflowBusinessActionSourceWorkflow, ProcessID: process.ProcessID, NodeID: node.ID,
			IdempotencyKey: task.TaskID + ":deadline-reminder",
		})
		if err != nil {
			return err
		}
	}
	metadata := map[string]any{"invocation_id": invocation.InvocationID, "outbox_ids": invocation.OutboxIDs, "recipient_user_id": task.AssigneeUserID}
	compiler, committer := s.processEngine.runtime.dependencies.CompileNotification, s.processEngine.runtime.dependencies.TaskNotificationCommit
	if compiler == nil && committer == nil {
		return s.recordWorkflowTaskDeadlineEvent(ctx, process.WorkspaceID, process.ProcessID, node.ID, task.TaskID, "task_reminder_queued", "workflow.task.reminderQueued", actor.UserID, metadata)
	}
	if compiler == nil || committer == nil {
		return internalError("compile workflow reminder notification", nil)
	}
	now := s.worker.Clock.Now().Format(time.RFC3339Nano)
	event, err := compiler(notificationmodel.NotificationIntent{
		WorkspaceID: process.WorkspaceID, SourceEventID: task.TaskID + ":reminded:" + task.UpdatedAt,
		EventType: "workflow.task.reminded", RecipientUserIDs: []string{task.AssigneeUserID},
		SubjectType: "workflow_task", SubjectID: task.TaskID, SubjectVersion: task.UpdatedAt, GroupKey: workflowTaskNotificationGroupKey(task.TaskID),
		OccurredAt: now, ExpiresAt: task.DueAt, Variables: map[string]any{"task_title": task.Title, "workflow_name": process.WorkflowName, "decision": task.Decision},
	})
	if err != nil {
		return err
	}
	processEvent := workflowmodel.WorkflowProcessEvent{WorkspaceID: process.WorkspaceID, EventID: WorkflowProcessID(ctx, "event"), ProcessID: process.ProcessID, NodeID: node.ID, TaskID: task.TaskID, Event: "task_reminder_queued", ActorID: valueOrDefault(actor.UserID, "system"), Summary: "workflow.task.reminderQueued", Metadata: workflowpolicy.WorkflowCloneMap(metadata), CreatedAt: now}
	return committer.CommitWorkflowTaskReminderNotification(ctx, process.WorkspaceID, processEvent, event)
}

func (s *WorkflowApplicationService) escalateWorkflowTask(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, task *workflowmodel.WorkflowTask, contract definitionmodel.WorkflowApprovalNodeContract, actor principalmodel.Principal) error {
	previousAssignee, err := s.prepareWorkflowTaskEscalation(ctx, process, task, contract, actor)
	if err != nil {
		return err
	}
	return s.recordWorkflowTaskDeadlineEvent(ctx, process.WorkspaceID, process.ProcessID, node.ID, task.TaskID, "task_escalated", "workflow.task.escalated", actor.UserID, map[string]any{"previous_assignee_user_id": previousAssignee, "assignee_user_id": task.AssigneeUserID, "assignee_role_key": task.AssigneeRoleKey, "resolver_key": task.AssigneeResolverKey, "assignee_evidence": task.AssigneeEvidence})
}

func (s *WorkflowApplicationService) prepareWorkflowTaskEscalation(ctx context.Context, process workflowmodel.WorkflowProcessInstance, task *workflowmodel.WorkflowTask, contract definitionmodel.WorkflowApprovalNodeContract, actor principalmodel.Principal) (string, error) {
	assignees, err := s.processEngine.ResolveAssignees(ctx, process, task.NodeID, contract.EscalationResolvers, actor)
	if err != nil {
		return "", err
	}
	if len(assignees) == 0 {
		return "", badRequest("backend.workflow.escalation_assignee_not_found")
	}
	previousAssignee := task.AssigneeUserID
	task.AssigneeUserID = assignees[0].UserID
	task.AssigneeRoleKey = assignees[0].RoleKey
	task.AssigneeResolverKey = assignees[0].ResolverKey
	task.AssigneeEvidence = assignees[0].Evidence
	task.AssigneeName = ""
	if s.identity != nil {
		if user, ok, getErr := s.identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(task.AssigneeUserID)}); getErr == nil && ok {
			task.AssigneeName = user.Name
		}
	}
	task.UpdatedAt = s.worker.Clock.Now().Format(time.RFC3339Nano)
	return previousAssignee, nil
}

func (s *WorkflowApplicationService) commitWorkflowTaskEscalation(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, task workflowmodel.WorkflowTask, previousAssignee string, actor principalmodel.Principal) error {
	metadata := map[string]any{"previous_assignee_user_id": previousAssignee, "assignee_user_id": task.AssigneeUserID, "assignee_role_key": task.AssigneeRoleKey, "resolver_key": task.AssigneeResolverKey, "assignee_evidence": task.AssigneeEvidence}
	compiler, committer := s.processEngine.runtime.dependencies.CompileNotification, s.processEngine.runtime.dependencies.TaskNotificationCommit
	if compiler == nil && committer == nil {
		if err := s.recordWorkflowTaskDeadlineEvent(ctx, process.WorkspaceID, process.ProcessID, node.ID, task.TaskID, "task_escalated", "workflow.task.escalated", actor.UserID, metadata); err != nil {
			return err
		}
		if err := s.processRepo.UpdateTask(ctx, process.WorkspaceID, task); err != nil {
			return internalError("update escalated workflow task", err)
		}
		return nil
	}
	if compiler == nil || committer == nil {
		return internalError("compile workflow escalation notifications", nil)
	}
	now := s.worker.Clock.Now().Format(time.RFC3339Nano)
	previousTask := task
	previousTask.AssigneeUserID = previousAssignee
	oldEvent, err := compileWorkflowTaskLifecycleNotification(compiler, process, previousTask, "workflow.task.cancelled", "cancelled", "", now, task.TaskID+":reassigned_from:"+previousAssignee+":"+task.UpdatedAt)
	if err != nil {
		return err
	}
	newEvent, err := s.processEngine.compileTaskAssignedNotification(process, task, "", compiler, task.TaskID+":assigned_to:"+task.AssigneeUserID+":"+task.UpdatedAt)
	if err != nil {
		return err
	}
	newEvent.OccurredAt = now
	processEvent := workflowmodel.WorkflowProcessEvent{WorkspaceID: process.WorkspaceID, EventID: WorkflowProcessID(ctx, "event"), ProcessID: process.ProcessID, NodeID: node.ID, TaskID: task.TaskID, Event: "task_escalated", ActorID: valueOrDefault(actor.UserID, "system"), Summary: "workflow.task.escalated", Metadata: workflowpolicy.WorkflowCloneMap(metadata), CreatedAt: now}
	return committer.CommitWorkflowTaskEscalationNotification(ctx, process.WorkspaceID, task, processEvent, []notificationmodel.NotificationEvent{oldEvent, newEvent})
}

func workflowTaskEventExists(events []workflowmodel.WorkflowProcessEvent, taskID, eventName string) bool {
	for _, event := range events {
		if event.TaskID == taskID && event.Event == eventName {
			return true
		}
	}
	return false
}

func (s *WorkflowApplicationService) recordWorkflowTaskDeadlineEvent(ctx context.Context, workspaceID, processID, nodeID, taskID, eventName, summaryKey, actorID string, metadata map[string]any) error {
	return s.workerRepo.InsertProcessEvent(ctx, workspaceID, workflowmodel.WorkflowProcessEvent{WorkspaceID: workspaceID, EventID: WorkflowProcessID(ctx, "event"), ProcessID: processID, NodeID: nodeID, TaskID: taskID, Event: eventName, ActorID: valueOrDefault(actorID, "system"), Summary: summaryKey, Metadata: workflowpolicy.WorkflowCloneMap(metadata), CreatedAt: s.worker.Clock.Now().Format(time.RFC3339Nano)})
}
