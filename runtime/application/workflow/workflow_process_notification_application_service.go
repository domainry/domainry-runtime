package workflow

import (
	"crypto/sha256"
	"fmt"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"strings"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func (e *WorkflowProcessEngine) insertApprovalTaskWithNotification(ctx context.Context, process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask, locale string) error {
	// Sequential approvals are persisted ahead of time, but a pending task is
	// not actionable yet. Its assignment notification is emitted only when the
	// workflow promotes it to open.
	if task.Status != "open" {
		return e.runtime.dependencies.Processes.InsertTask(ctx, process.WorkspaceID, task)
	}
	compiler, committer := e.runtime.dependencies.CompileNotification, e.runtime.dependencies.TaskNotificationCommit
	if compiler == nil && committer == nil {
		return e.runtime.dependencies.Processes.InsertTask(ctx, process.WorkspaceID, task)
	}
	if compiler == nil || committer == nil {
		return fmt.Errorf("workflow notification transaction dependencies are incomplete")
	}
	event, err := e.compileTaskAssignedNotification(process, task, locale, compiler)
	if err != nil {
		return err
	}
	return committer.CommitWorkflowTaskNotification(ctx, process.WorkspaceID, task, event)
}

func (e *WorkflowProcessEngine) compileTaskAssignedNotification(process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask, locale string, compiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)) (notificationmodel.NotificationEvent, error) {
	return compiler(notificationmodel.NotificationIntent{
		ID: "notification_" + task.ID + "_assigned", WorkspaceID: process.WorkspaceID,
		SourceEventID: task.ID + ":assigned:" + task.UpdatedAt, EventType: "workflow.task.assigned", Surface: "business_workspace",
		RecipientUserIDs: []string{task.AssigneeUserID}, SubjectType: "workflow_task", SubjectID: task.ID, SubjectVersion: task.UpdatedAt, GroupKey: workflowTaskNotificationGroupKey(task.ID),
		OccurredAt: task.CreatedAt, ExpiresAt: task.DueAt, Locale: locale,
		Variables: map[string]any{"task_title": task.Title, "workflow_name": process.WorkflowName, "due_at": task.DueAt},
	})
}

func compileWorkflowTaskLifecycleNotification(compiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error), process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask, eventType, lifecycle, locale, occurredAt string) (notificationmodel.NotificationEvent, error) {
	actionState := notificationmodel.NotificationActionCompleted
	if lifecycle == "cancelled" {
		actionState = notificationmodel.NotificationActionCancelled
	}
	eventRevision := fmt.Sprintf("%x", sha256.Sum256([]byte(task.ID+":"+lifecycle+":"+task.UpdatedAt)))[:16]
	return compiler(notificationmodel.NotificationIntent{
		ID: "notification_" + task.ID + "_" + lifecycle + "_" + eventRevision, WorkspaceID: process.WorkspaceID,
		SourceEventID: task.ID + ":" + lifecycle + ":" + task.UpdatedAt, EventType: eventType, Surface: "business_workspace",
		RecipientUserIDs: []string{task.AssigneeUserID}, SubjectType: "workflow_task", SubjectID: task.ID, SubjectVersion: task.UpdatedAt,
		GroupKey: workflowTaskNotificationGroupKey(task.ID), ActionState: actionState, OccurredAt: occurredAt, Locale: locale,
		Variables: map[string]any{"task_title": task.Title, "workflow_name": process.WorkflowName, "decision": task.Decision},
	})
}

func workflowTaskNotificationGroupKey(taskID string) string {
	return "workflow_task:" + strings.TrimSpace(taskID)
}

func appendWorkflowTaskNotification(commit *transactionmodel.WorkflowDecisionCommit, compiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error), process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask, eventType, lifecycle, locale, occurredAt string) error {
	if compiler == nil {
		return nil
	}
	event, err := compileWorkflowTaskLifecycleNotification(compiler, process, task, eventType, lifecycle, locale, occurredAt)
	if err != nil {
		return err
	}
	commit.NotificationEvents = append(commit.NotificationEvents, event)
	return nil
}

