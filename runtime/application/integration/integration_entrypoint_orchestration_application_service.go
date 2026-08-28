package integration

import actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"

// This file exposes cross-owner Integration use-case entrypoints.

import (
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	"context"
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"
	apperrorbusiness "github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type IntegrationWorkflowRunResult struct {
	ExternalIdentity integrationmodel.IntegrationExternalIdentityResolveResult `json:"external_identity"`
	Workflow         workflowmodel.WorkflowRunResult                           `json:"workflow"`
}

type IntegrationActionExecutionResult struct {
	ExternalIdentity integrationmodel.IntegrationExternalIdentityResolveResult `json:"external_identity"`
	Action           actionmodel.ActionResult                                  `json:"action"`
}

func (s *IntegrationApplicationService) ExecuteIntegrationAction(ctx context.Context, objectKey, recordID, actionKey string, req integrationmodel.IntegrationEntrypointActionRequest, principal principalmodel.Principal) (IntegrationActionExecutionResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return IntegrationActionExecutionResult{}, err
	}
	resolved, resolvedPrincipal, err := s.ResolveIntegrationExternalIdentity(ctx, req.ExternalIdentity, principal)
	if err != nil {
		return IntegrationActionExecutionResult{}, err
	}
	// External identity is authorization and audit context. It must not be
	// injected as undeclared fields into the Action's strict typed input.
	data := cloneMap(req.Data)
	invocation, err := s.invokeAction(ctx, actionmodel.ActionInvocation{
		ActionKey: actionKey, ObjectKey: objectKey, RecordID: recordID, Input: data, Principal: resolvedPrincipal,
		Source: actionmodel.ActionSourceIntegration, IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
	})
	if err != nil {
		s.audit(ctx, "integration_entrypoint_action_denied", strings.TrimSpace(objectKey), strings.TrimSpace(recordID), resolvedPrincipal, "Integration action entrypoint denied "+strings.TrimSpace(actionKey), nil, nil, integrationprojection.IntegrationEntrypointAuditMetadata(resolved, map[string]any{
			"object_key": strings.TrimSpace(objectKey),
			"record_id":  strings.TrimSpace(recordID),
			"action_key": strings.TrimSpace(actionKey),
			"error_code": stableIntegrationFailureCode(err, "backend.integration.entrypoint.action_denied"),
		}))
		return IntegrationActionExecutionResult{}, err
	}
	result := *invocation.Record
	s.audit(ctx, "integration_entrypoint_action_executed", result.ObjectKey, result.RecordID, resolvedPrincipal, "Integration action entrypoint executed "+result.ActionKey, nil, nil, integrationprojection.IntegrationEntrypointAuditMetadata(resolved, map[string]any{
		"object_key":     result.ObjectKey,
		"record_id":      result.RecordID,
		"action_key":     result.ActionKey,
		"workflow_count": len(result.TriggeredWorkflows),
	}))
	return IntegrationActionExecutionResult{ExternalIdentity: resolved, Action: result}, nil
}

