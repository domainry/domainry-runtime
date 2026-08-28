package agent

import (
	"context"
	"fmt"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type AgentGuardedWriteContract struct {
	ObjectKey      string
	Operation      string
	ActionKey      string
	Endpoint       string
	RequiresRecord bool
}

type AgentActionInvocation struct {
	ActionKey      string
	ObjectKey      string
	RecordID       string
	Input          map[string]any
	Principal      principalmodel.Principal
	RequestID      string
	IdempotencyKey string
}

type AgentActionInvocationResult struct {
	Record any
	Object any
}

type AgentProposalDependencies struct {
	GuardedWrites        func(context.Context, principalmodel.Principal) []AgentGuardedWriteContract
	ResolvePrincipalRole func(context.Context, string, string) (principalmodel.Principal, error)
	InvokeAction         func(context.Context, AgentActionInvocation) (AgentActionInvocationResult, error)
	RunWorkflow          func(context.Context, string, map[string]any, principalmodel.Principal) (any, error)
	ResolveLifecycle     func(context.Context, AgentProposal, principalmodel.Principal) error
}

// AgentProposalApplicationService evaluates agent proposals.
type AgentProposalApplicationService struct {
	state        *AgentApplicationService
	dependencies AgentProposalDependencies
}

func NewAgentProposalApplicationService(state *AgentApplicationService, dependencies AgentProposalDependencies) *AgentProposalApplicationService {
	return &AgentProposalApplicationService{state: state, dependencies: dependencies}
}

func (s *AgentProposalApplicationService) NormalizeGuardedWrite(ctx context.Context, proposed map[string]any, principal principalmodel.Principal) map[string]any {
	if proposed == nil {
		return map[string]any{}
	}
	if binding := proposalMap(proposed["action_binding"]); len(binding) > 0 {
		return proposed
	}
	var tool map[string]any
	for _, key := range []string{"tool_binding", "tool_call", "tool", "crud_binding"} {
		if candidate := proposalMap(proposed[key]); len(candidate) > 0 {
			tool = candidate
			break
		}
	}
	if len(tool) == 0 {
		return proposed
	}
	toolName := firstProposalString(tool, "tool_name", "tool", "name")
	operation := map[string]string{"createRecord": "create", "updateRecord": "update", "deleteRecord": "delete"}[toolName]
	objectKey := firstProposalString(tool, "object_key", "objectKey", "object")
	if operation == "" || objectKey == "" {
		return proposed
	}
	contract, ok := s.guardedWriteContract(ctx, principal, objectKey, operation)
	if !ok {
		return proposed
	}
	recordID := firstProposalString(tool, "record_id", "recordId", "id")
	if contract.RequiresRecord && recordID == "" {
		return proposed
	}
	data := proposalMap(tool["data"])
	if operation == "update" && len(data) == 0 {
		data = proposalMap(tool["patch"])
	}
	normalized := cloneAgentContext(proposed)
	delete(normalized, "tool_binding")
	normalized["action_binding"] = map[string]any{"object_key": contract.ObjectKey, "record_id": recordID, "action_key": contract.ActionKey, "data": data, "guarded_write": true, "operation": contract.Operation, "endpoint": contract.Endpoint, "source_tool": toolName}
	return normalized
}

func (s *AgentProposalApplicationService) guardedWriteContract(ctx context.Context, principal principalmodel.Principal, objectKey, operation string) (AgentGuardedWriteContract, bool) {
	if s == nil || s.dependencies.GuardedWrites == nil {
		return AgentGuardedWriteContract{}, false
	}
	for _, contract := range s.dependencies.GuardedWrites(ctx, principal) {
		if contract.ObjectKey == objectKey && contract.Operation == operation && contract.ActionKey != "" {
			return contract, true
		}
	}
	return AgentGuardedWriteContract{}, false
}

func firstProposalString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := proposalString(values[key]); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func (s *AgentProposalApplicationService) Decide(ctx context.Context, proposalID, decision, reason string, metadata map[string]any, principal principalmodel.Principal) (AgentProposal, error) {
	decision = strings.TrimSpace(decision)
	switch decision {
	case "approved", "rejected", "returned", "timed_out", "cancelled":
	default:
		return AgentProposal{}, apperror.New(apperror.KindBadRequest, "agent_dialog.proposal_decision_invalid", nil, nil)
	}
	proposal, err := s.state.GetProposal(ctx, proposalID, principal)
	if err != nil {
		return proposal, err
	}
	if proposal.Status != "draft" {
		if proposal.Status != decision {
			return proposal, apperror.New(apperror.KindConflict, "agent_dialog.proposal_already_decided", nil, nil)
		}
		return proposal, s.resolveLifecycle(ctx, proposal, principal)
	}
	actorPrincipal, executionPrincipal := principal, principal
	if decision == "approved" {
		executionPrincipal, err = s.resolveApprovalPrincipal(ctx, proposal, principal)
		if err != nil {
			return proposal, err
		}
	}
	proposal, err = s.state.decideProposalCAS(ctx, proposal, decision, reason, metadata, nil, actorPrincipal)
	if err != nil {
		return proposal, err
	}
	if decision != "approved" {
		return proposal, s.resolveLifecycle(ctx, proposal, actorPrincipal)
	}
	execution := s.execute(ctx, proposal, executionPrincipal)
	proposal, err = s.state.decideProposalCAS(ctx, proposal, decision, reason, metadata, execution, actorPrincipal)
	if err != nil {
		return proposal, err
	}
	return proposal, s.resolveLifecycle(ctx, proposal, actorPrincipal)
}

func (s *AgentProposalApplicationService) resolveLifecycle(ctx context.Context, proposal AgentProposal, principal principalmodel.Principal) error {
	if s == nil {
		return nil
	}
	if s.dependencies.ResolveLifecycle == nil {
		return nil
	}
	return s.dependencies.ResolveLifecycle(ctx, proposal, principal)
}

func (s *AgentProposalApplicationService) resolveApprovalPrincipal(ctx context.Context, proposal AgentProposal, requestPrincipal principalmodel.Principal) (principalmodel.Principal, error) {
	if s == nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindUnavailable, "agent.authorization.resolver_unavailable", nil, nil)
	}
	if s.dependencies.ResolvePrincipalRole == nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindUnavailable, "agent.authorization.resolver_unavailable", nil, nil)
	}
	principal, err := s.dependencies.ResolvePrincipalRole(ctx, proposal.UserID, proposal.Role)
	if err != nil {
		return principalmodel.Principal{}, err
	}
	if !principal.Known {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "agent.authorization.approval_principal_revoked", nil, map[string]string{"authorization_revision": principal.AuthorizationRevision})
	}
	if strings.TrimSpace(principal.WorkspaceID) != strings.TrimSpace(proposal.WorkspaceID) {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "agent.authorization.approval_principal_revoked", nil, map[string]string{"authorization_revision": principal.AuthorizationRevision})
	}
	if strings.TrimSpace(principal.UserID) != strings.TrimSpace(proposal.UserID) {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "agent.authorization.approval_principal_revoked", nil, map[string]string{"authorization_revision": principal.AuthorizationRevision})
	}
	if strings.TrimSpace(principal.RoleKey) != strings.TrimSpace(proposal.Role) {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "agent.authorization.approval_principal_revoked", nil, map[string]string{"authorization_revision": principal.AuthorizationRevision})
	}
	principal.RequestID, principal.CorrelationID, principal.CausationID = requestPrincipal.RequestID, requestPrincipal.CorrelationID, requestPrincipal.CausationID
	principal.SurfaceKey = requestPrincipal.SurfaceKey
	return principal, nil
}