func prepareWorkflowDecisionNotifications(records *WorkflowProcessRuntime, commit *transactionmodel.WorkflowDecisionCommit, process workflowmodel.WorkflowProcessInstance, occurredAt string) error {
	compiler := records.dependencies.CompileNotification
	if compiler == nil {
		return nil
	}
	if err := appendWorkflowTaskNotification(commit, compiler, process, commit.DecidedTask, "workflow.task.completed", "completed", "", occurredAt); err != nil {
		return err
	}
	for _, task := range append(append([]workflowmodel.WorkflowTask(nil), commit.UpdateTasks...), commit.InsertTasks...) {
		switch task.Status {
		case "open":
			event, err := records.processEngine.compileTaskAssignedNotification(process, task, "", compiler)
			if err != nil {
				return err
			}
			commit.NotificationEvents = append(commit.NotificationEvents, event)
		case "cancelled":
			if err := appendWorkflowTaskNotification(commit, compiler, process, task, "workflow.task.cancelled", "cancelled", "", occurredAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *WorkflowProcessEngine) openSequentialApprovalTask(ctx context.Context, process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask) error {
	compiler, committer := e.runtime.dependencies.CompileNotification, e.runtime.dependencies.TaskNotificationCommit
	if compiler == nil && committer == nil {
		return e.runtime.dependencies.Processes.UpdateTask(ctx, process.WorkspaceID, task)
	}
	if compiler == nil || committer == nil {
		return fmt.Errorf("workflow notification transaction dependencies are incomplete")
	}
	locale := ""
	if user, found, err := e.runtime.dependencies.Identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(task.AssigneeUserID)}); err == nil && found {
		locale = user.Locale
	}
	event, err := e.compileTaskAssignedNotification(process, task, locale, compiler)
	if err != nil {
		return err
	}
	return committer.CommitWorkflowTaskOpeningNotification(ctx, process.WorkspaceID, task, event)
}

func (e *WorkflowProcessEngine) executeCCNode(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, principal principalmodel.Principal) (map[string]any, error) {
	contract := workflowpolicy.WorkflowCCNodeContract(node)
	if strings.TrimSpace(contract.NotificationActionKey) == "" {
		return nil, badRequest("backend.workflow.cc_notification_action_required", "node", node.ID)
	}
	recipients, err := e.resolveWorkflowRecipients(ctx, process, contract.Resolvers, principal)
	if err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return nil, badRequest("backend.workflow.cc_recipient_not_found", "node", node.ID)
	}
	actionPrincipal, err := NewWorkflowPrincipalResolver(e.runtime.dependencies.Principals).ResolveWorkflowPrincipal(ctx, process.DefinitionSnapshot, principal)
	if err != nil {
		return nil, err
	}
	input := workflowpolicy.WorkflowCloneMap(process.Variables)
	for key, value := range renderWorkflowRecordData(ctx, contract.Input, process.Variables, actionPrincipal) {
		input[key] = value
	}
	input["recipients"] = recipients
	input["workflow_process_id"] = process.ID
	input["workflow_node_id"] = node.ID
	invocation, err := e.runtime.dependencies.InvokeAction(ctx, WorkflowBusinessActionInvocation{
		ActionKey: contract.NotificationActionKey, ObjectKey: process.ObjectKey, RecordID: process.RecordID,
		Input: input, Principal: actionPrincipal, Source: WorkflowBusinessActionSourceWorkflow, ProcessID: process.ID, NodeID: node.ID,
	})
	if err != nil {
		return nil, err
	}
	e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, "", "cc_notified", "system", node.Name, map[string]any{
		"recipients": recipients, "action_key": contract.NotificationActionKey, "invocation_id": invocation.InvocationID,
	})
	return map[string]any{
		"notified": true, "recipients": recipients, "action_key": contract.NotificationActionKey, "invocation_id": invocation.InvocationID,
	}, nil
}

func (e *WorkflowProcessEngine) resolveWorkflowRecipients(ctx context.Context, process workflowmodel.WorkflowProcessInstance, resolvers []definitionmodel.WorkflowAssigneeResolver, principal principalmodel.Principal) ([]string, error) {
	recipients := []string{}
	for _, resolver := range resolvers {
		users, _, err := e.resolveApprovalAssigneeStrategy(ctx, process, resolver, principal)
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, users...)
	}
	return uniqueSortedStrings(recipients), nil
}
