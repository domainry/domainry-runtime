package workflow

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"fmt"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func (e *WorkflowProcessEngine) createApprovalTasks(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, nodeInstance workflowmodel.WorkflowNodeInstance, principal principalmodel.Principal) (int, error) {
	ctx = requestcontext.WithWorkspaceID(ctx, process.WorkspaceID)
	assignees, roleKey, err := e.resolveApprovalAssignees(ctx, process, node, principal)
	if err != nil {
		return 0, err
	}
	if len(assignees) == 0 {
		if valueOrDefault(strings.TrimSpace(workflowpolicy.WorkflowApprovalNodeContract(node).EmptyAssigneePolicy), "fail") == "skip" {
			return 0, nil
		}
		return 0, badRequest("backend.workflow.approval_assignee_not_found", "node", node.ID)
	}
	contract := workflowpolicy.WorkflowApprovalNodeContract(node)
	mode := valueOrDefault(strings.TrimSpace(contract.Mode), "any")
	nowTime := time.Now().UTC()
	now := nowTime.Format(time.RFC3339Nano)
	for index, assignee := range assignees {
		status := "open"
		if mode == "sequential" && index > 0 {
			status = "pending"
		}
		title := strings.TrimSpace(contract.Title)
		if title == "" || title == "<nil>" {
			title = node.Name
		}
		task := workflowmodel.WorkflowTask{
			ID:                    workflowProcessID(ctx, "task"),
			ProcessID:             process.ID,
			NodeInstanceID:        nodeInstance.ID,
			NodeID:                node.ID,
			Title:                 title,
			AssigneeUserID:        assignee,
			AssigneeRoleKey:       roleKey,
			ResolverSnapshot:      workflowpolicy.WorkflowOrderedApprovalResolvers(contract.Resolvers),
			CandidateSource:       workflowpolicy.WorkflowCandidateSource(workflowpolicy.WorkflowOrderedApprovalResolvers(contract.Resolvers)),
			NodeDefinitionVersion: workflowpolicy.WorkflowGraphContractVersion(process.DefinitionSnapshot),
			Sequence:              index + 1,
			Status:                status,
			DueAt:                 workflowpolicy.WorkflowApprovalDueAt(contract, node, nowTime),
			CreatedAt:             now,
			UpdatedAt:             now,
		}
		assigneeLocale := ""
		if user, ok, getErr := e.runtime.dependencies.Identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(assignee)}); getErr == nil && ok {
			task.AssigneeName = user.Name
			assigneeLocale = user.Locale
		}
		if err := e.insertApprovalTaskWithNotification(ctx, process, task, assigneeLocale); err != nil {
			return 0, internalError("insert workflow task", err)
		}
		if err := e.scheduleApprovalDeadlineTimers(ctx, process, task, contract, nowTime); err != nil {
			return 0, err
		}
		e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, task.ID, "task_created", "system", task.Title, map[string]any{"assignee_user_id": assignee, "mode": mode, "sequence": task.Sequence})
	}
	return len(assignees), nil
}

func (e *WorkflowProcessEngine) scheduleApprovalDeadlineTimers(ctx context.Context, process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask, contract definitionmodel.WorkflowApprovalNodeContract, createdAt time.Time) error {
	if strings.TrimSpace(task.DueAt) == "" || strings.TrimSpace(contract.ReminderActionKey) == "" && contract.EscalationSeconds <= 0 {
		return nil
	}
	if e.runtime.dependencies.ApprovalTimers == nil {
		return internalError("schedule workflow approval deadline", fmt.Errorf("workflow approval timer scheduler is required"))
	}
	dueAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(task.DueAt))
	if err != nil {
		return badRequest("backend.workflow.approval_due_at_invalid", "task", task.ID)
	}
	schedule := func(phase string, at time.Time) error {
		_, scheduleErr := e.runtime.dependencies.ApprovalTimers.ScheduleWorkflowApprovalDeadlineTimer(ctx, WorkflowApprovalDeadlineTimerRequest{
			WorkspaceID: process.WorkspaceID, ProcessID: process.ID, NodeID: task.NodeID, TaskID: task.ID,
			Phase: phase, DueAt: at, CreatedAt: createdAt,
		})
		if scheduleErr != nil {
			return internalError("schedule workflow approval "+phase+" timer", scheduleErr)
		}
		return nil
	}
	if strings.TrimSpace(contract.ReminderActionKey) != "" {
		if err := schedule("reminder", dueAt); err != nil {
			return err
		}
	}
	if contract.EscalationSeconds > 0 {
		if err := schedule("escalation", dueAt.Add(time.Duration(contract.EscalationSeconds)*time.Second)); err != nil {
			return err
		}
	}
	return nil
}

