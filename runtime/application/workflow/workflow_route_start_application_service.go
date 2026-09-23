package workflow

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/requestcontext"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

// workflowRouteStartError is the project-visible failure of a staged Workflow
// start. Every rejection names the exact authored position so a Handler can
// map it back onto its own input without guessing.
func workflowRouteStartError(code, fieldPath string, parameters map[string]string) error {
	params := map[string]string{"field_path": fieldPath}
	for key, value := range parameters {
		params[key] = value
	}
	return &runtimeext.BusinessError{Code: code, Message: code, Parameters: params}
}

// StageWorkflowRouteStart validates one Action-staged Workflow start against
// the published definition and the live Identity projection and returns the
// durable rows the Action transaction must write. Nothing is persisted here:
// the Action Unit of Work owns the commit boundary.
func (s *WorkflowApplicationService) StageWorkflowRouteStart(ctx context.Context, request workflowmodel.WorkflowRouteStartRequest, principal principalmodel.Principal) (transactionmodel.WorkflowStartCommit, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return transactionmodel.WorkflowStartCommit{}, err
	}
	workflowKey := strings.TrimSpace(request.WorkflowKey)
	if !workflowRouteStartGranted(request.GrantedWorkflowKeys, workflowKey) {
		return transactionmodel.WorkflowStartCommit{}, workflowRouteStartError(runtimeext.WorkflowGrantDeniedErrorCode, "workflow_key", map[string]string{"workflow": workflowKey})
	}
	workflow, ok := s.registry.Get(workflowKey)
	if !ok || !workflow.Enabled {
		return transactionmodel.WorkflowStartCommit{}, workflowRouteStartError("backend.workflow.not_found", "workflow_key", map[string]string{"workflow": workflowKey})
	}
	node, route, err := workflowRouteApprovalNode(workflow)
	if err != nil {
		return transactionmodel.WorkflowStartCommit{}, err
	}
	recordID := strings.TrimSpace(request.RecordID)
	if recordID == "" {
		return transactionmodel.WorkflowStartCommit{}, workflowRouteStartError("backend.workflow.route_record_required", "record_id", map[string]string{"workflow": workflowKey})
	}
	if len(request.Steps) < route.MinSteps || len(request.Steps) > route.MaxSteps {
		return transactionmodel.WorkflowStartCommit{}, workflowRouteStartError("backend.workflow.route_step_count_invalid", "route.steps", map[string]string{
			"workflow": workflowKey, "min": fmt.Sprint(route.MinSteps), "max": fmt.Sprint(route.MaxSteps), "actual": fmt.Sprint(len(request.Steps)),
		})
	}
	electorate, err := s.workflowRouteEligibleUsers(ctx, workflow, route, principal)
	if err != nil {
		return transactionmodel.WorkflowStartCommit{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	process := s.workflowRouteStartProcess(ctx, workflow, request, recordID, principal, now)
	steps := make([]workflowmodel.WorkflowRouteStep, 0, len(request.Steps))
	stepKeys := map[string]bool{}
	for index, authored := range request.Steps {
		step, err := workflowRouteStartStep(index, authored, node, route, electorate, "route.steps", now)
		if err != nil {
			return transactionmodel.WorkflowStartCommit{}, err
		}
		if stepKeys[step.StepKey] {
			return transactionmodel.WorkflowStartCommit{}, workflowRouteStartError("backend.workflow.route_step_key_duplicate", fmt.Sprintf("route.steps[%d].step_key", index), map[string]string{"step_key": step.StepKey})
		}
		stepKeys[step.StepKey] = true
		step.WorkspaceID, step.ProcessID = principal.WorkspaceID, process.ID
		step.ID = fmt.Sprintf("%s_step_%d", process.ID, step.StepNo)
		steps = append(steps, step)
	}
	intent := workflowRouteStartIntent(workflow, process, principal, now)
	return transactionmodel.WorkflowStartCommit{Process: process, RouteSteps: steps, Intent: intent}, nil
}

func workflowRouteStartGranted(granted []string, workflowKey string) bool {
	if strings.TrimSpace(workflowKey) == "" {
		return false
	}
	for _, value := range granted {
		if strings.TrimSpace(value) == workflowKey {
			return true
		}
	}
	return false
}

// workflowRouteApprovalNode is the single approval node the staged route
// configures. A workflow without exactly one instance-routed approval node
// cannot accept a Handler route.
func workflowRouteApprovalNode(workflow definitionmodel.WorkflowSchema) (definitionmodel.WorkflowGraphNode, definitionmodel.WorkflowApprovalRouteContract, error) {
	if workflow.Graph == nil {
		return definitionmodel.WorkflowGraphNode{}, definitionmodel.WorkflowApprovalRouteContract{}, workflowRouteStartError("backend.workflow.graph_v2_required", "workflow_key", map[string]string{"workflow": workflow.Key})
	}
	for _, node := range workflow.Graph.Nodes {
		if node.Type != "approval" {
			continue
		}
		route, ok := workflowpolicy.WorkflowApprovalRoute(node)
		if ok && route.Source == workflowpolicy.WorkflowApprovalRouteSourceInstance {
			return node, route, nil
		}
	}
	return definitionmodel.WorkflowGraphNode{}, definitionmodel.WorkflowApprovalRouteContract{}, workflowRouteStartError("backend.workflow.route_not_supported", "workflow_key", map[string]string{"workflow": workflow.Key})
}

// workflowRouteEligibleUsers is the live electorate a staged step may draw
// from: the users holding one of the route's eligible roles, or — when the
// route names none — the users who may approve this workflow at all.
func (s *WorkflowApplicationService) workflowRouteEligibleUsers(ctx context.Context, workflow definitionmodel.WorkflowSchema, route definitionmodel.WorkflowApprovalRouteContract, principal principalmodel.Principal) (WorkflowRouteElectorate, error) {
	_ = principal
	return workflowRouteEligibleAssignees(ctx, s.identity, workflow, route)
}

// WorkflowRouteElectorate separates the three reasons a named approver cannot
// carry a route step, so a Handler is told which of them applies instead of
// one opaque rejection.
type WorkflowRouteElectorate struct {
	Known    map[string]bool
	Active   map[string]bool
	Eligible map[string]workflowmodel.WorkflowRouteAssignee
}

func workflowRouteEligibleAssignees(ctx context.Context, identity identitysdk.Projection, workflow definitionmodel.WorkflowSchema, route definitionmodel.WorkflowApprovalRouteContract) (WorkflowRouteElectorate, error) {
	electorate := WorkflowRouteElectorate{Known: map[string]bool{}, Active: map[string]bool{}, Eligible: map[string]workflowmodel.WorkflowRouteAssignee{}}
	if identity == nil {
		return electorate, workflowRouteStartError("backend.workflow.approval_identity_unavailable", "route.steps", nil)
	}
	users, err := identity.ListUsers(ctx, identitysdk.ProjectionQuery{})
	if err != nil {
		return electorate, internalError("list workflow route users", err)
	}
	active := make(map[string]identitysdk.User, len(users))
	for _, user := range users {
		electorate.Known[user.ID] = true
		if user.Status != identitysdk.UserStatusActive {
			continue
		}
		electorate.Active[user.ID] = true
		active[user.ID] = user
	}
	roleKeys := route.EligibleRoles
	if len(roleKeys) == 0 {
		roleKeys = workflowRouteApprovePermissionRoles(workflow)
	}
	if len(roleKeys) == 0 {
		for _, user := range active {
			electorate.Eligible[user.ID] = workflowmodel.WorkflowRouteAssignee{UserID: user.ID, DisplayName: user.Name}
		}
		return electorate, nil
	}
	roles, err := identity.ListRoles(ctx, identitysdk.ProjectionQuery{})
	if err != nil {
		return electorate, internalError("list workflow route roles", err)
	}
	roleByID := map[string]string{}
	for _, role := range roles {
		for _, wanted := range roleKeys {
			if role.ID == wanted || role.Key == wanted {
				roleByID[role.ID] = valueOrDefault(strings.TrimSpace(role.Key), role.ID)
			}
		}
	}
	assignments, err := identity.ListUserRoleAssignments(ctx, identitysdk.UserRoleAssignmentQuery{})
	if err != nil {
		return electorate, internalError("list workflow route role assignments", err)
	}
	for _, assignment := range assignments {
		roleKey, held := roleByID[assignment.RoleID]
		if !held {
			continue
		}
		user, usable := active[assignment.UserID]
		if !usable {
			continue
		}
		electorate.Eligible[assignment.UserID] = workflowmodel.WorkflowRouteAssignee{UserID: user.ID, DisplayName: user.Name, RoleKey: roleKey}
	}
	return electorate, nil
}

// workflowRouteApprovePermissionRoles falls back to the roles the workflow
// itself names as approvers when the route declares no eligible role.
func workflowRouteApprovePermissionRoles(workflow definitionmodel.WorkflowSchema) []string {
	if workflow.Graph == nil {
		return nil
	}
	keys := []string{}
	for _, node := range workflow.Graph.Nodes {
		if node.Type != "approval" {
			continue
		}
		for _, resolver := range workflowpolicy.WorkflowApprovalNodeContract(node).Resolvers {
			if roleKey := strings.TrimSpace(resolver.RoleKey); roleKey != "" {
				keys = append(keys, roleKey)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// WorkflowValidateRouteStepAssignees is shared by the staged start and the
// next-step configuration on a decision so both surfaces reject exactly the
// same electorate with the same codes.
func WorkflowValidateRouteStepAssignees(userIDs []string, route definitionmodel.WorkflowApprovalRouteContract, electorate WorkflowRouteElectorate, fieldPrefix string) ([]workflowmodel.WorkflowRouteAssignee, error) {
	if len(userIDs) > route.MaxAssigneesPerStep {
		return nil, workflowRouteStartError("backend.workflow.route_step_count_invalid", fieldPrefix, map[string]string{"max": fmt.Sprint(route.MaxAssigneesPerStep), "actual": fmt.Sprint(len(userIDs))})
	}
	assignees := make([]workflowmodel.WorkflowRouteAssignee, 0, len(userIDs))
	seen := map[string]bool{}
	for index, raw := range userIDs {
		fieldPath := fmt.Sprintf("%s[%d]", fieldPrefix, index)
		userID := strings.TrimSpace(raw)
		if userID == "" {
			return nil, workflowRouteStartError("backend.workflow.route_assignee_not_found", fieldPath, nil)
		}
		if seen[userID] {
			return nil, workflowRouteStartError("backend.workflow.route_assignee_duplicate", fieldPath, map[string]string{"user_id": userID})
		}
		seen[userID] = true
		if !electorate.Known[userID] {
			return nil, workflowRouteStartError("backend.workflow.route_assignee_not_found", fieldPath, map[string]string{"user_id": userID})
		}
		if !electorate.Active[userID] {
			return nil, workflowRouteStartError("backend.workflow.route_assignee_inactive", fieldPath, map[string]string{"user_id": userID})
		}
		assignee, eligibleUser := electorate.Eligible[userID]
		if !eligibleUser {
			return nil, workflowRouteStartError("backend.workflow.route_assignee_not_eligible", fieldPath, map[string]string{"user_id": userID})
		}
		assignees = append(assignees, assignee)
	}
	return assignees, nil
}

func workflowRouteStartStep(index int, authored workflowmodel.WorkflowRouteStartStep, node definitionmodel.WorkflowGraphNode, route definitionmodel.WorkflowApprovalRouteContract, electorate WorkflowRouteElectorate, fieldPrefix, now string) (workflowmodel.WorkflowRouteStep, error) {
	fieldPath := fmt.Sprintf("%s[%d]", fieldPrefix, index)
	step := workflowmodel.WorkflowRouteStep{
		NodeID: node.ID, StepNo: index + 1, StepKey: strings.TrimSpace(authored.StepKey),
		Title:  valueOrDefault(strings.TrimSpace(authored.Title), valueOrDefault(strings.TrimSpace(workflowpolicy.WorkflowApprovalNodeContract(node).Title), node.Name)),
		Mode:   workflowpolicy.WorkflowRouteStepMode(authored.Mode),
		Status: "pending", CreatedAt: now, UpdatedAt: now,
	}
	if step.StepKey == "" {
		step.StepKey = fmt.Sprintf("s%d", step.StepNo)
	}
	if authored.Deferred {
		if route.DeferredSteps != "allow" || index == 0 || len(authored.AssigneeUserIDs) > 0 {
			return workflowmodel.WorkflowRouteStep{}, workflowRouteStartError("backend.workflow.route_deferred_not_allowed", fieldPath+".deferred", map[string]string{"step_key": step.StepKey})
		}
		step.Status = "configurable"
		step.RequiredApprovals = authored.RequiredApprovals
		return step, nil
	}
	assignees, err := WorkflowValidateRouteStepAssignees(authored.AssigneeUserIDs, route, electorate, fieldPath+".assignees")
	if err != nil {
		return workflowmodel.WorkflowRouteStep{}, err
	}
	if len(assignees) == 0 {
		return workflowmodel.WorkflowRouteStep{}, workflowRouteStartError("backend.workflow.route_step_count_invalid", fieldPath+".assignees", map[string]string{"min": "1", "actual": "0"})
	}
	step.AssigneeSnapshot = assignees
	required, err := WorkflowRouteRequiredApprovals(step.Mode, authored.RequiredApprovals, len(assignees), fieldPath+".required_approvals")
	if err != nil {
		return workflowmodel.WorkflowRouteStep{}, err
	}
	step.RequiredApprovals = required
	return step, nil
}

// WorkflowRouteRequiredApprovals normalizes an authored threshold: quorum must
// stay inside [1, assignees], all is forced to the whole electorate and any
// always completes on the first approval.
func WorkflowRouteRequiredApprovals(mode string, authored, assignees int, fieldPath string) (int, error) {
	switch workflowpolicy.WorkflowRouteStepMode(mode) {
	case "quorum":
		if authored < 1 || authored > assignees {
			return 0, workflowRouteStartError("backend.workflow.route_required_approvals_invalid", fieldPath, map[string]string{"min": "1", "max": fmt.Sprint(assignees), "actual": fmt.Sprint(authored)})
		}
		return authored, nil
	case "all":
		if authored != 0 && authored != assignees {
			return 0, workflowRouteStartError("backend.workflow.route_required_approvals_invalid", fieldPath, map[string]string{"expected": fmt.Sprint(assignees), "actual": fmt.Sprint(authored)})
		}
		return assignees, nil
	default:
		if authored != 0 && authored != 1 {
			return 0, workflowRouteStartError("backend.workflow.route_required_approvals_invalid", fieldPath, map[string]string{"expected": "1", "actual": fmt.Sprint(authored)})
		}
		return 1, nil
	}
}

func (s *WorkflowApplicationService) workflowRouteStartProcess(ctx context.Context, workflow definitionmodel.WorkflowSchema, request workflowmodel.WorkflowRouteStartRequest, recordID string, principal principalmodel.Principal, now string) workflowmodel.WorkflowProcessInstance {
	variables := workflowpolicy.WorkflowCloneMap(request.Variables)
	objectKey := strings.TrimSpace(request.ObjectKey)
	if objectKey == "" && workflow.TriggerContract != nil {
		objectKey = strings.TrimSpace(workflow.TriggerContract.ObjectKey)
	}
	variables["object_key"], variables["record_id"] = objectKey, recordID
	if principal.UserID != "" {
		variables["initiating_user_id"] = principal.UserID
	}
	if principal.RoleKey != "" {
		variables["initiating_role_key"] = principal.RoleKey
	}
	return workflowmodel.WorkflowProcessInstance{
		WorkspaceID: principal.WorkspaceID, ID: workflowRouteStartProcessID(ctx, request),
		OperationID: requestcontext.OwnerExecutionID(ctx),
		WorkflowKey: workflow.Key, WorkflowName: workflow.Name,
		DefinitionVersionID: workflow.DefinitionVersionID, DefinitionVersion: workflowpolicy.WorkflowPublishedVersion(workflow),
		DefinitionHash: workflowpolicy.WorkflowDefinitionHash(workflow), DefinitionSnapshot: workflow,
		ObjectKey: objectKey, RecordID: recordID, InitiatorID: principal.UserID, InitiatorRoleKey: principal.RoleKey,
		Status: "starting", Variables: variables, Result: map[string]any{}, CreatedAt: now, UpdatedAt: now,
	}
}

// workflowRouteStartProcessID is derived from the Action execution so a
// retried Action invocation stages the identical process identity instead of
// leaking a second starting process.
func workflowRouteStartProcessID(ctx context.Context, request workflowmodel.WorkflowRouteStartRequest) string {
	executionID := strings.TrimSpace(request.ExecutionID)
	if executionID == "" {
		return workflowProcessID(ctx, "process")
	}
	return fmt.Sprintf("process_%s_%d", executionID, request.StageIndex)
}

func workflowRouteStartIntent(workflow definitionmodel.WorkflowSchema, process workflowmodel.WorkflowProcessInstance, principal principalmodel.Principal, now string) workflowmodel.WorkflowExecution {
	payload := workflowpolicy.WorkflowCloneMap(process.Variables)
	payload["process_id"] = process.ID
	return workflowmodel.WorkflowExecution{
		WorkspaceID: process.WorkspaceID, ID: "intent_" + process.ID, WorkflowKey: workflow.Key, Name: workflow.Name,
		OperationID: process.OperationID,
		Trigger:     WorkflowRouteStartTrigger, Status: "pending", ActionType: "workflow_graph",
		Action: workflowpolicy.WorkflowCloneMap(workflow.Action), Payload: payload,
		Result:    map[string]any{"transactional_intent": true, "route_start": true, "resume_process_id": process.ID},
		ProcessID: process.ID, ObjectKey: process.ObjectKey, RecordID: process.RecordID, ActorID: principal.UserID,
		RunAs: workflowpolicy.WorkflowRunAs(workflow), Attempt: 0, MaxAttempts: workflowpolicy.WorkflowMaxAttempts(workflow),
		Message: "workflow.message.queued", CreatedAt: now, UpdatedAt: now,
	}
}

// WorkflowRouteStartTrigger marks an intent that must activate an existing
// starting process instead of executing the workflow from its payload.
const WorkflowRouteStartTrigger = "workflow_route_start"