func (s *IntegrationApplicationService) InvokeIntegrationAgentTool(ctx context.Context, agentKey string, toolKey string, req integrationmodel.IntegrationAgentToolInvocationRequest, principal principalmodel.Principal) (integrationmodel.IntegrationAgentToolInvocationResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationAgentToolInvocationResult{}, err
	}
	if !CanInvokeEntrypoint(principal) {
		return integrationmodel.IntegrationAgentToolInvocationResult{}, forbidden("auth.permission_denied")
	}
	agentKey = strings.TrimSpace(agentKey)
	toolKey = strings.TrimSpace(toolKey)
	if agentKey == "" || toolKey == "" {
		return integrationmodel.IntegrationAgentToolInvocationResult{}, badRequest("backend.integration.agent_tool.missing_identity")
	}
	resolved, resolvedPrincipal, err := s.ResolveIntegrationExternalIdentity(ctx, req.ExternalIdentity, principal)
	if err != nil {
		return integrationmodel.IntegrationAgentToolInvocationResult{}, err
	}
	snapshot := s.schema(ctx, resolvedPrincipal)
	var agent agentmodel.AgentSchema
	found := false
	for _, candidate := range snapshot.Agents {
		if candidate.Key == agentKey {
			agent = candidate
			found = true
			break
		}
	}
	if !found || !AgentAllowsTool(agent, toolKey) {
		s.audit(ctx, "integration_agent_tool_denied", "integration_agent", agentKey, resolvedPrincipal, "backend.integration.agent_tool.not_allowed", nil, nil, integrationprojection.IntegrationEntrypointAuditMetadata(resolved, map[string]any{
			"workspace_id": principalWorkspaceID(resolvedPrincipal),
			"agent_key":    agentKey,
			"tool_key":     toolKey,
			"reason":       "not_allowed",
		}))
		return integrationmodel.IntegrationAgentToolInvocationResult{}, forbidden("backend.integration.agent_tool.not_allowed")
	}
	guardedWrites := make([]AgentToolGuardedWrite, 0, len(snapshot.GuardedWrites))
	for _, contract := range snapshot.GuardedWrites {
		guardedWrites = append(guardedWrites, AgentToolGuardedWrite{ObjectKey: contract.ObjectKey, Operation: contract.Operation, ActionKey: contract.ActionKey, Endpoint: contract.Endpoint})
	}
	riskDecision, err := AssessAgentToolRiskForGuardedWrites(toolKey, req.Input, snapshot.Integrations, guardedWrites)
	if err != nil {
		s.audit(ctx, "integration_agent_tool_denied", "integration_agent", agentKey, resolvedPrincipal, "backend.integration.agent_tool.denied_by_policy", nil, nil, integrationprojection.IntegrationEntrypointAuditMetadata(resolved, map[string]any{
			"workspace_id":      principalWorkspaceID(resolvedPrincipal),
			"agent_key":         agentKey,
			"tool_key":          toolKey,
			"connector_key":     riskDecision.ConnectorKey,
			"risk_level":        riskDecision.RiskLevel,
			"policy":            riskDecision.Policy,
			"reason":            apperrorbusiness.CodeOf(err),
			"requires_approval": riskDecision.RequiresApproval,
		}))
		return integrationmodel.IntegrationAgentToolInvocationResult{}, err
	}
	status := "prepared"
	errorText := ""
	if riskDecision.RequiresApproval && !req.Approved {
		status = "approval_required"
		errorText = "backend.integration.agent_tool.approval_required"
	}
	approvalPlan := AgentToolApprovalPlan(agentKey, toolKey, req, resolved, resolvedPrincipal, riskDecision, time.Now())
	metadata := auditapplication.AuditRedactSensitiveMap(recordvalidation.RecordCloneData(req.Input))
	if principal.RequestID != "" {
		metadata["request_id"] = principal.RequestID
	}
	for key, value := range integrationprojection.IntegrationEntrypointAuditMetadata(resolved, nil) {
		metadata[key] = value
	}
	invocation := integrationmodel.IntegrationInvocation{
		WorkspaceID:  principalWorkspaceID(resolvedPrincipal),
		ConnectorKey: "agent_tool",
		Operation:    "agent." + agentKey + "." + toolKey,
		Status:       status,
		RequestRef:   strings.TrimSpace(req.RequestRef),
		Error:        errorText,
		Metadata:     metadata,
	}
	invocation.Metadata["agent_key"] = agentKey
	invocation.Metadata["tool_key"] = toolKey
	invocation.Metadata["approved"] = req.Approved
	invocation.Metadata["actor_id"] = resolvedPrincipal.UserID
	invocation.Metadata["role_key"] = resolvedPrincipal.RoleKey
	invocation.Metadata["risk_level"] = riskDecision.RiskLevel
	invocation.Metadata["risk_policy"] = riskDecision.Policy
	invocation.Metadata["requires_approval"] = riskDecision.RequiresApproval
	if approvalPlan != nil {
		invocation.Metadata["approval_queue"] = approvalPlan["queue_key"]
		invocation.Metadata["approval_expires_at"] = approvalPlan["expires_at"]
	}
	if riskDecision.ConnectorKey != "" {
		invocation.Metadata["connector_key"] = riskDecision.ConnectorKey
	}
	var executionErr error
	if req.Approved {
		toolResult, executeErr := s.executeIntegrationAgentRecordTool(ctx, toolKey, req.Input, resolvedPrincipal)
		if executeErr != nil {
			executionErr = executeErr
			invocation.Status = "failed"
			invocation.Error = executeErr.Error()
			invocation.Metadata["execution_error_code"] = apperrorbusiness.CodeOf(executeErr)
		} else if toolResult != nil {
			invocation.Status = "executed"
			invocation.Metadata["result"] = toolResult
		}
		status = invocation.Status
	}
	saved, err := s.deliveryRepo.InsertInvocation(ctx, invocation.WorkspaceID, invocation)
	if err != nil {
		return integrationmodel.IntegrationAgentToolInvocationResult{}, err
	}
	s.audit(ctx, "integration_agent_tool_invoked", "integration_agent", agentKey, resolvedPrincipal, "backend.integration.agent_tool.prepared", nil, integrationprojection.IntegrationInvocationAuditShape(saved), integrationprojection.IntegrationEntrypointAuditMetadata(resolved, map[string]any{
		"workspace_id":  principalWorkspaceID(resolvedPrincipal),
		"agent_key":     agentKey,
		"tool_key":      toolKey,
		"status":        status,
		"approved":      req.Approved,
		"connector_key": riskDecision.ConnectorKey,
		"risk_level":    riskDecision.RiskLevel,
		"risk_policy":   riskDecision.Policy,
	}))
	if executionErr != nil {
		return integrationmodel.IntegrationAgentToolInvocationResult{}, executionErr
	}
	return integrationmodel.IntegrationAgentToolInvocationResult{Agent: agent, Tool: toolKey, Status: status, ExternalIdentity: resolved, ActionInvocation: saved, ApprovalPlan: approvalPlan}, nil
}