func (e *WorkflowProcessEngine) resolveApprovalAssignees(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, principal principalmodel.Principal) ([]string, string, error) {
	ctx = requestcontext.WithWorkspaceID(ctx, process.WorkspaceID)
	if e.runtime.dependencies.Identity == nil {
		return nil, "", badRequest("backend.workflow.approval_identity_unavailable")
	}
	contract := workflowpolicy.WorkflowApprovalNodeContract(node)
	resolved := []string{}
	roleKey := ""
	for _, resolver := range workflowpolicy.WorkflowOrderedApprovalResolvers(contract.Resolvers) {
		users, resolvedRole, err := e.resolveApprovalAssigneeStrategy(ctx, process, resolver, principal)
		if err != nil {
			return nil, "", err
		}
		if resolvedRole != "" {
			roleKey = resolvedRole
		}
		resolved = append(resolved, users...)
		if strings.TrimSpace(contract.ResolverMode) == "first_match" && len(users) > 0 {
			break
		}
	}
	resolved = workflowpolicy.WorkflowUniqueAssignees(resolved)
	if len(resolved) == 0 && valueOrDefault(strings.TrimSpace(contract.EmptyAssigneePolicy), "fail") == "admin" {
		admins, err := e.usersForApprovalRole(ctx, "admin")
		return admins, "admin", err
	}
	return resolved, roleKey, nil
}

func (e *WorkflowProcessEngine) resolveApprovalAssigneeStrategy(ctx context.Context, process workflowmodel.WorkflowProcessInstance, resolver definitionmodel.WorkflowAssigneeResolver, principal principalmodel.Principal) ([]string, string, error) {
	switch strings.TrimSpace(resolver.Type) {
	case "users":
		return uniqueSortedStrings(resolver.UserIDs), "", nil
	case "manager", "manager_of", "initiator_manager":
		userID := process.InitiatorID
		if resolver.Type != "initiator_manager" {
			field := strings.TrimSpace(resolver.UserField)
			userID = workflowpolicy.WorkflowApprovalSubjectUserID(process.Variables, field)
			if userID == "" {
				return nil, "", nil
			}
		}
		entries, err := e.runtime.dependencies.Identity.ListWorkforce(ctx, identitysdk.DirectoryQuery{})
		if err != nil {
			return nil, "", err
		}
		managerID := ""
		for _, entry := range entries {
			if entry.IdentityUserID == userID {
				managerID = strings.TrimSpace(entry.ManagerIdentityUserID)
				break
			}
		}
		if managerID == "" {
			return nil, "", nil
		}
		manager, managerExists, managerErr := e.runtime.dependencies.Identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(managerID)})
		if managerErr != nil {
			return nil, "", managerErr
		}
		if !managerExists || manager.Status != identitysdk.UserStatusActive {
			return nil, "", nil
		}
		return []string{managerID}, "", nil
	case "record_field":
		field := strings.TrimSpace(resolver.Field)
		userID := workflowpolicy.WorkflowPayloadString(process.Variables, field)
		if userID == "" {
			return nil, "", nil
		}
		return []string{userID}, "", nil
	case "role":
		roleKey := strings.TrimSpace(resolver.RoleKey)
		users, err := e.usersForApprovalRole(ctx, roleKey)
		return users, roleKey, err
	default:
		return nil, "", badRequest("backend.workflow.approval_resolver_invalid", "resolver", resolver.Type)
	}
}

