package workflow

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	invocationcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/invocation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowExecutionReceiptReader interface {
	FindExecutionReceipt(context.Context, string, string, string) (workflowmodel.WorkflowExecutionReceipt, bool, error)
}

// AgentWorkflowDefinition resolves the current executable definition. Schema
// discovery alone cannot authorize a start or substitute a stale definition.
func (s *WorkflowApplicationService) AgentWorkflowDefinition(ctx context.Context, key string, principal principalmodel.Principal) (definitionmodel.WorkflowSchema, error) {
	if err := workflowAuthorizeCommand(principal); err != nil || !principal.Known || strings.TrimSpace(principal.UserID) == "" {
		return definitionmodel.WorkflowSchema{}, forbidden("backend.workflow.run_permission_required")
	}
	if _, ok := s.workerRepo.(workflowExecutionReceiptReader); !ok {
		return definitionmodel.WorkflowSchema{}, conflict(idempotency.ErrorCodeReceiptUnavailable)
	}
	workflow, found := s.registry.Get(strings.TrimSpace(key))
	if !found {
		return workflow, notFound("backend.workflow.not_found")
	}
	if len(invocationcontract.ValidateWorkflowPermission(workflow, principal)) > 0 {
		return workflow, forbidden("backend.workflow.run_permission_required")
	}
	if issues := invocationcontract.ValidateWorkflowTarget(workflow, invocationcontract.WorkflowEntryAgent); len(issues) > 0 {
		return workflow, workflowInvocationError(issues[0])
	}
	if workflow.Graph == nil || workflow.Graph.Version != 2 || len(workflow.Graph.Nodes) == 0 {
		return workflow, badRequest("backend.workflow.graph_v2_required")
	}
	return workflow, nil
}

// NormalizeAgentWorkflowInput uses the declared business input contract. The
// conversation cannot inject the reserved metadata accepted by internal callers.
func NormalizeAgentWorkflowInput(workflow definitionmodel.WorkflowSchema, payload map[string]any) (map[string]any, error) {
	fields := make([]definitionmodel.ActionPayloadField, 0, len(workflow.InputFields))
	allowed := make(map[string]bool, len(workflow.InputFields))
	for _, field := range workflow.InputFields {
		fields = append(fields, definitionmodel.ActionPayloadField{Key: field.Key, Name: field.Name, Type: field.Type, Options: field.Options, Required: field.Required, DefaultValue: field.DefaultValue})
		allowed[field.Key] = true
	}
	for key := range payload {
		if !allowed[key] {
			return nil, badRequest("backend.workflow.input_invalid")
		}
	}
	return actionapplication.ActionNormalizePayload(definitionmodel.ActionSchema{PayloadFields: fields}, payload)
}

func agentWorkflowInvocationKey(workflowKey, callerKey string, principal principalmodel.Principal) (string, error) {
	if callerKey == "" || strings.TrimSpace(callerKey) != callerKey || len(callerKey) > 256 {
		return "", badRequest(idempotency.ErrorCodeMissingKey)
	}
	return idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "workflow.agent.start", ResourceType: "workflow", TargetID: workflowKey,
		Payload: map[string]any{"workspace_id": principal.WorkspaceID, "user_id": principal.UserID, "caller_key": callerKey},
	})
}

// RunAgentWorkflowWithKey keeps the logical start scoped to its user and never
// reclaims an unresolved start. A returned process can still be waiting/running;
// successful acceptance must not be presented as completion of the workflow.
func (s *WorkflowApplicationService) RunAgentWorkflowWithKey(ctx context.Context, workflowKey string, payload map[string]any, callerKey string, principal principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	workflow, err := s.AgentWorkflowDefinition(ctx, workflowKey, principal)
	if err != nil {
		return workflowmodel.WorkflowRunResult{}, err
	}
	data, err := NormalizeAgentWorkflowInput(workflow, payload)
	if err != nil {
		return workflowmodel.WorkflowRunResult{}, err
	}
	key, err := agentWorkflowInvocationKey(workflow.Key, callerKey, principal)
	if err != nil {
		return workflowmodel.WorkflowRunResult{}, err
	}
	execution, err := s.executeWorkflowAttemptWithIdempotencyKey(ctx, workflow, data, principal, "agent", key, 1, false, true)
	if err != nil {
		return workflowmodel.WorkflowRunResult{}, err
	}
	return workflowmodel.WorkflowRunResult{WorkflowKey: workflow.Key, Name: workflow.Name, Status: execution.Status, Execution: execution}, nil
}

// InspectAgentWorkflowInvocation only reads an owned start receipt and its
// execution. Missing is distinct from unavailable or in progress, so a caller
// can only start an absent operation through the guarded method above.
func (s *WorkflowApplicationService) InspectAgentWorkflowInvocation(ctx context.Context, workflowKey string, payload map[string]any, callerKey string, principal principalmodel.Principal) (workflowmodel.WorkflowExecution, bool, error) {
	workflow, err := s.AgentWorkflowDefinition(ctx, workflowKey, principal)
	if err != nil {
		return workflowmodel.WorkflowExecution{}, false, err
	}
	data, err := NormalizeAgentWorkflowInput(workflow, payload)
	if err != nil {
		return workflowmodel.WorkflowExecution{}, false, err
	}
	key, err := agentWorkflowInvocationKey(workflow.Key, callerKey, principal)
	if err != nil {
		return workflowmodel.WorkflowExecution{}, false, err
	}
	receipt, found, err := s.workerRepo.(workflowExecutionReceiptReader).FindExecutionReceipt(ctx, principal.WorkspaceID, workflow.Key, key)
	if err != nil || !found {
		return workflowmodel.WorkflowExecution{}, found, err
	}
	fingerprint, err := workflowExecutionFingerprint(workflow, data, "agent", 1)
	if err != nil || receipt.RequestFingerprint != fingerprint || receipt.WorkspaceID != principal.WorkspaceID || receipt.WorkflowKey != workflow.Key || receipt.IdempotencyKey != key {
		return workflowmodel.WorkflowExecution{}, true, conflict(idempotency.ErrorCodeKeyReused)
	}
	if receipt.Status != string(idempotency.StatusSucceeded) {
		return workflowmodel.WorkflowExecution{}, true, conflict(idempotency.ErrorCodeInProgress)
	}
	expires, err := time.Parse(time.RFC3339Nano, receipt.ExpiresAt)
	if err != nil || !expires.After(time.Now().UTC()) || receipt.ExecutionID == "" {
		return workflowmodel.WorkflowExecution{}, true, conflict(idempotency.ErrorCodeReceiptUnavailable)
	}
	execution, found, err := s.workerRepo.GetExecution(ctx, principal.WorkspaceID, receipt.ExecutionID)
	if err != nil {
		return workflowmodel.WorkflowExecution{}, true, err
	}
	if !found || execution.ID != receipt.ExecutionID || execution.ActorID != principal.UserID || execution.WorkspaceID != principal.WorkspaceID || execution.WorkflowKey != workflow.Key || execution.IdempotencyKey != key || execution.Trigger != "agent" || execution.ProcessID == "" {
		return workflowmodel.WorkflowExecution{}, true, conflict(idempotency.ErrorCodeReceiptUnavailable)
	}
	return execution, true, nil
}