func (s *AgentProposalApplicationService) execute(ctx context.Context, proposal AgentProposal, principal principalmodel.Principal) map[string]any {
	if proposal.Status != "approved" {
		return nil
	}
	if binding := proposalMap(proposal.Proposed["action_binding"]); len(binding) > 0 {
		objectKey, recordID, actionKey := proposalString(binding["object_key"]), proposalString(binding["record_id"]), proposalString(binding["action_key"])
		if objectKey == "" || actionKey == "" {
			return map[string]any{"status": "skipped", "kind": "action", "reason": "binding_incomplete"}
		}
		proposalID := strings.TrimSpace(proposal.ProposalID)
		if proposalID == "" {
			return map[string]any{"status": "failed", "kind": "action", "reason": "proposal_id_required"}
		}
		if s.dependencies.InvokeAction == nil {
			return map[string]any{"status": "failed", "kind": "action", "reason": "action_executor_unavailable"}
		}
		invocation, err := s.dependencies.InvokeAction(ctx, AgentActionInvocation{
			ActionKey: actionKey, ObjectKey: objectKey, RecordID: recordID, Input: proposalMap(binding["data"]), Principal: principal,
			RequestID: principal.RequestID, IdempotencyKey: "agent-proposal:" + proposalID,
		})
		kind, result := "action", invocation.Record
		if recordID == "" {
			kind, result = "object_action", invocation.Object
		}
		if err != nil {
			return map[string]any{"status": "failed", "kind": kind, "object_key": objectKey, "record_id": recordID, "action_key": actionKey, "error_code": apperror.CodeOf(err)}
		}
		return map[string]any{"status": "applied", "kind": kind, "object_key": objectKey, "record_id": recordID, "action_key": actionKey, "result": result}
	}
	if binding := proposalMap(proposal.Proposed["workflow_binding"]); len(binding) > 0 {
		key := proposalString(binding["workflow_key"])
		if key == "" {
			return map[string]any{"status": "skipped", "kind": "workflow", "reason": "binding_incomplete"}
		}
		if s.dependencies.RunWorkflow == nil {
			return map[string]any{"status": "failed", "kind": "workflow", "workflow_key": key, "reason": "workflow_runner_unavailable"}
		}
		result, err := s.dependencies.RunWorkflow(ctx, key, proposalMap(binding["payload"]), principal)
		if err != nil {
			return map[string]any{"status": "failed", "kind": "workflow", "workflow_key": key, "error_code": apperror.CodeOf(err)}
		}
		return map[string]any{"status": "applied", "kind": "workflow", "workflow_key": key, "result": result}
	}
	return map[string]any{"status": "skipped", "kind": "none", "reason": "no_binding"}
}

func proposalMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return cloneAgentContext(typed)
	}
	return map[string]any{}
}

func proposalString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