func (e *WorkflowProcessEngine) usersForApprovalRole(ctx context.Context, roleKey string) ([]string, error) {
	roles, err := e.runtime.dependencies.Identity.ListRoles(ctx, identitysdk.DirectoryQuery{})
	if err != nil {
		return nil, err
	}
	roleID := ""
	for _, role := range roles {
		if role.ID == roleKey || role.Key == roleKey {
			roleID = role.ID
			break
		}
	}
	if roleID == "" {
		return nil, badRequest("backend.workflow.approval_role_not_found", "role", roleKey)
	}
	users, err := e.runtime.dependencies.Identity.ListUsers(ctx, identitysdk.DirectoryQuery{})
	if err != nil {
		return nil, err
	}
	assignments, err := e.runtime.dependencies.Identity.ListUserRoleAssignments(ctx, identitysdk.UserRoleAssignmentQuery{})
	if err != nil {
		return nil, err
	}
	usersWithRole := make(map[string]struct{})
	for _, assignment := range assignments {
		if assignment.RoleID == roleID {
			usersWithRole[assignment.UserID] = struct{}{}
		}
	}
	out := []string{}
	for _, user := range users {
		if user.Status != identitysdk.UserStatusActive {
			continue
		}
		if _, ok := usersWithRole[user.ID]; ok {
			out = append(out, user.ID)
		}
	}
	return uniqueSortedStrings(out), nil
}

func (e *WorkflowProcessEngine) aggregateApproval(ctx context.Context, process workflowmodel.WorkflowProcessInstance, nodeID string, principal principalmodel.Principal) (string, bool, error) {
	tasks, err := e.runtime.dependencies.Processes.ListTasks(ctx, process.WorkspaceID, process.ID, "", "", 500)
	if err != nil {
		return "", false, internalError("list approval tasks", err)
	}
	nodeTasks := []workflowmodel.WorkflowTask{}
	for _, task := range tasks {
		if task.NodeID == nodeID {
			nodeTasks = append(nodeTasks, task)
		}
	}
	if len(nodeTasks) == 0 {
		return "", false, badRequest("backend.workflow.approval_tasks_missing")
	}
	sort.Slice(nodeTasks, func(i, j int) bool { return nodeTasks[i].Sequence < nodeTasks[j].Sequence })
	for _, task := range nodeTasks {
		if task.Status == "returned" {
			e.cancelUnfinishedApprovalTasks(ctx, nodeTasks, task.ID, principal.UserID)
			return "returned", true, nil
		}
		if task.Status == "rejected" {
			e.cancelUnfinishedApprovalTasks(ctx, nodeTasks, task.ID, principal.UserID)
			return "rejected", true, nil
		}
	}
	node, _ := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, nodeID)
	mode := valueOrDefault(strings.TrimSpace(workflowpolicy.WorkflowApprovalNodeContract(node).Mode), "any")
	approved := 0
	for _, task := range nodeTasks {
		if task.Status == "approved" {
			approved++
		}
	}
	switch mode {
	case "any":
		if approved > 0 {
			e.cancelUnfinishedApprovalTasks(ctx, nodeTasks, "", principal.UserID)
			return "approved", true, nil
		}
	case "all":
		return "approved", approved == len(nodeTasks), nil
	case "sequential":
		if approved == len(nodeTasks) {
			return "approved", true, nil
		}
		for _, task := range nodeTasks {
			if task.Status == "pending" {
				task.Status = "open"
				task.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
				if err := e.openSequentialApprovalTask(ctx, process, task); err != nil {
					return "", false, internalError("open sequential approval task", err)
				}
				e.appendEvent(ctx, process.WorkspaceID, process.ID, nodeID, task.ID, "task_opened", "system", task.Title, nil)
				break
			}
		}
	}
	return "", false, nil
}

