package composition

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

// scheduledAgentTargetRuntimeAdapter is the only Scheduler-to-Agent mapping.
// Scheduler owns timing and signed plan facts, Identity owns current principal
// state, and Agent owns task acceptance and execution. No source implementation
// imports another source implementation or writes another source's tables.
type scheduledAgentTargetRuntimeAdapter struct {
	runtime    *runtimeAssembly
	principals identitysdk.PrincipalResolver
	tasks      agentsdk.ScheduledConversationTaskService
}

func (a scheduledAgentTargetRuntimeAdapter) ExecuteAgentTarget(ctx context.Context, request dispatchapplication.AgentTargetRequest) (dispatchapplication.AgentTargetReceipt, error) {
	if a.runtime == nil || a.principals == nil || a.tasks == nil {
		return dispatchapplication.AgentTargetReceipt{}, scheduledAgentTargetError(apperror.KindUnavailable, "backend.dispatch.scheduled_agent_unavailable")
	}
	if strings.TrimSpace(request.Operation) != "conversation_task_start" {
		return dispatchapplication.AgentTargetReceipt{}, scheduledAgentTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_agent_operation_invalid")
	}
	var dispatch schedulersdk.ScheduledPlanDispatch
	if len(request.Payload) == 0 || json.Unmarshal(request.Payload, &dispatch) != nil ||
		dispatch.ContractVersion != schedulersdk.ScheduledPlanDispatchContractVersion || dispatch.Owner.Validate() != nil ||
		strings.TrimSpace(dispatch.PlanID) == "" || strings.TrimSpace(dispatch.ConversationRef.ConversationID) == "" {
		return dispatchapplication.AgentTargetReceipt{}, scheduledAgentTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_agent_payload_invalid")
	}
	principal, err := resolveCurrentScheduledPlanOwner(ctx, a.productKey(), a.principals, dispatch.Owner, "backend.dispatch.scheduled_agent_product_denied", "backend.dispatch.scheduled_agent_principal_denied")
	if err != nil {
		return dispatchapplication.AgentTargetReceipt{}, err
	}
	start, err := scheduledAgentConversationTaskStart(dispatch.Input)
	if err != nil {
		return dispatchapplication.AgentTargetReceipt{}, err
	}
	authority := agentsdk.ConversationAuthority{Known: true, WorkspaceID: principal.WorkspaceID, UserID: principal.UserID}
	serviceContext := agentsdk.WithAuthorizedServiceAction(ctx, agentsdk.ActionAgentScheduledConversationTaskStart, agentsdk.AgentRuntimeServiceAudience)
	receipt, err := a.tasks.StartScheduledConversationTask(serviceContext, agentsdk.ScheduledConversationTaskRequest{
		ContractVersion: agentsdk.ScheduledConversationTaskContractVersion,
		PlanID:          dispatch.PlanID, SchedulerRunID: request.ExecutionID, IdempotencyKey: request.IdempotencyKey, ScheduledFor: request.ScheduledFor,
		Authority: authority, ConversationID: dispatch.ConversationRef.ConversationID, SourceRunID: dispatch.ConversationRef.RunID,
		Input: start, AllowedActions: append([]string(nil), dispatch.AllowedActions...),
	})
	if err != nil {
		return dispatchapplication.AgentTargetReceipt{}, err
	}
	return dispatchapplication.AgentTargetReceipt{ID: receipt.Task.ID, Status: "accepted"}, nil
}

func (a scheduledAgentTargetRuntimeAdapter) productKey() string {
	a.runtime.mu.RLock()
	defer a.runtime.mu.RUnlock()
	return a.runtime.templateID
}

func scheduledAgentConversationTaskStart(raw json.RawMessage) (agentsdk.ConversationTaskStart, error) {
	if len(raw) == 0 || !json.Valid(raw) {
		return agentsdk.ConversationTaskStart{}, scheduledAgentTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_agent_input_invalid")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return agentsdk.ConversationTaskStart{}, scheduledAgentTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_agent_input_invalid")
	}
	var start agentsdk.ConversationTaskStart
	if goal, ok := fields["goal"]; !ok || json.Unmarshal(goal, &start.Goal) != nil {
		return agentsdk.ConversationTaskStart{}, scheduledAgentTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_agent_input_invalid")
	}
	if allowed, ok := fields["allowed_tools"]; ok && json.Unmarshal(allowed, &start.AllowedTools) != nil {
		return agentsdk.ConversationTaskStart{}, scheduledAgentTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_agent_input_invalid")
	}
	if budget, ok := fields["budget"]; ok && json.Unmarshal(budget, &start.Budget) != nil {
		return agentsdk.ConversationTaskStart{}, scheduledAgentTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_agent_input_invalid")
	}
	if followUp, ok := fields["follow_up"]; ok {
		decoder := json.NewDecoder(bytes.NewReader(followUp))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&start.FollowUp) != nil {
			return agentsdk.ConversationTaskStart{}, scheduledAgentTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_agent_input_invalid")
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return agentsdk.ConversationTaskStart{}, scheduledAgentTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_agent_input_invalid")
		}
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return agentsdk.ConversationTaskStart{}, scheduledAgentTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_agent_input_invalid")
	}
	// Preserve the complete product-authored input for the model. Runtime reads
	// only the execution controls above; arbitrary business parameters are not
	// silently discarded or interpreted as authorization.
	start.Input = compact.String()
	return start, nil
}

func scheduledAgentTargetError(kind apperror.ErrorKind, code string) error {
	return apperror.New(kind, code, nil, nil)
}

var _ dispatchapplication.AgentTargetRuntime = scheduledAgentTargetRuntimeAdapter{}