func (s *IntegrationApplicationService) executeIntegrationAgentRecordTool(ctx context.Context, toolKey string, input map[string]any, principal principalmodel.Principal) (map[string]any, error) {
	objectKey := AgentToolObjectKey(input)
	switch strings.TrimSpace(toolKey) {
	case "createRecord":
		record, err := s.recordsApp.CreateRecord(ctx, objectKey, integrationMapFromAny(input["data"]), principal)
		if err != nil {
			return nil, err
		}
		return map[string]any{"object_key": objectKey, "record_id": record.ID, "operation": "create"}, nil
	case "updateRecord":
		recordID := strings.TrimSpace(fmt.Sprint(integrationFirstNonNil(input["record_id"], input["id"])))
		record, err := s.recordsApp.UpdateRecord(ctx, objectKey, recordID, integrationMapFromAny(integrationFirstNonNil(input["patch"], input["data"])), principal)
		if err != nil {
			return nil, err
		}
		return map[string]any{"object_key": objectKey, "record_id": record.ID, "operation": "update"}, nil
	case "deleteRecord":
		recordID := strings.TrimSpace(fmt.Sprint(integrationFirstNonNil(input["record_id"], input["id"])))
		if err := s.recordsApp.DeleteRecord(ctx, objectKey, recordID, principal); err != nil {
			return nil, err
		}
		return map[string]any{"object_key": objectKey, "record_id": recordID, "operation": "delete"}, nil
	default:
		return nil, nil
	}
}

func integrationFirstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (s *IntegrationApplicationService) RunIntegrationWorkflow(ctx context.Context, workflowKey string, req integrationmodel.IntegrationEntrypointWorkflowRequest, principal principalmodel.Principal) (IntegrationWorkflowRunResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return IntegrationWorkflowRunResult{}, err
	}
	resolved, resolvedPrincipal, err := s.ResolveIntegrationExternalIdentity(ctx, req.ExternalIdentity, principal)
	if err != nil {
		return IntegrationWorkflowRunResult{}, err
	}
	payload := integrationprojection.IntegrationEntrypointPayload(req.Payload, resolved)
	result, err := s.workflows.RunIntegrationWorkflow(ctx, workflowKey, payload, resolvedPrincipal)
	if err != nil {
		s.audit(ctx, "integration_entrypoint_workflow_denied", "integration_workflow", strings.TrimSpace(workflowKey), resolvedPrincipal, "Integration workflow entrypoint denied "+strings.TrimSpace(workflowKey), nil, nil, integrationprojection.IntegrationEntrypointAuditMetadata(resolved, map[string]any{
			"workflow_key": strings.TrimSpace(workflowKey),
			"error_code":   stableIntegrationFailureCode(err, "backend.integration.entrypoint.workflow_denied"),
		}))
		return IntegrationWorkflowRunResult{}, err
	}
	s.audit(ctx, "integration_entrypoint_workflow_executed", "integration_workflow", result.WorkflowKey, resolvedPrincipal, "Integration workflow entrypoint executed "+result.WorkflowKey, nil, nil, integrationprojection.IntegrationEntrypointAuditMetadata(resolved, map[string]any{
		"workflow_key": result.WorkflowKey,
		"status":       result.Status,
	}))
	return IntegrationWorkflowRunResult{ExternalIdentity: resolved, Workflow: result}, nil
}