func (e *WorkflowProcessEngine) completeApprovalNode(ctx context.Context, workspaceID, processID, nodeID, outcome, actorID string) error {
	nodes, err := e.runtime.dependencies.Processes.ListNodes(ctx, workspaceID, processID)
	if err != nil {
		return internalError("list workflow nodes", err)
	}
	for _, node := range nodes {
		if node.NodeID != nodeID || node.Status != "waiting" {
			continue
		}
		node.Status = outcome
		node.Output = map[string]any{"decision": outcome}
		node.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := e.runtime.dependencies.Processes.UpdateNode(ctx, workspaceID, node); err != nil {
			return internalError("complete approval node", err)
		}
		e.appendEvent(ctx, workspaceID, processID, nodeID, "", "approval_"+outcome, actorID, "workflow.event.approval."+outcome, nil)
		return nil
	}
	return notFound("backend.workflow.approval_node_instance_not_found")
}

func runCommittedWorkflowContinuation(ctx context.Context, records *WorkflowProcessRuntime, execution workflowmodel.WorkflowExecution, principal principalmodel.Principal, processSnapshot workflowmodel.WorkflowProcessInstance, nodeSnapshots []workflowmodel.WorkflowNodeInstance, taskSnapshots []workflowmodel.WorkflowTask, decidedTask workflowmodel.WorkflowTask) (workflowmodel.WorkflowProcessInstance, bool, error) {
	claimed := execution
	claimed.Status, claimed.UpdatedAt = "running", time.Now().UTC().Format(time.RFC3339Nano)
	updated, err := records.dependencies.Workers.UpdateExecutionWhere(ctx, execution.WorkspaceID, claimed, map[string]any{"status": "pending", "updated_at": execution.UpdatedAt})
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, false, internalError("claim workflow continuation", err)
	}
	if !updated {
		return workflowmodel.WorkflowProcessInstance{}, false, nil
	}
	process, found, err := records.dependencies.Workers.GetProcess(ctx, execution.WorkspaceID, execution.ProcessID)
	if err != nil || !found {
		return workflowmodel.WorkflowProcessInstance{}, true, internalError("load workflow continuation process", err)
	}
	nodeIDs := workflowNodeIDsFromAny(execution.Result["resume_node_ids"])
	if len(nodeIDs) == 0 {
		nodeIDs = append([]string(nil), process.CurrentNodeIDs...)
	}
	continued, runErr := records.processEngine.runWithContext(ctx, process, nodeIDs, nil, principal)
	claimed.Attempt++
	claimed.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	claimed.Result = workflowpolicy.WorkflowCloneMap(claimed.Result)
	if runErr != nil {
		workflow, _ := records.dependencies.WorkflowRegistry.Get(execution.WorkflowKey)
		workflowpolicy.WorkflowMarkFailed(&claimed, workflow, runErr, time.Now().UTC())
		recoveredExecution := execution
		recoveredExecution.Status, recoveredExecution.Message, recoveredExecution.LastError = "waiting", "workflow.message.approvalWaiting", ""
		recoveredExecution.UpdatedAt, recoveredExecution.NextRunAt = time.Now().UTC().Format(time.RFC3339Nano), ""
		recoveredExecution.Result = workflowpolicy.WorkflowCloneMap(recoveredExecution.Result)
		recoveredExecution.Result["process_status"], recoveredExecution.Result["current_node_ids"] = processSnapshot.Status, processSnapshot.CurrentNodeIDs
		delete(recoveredExecution.Result, "resume_node_ids")
		recovered, recoveryErr := records.processEngine.recoverWorkflowDecision(ctx, processSnapshot, nodeSnapshots, taskSnapshots, decidedTask, runErr, principal.UserID, &recoveredExecution)
		return recovered, true, recoveryErr
	}
	claimed.Status, claimed.Message, claimed.LastError, claimed.NextRunAt = continued.Status, "workflow.message.continuationCompleted", "", ""
	claimed.NodeID = ""
	if len(continued.CurrentNodeIDs) > 0 {
		claimed.NodeID = continued.CurrentNodeIDs[0]
	}
	claimed.Result["process_status"], claimed.Result["current_node_ids"] = continued.Status, continued.CurrentNodeIDs
	delete(claimed.Result, "resume_node_ids")
	if err := records.dependencies.Workers.UpdateExecution(ctx, execution.WorkspaceID, claimed); err != nil {
		return continued, true, internalError("complete workflow continuation", err)
	}
	return continued, true, nil
}
