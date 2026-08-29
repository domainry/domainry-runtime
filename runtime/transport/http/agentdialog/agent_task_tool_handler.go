package agentdialog

import (
	"net/http"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type agentTaskToolInvokeRequest struct {
	Credential     string         `json:"credential"`
	WorkspaceID    string         `json:"workspace_id"`
	TaskRunID      string         `json:"task_run_id"`
	Tool           string         `json:"tool"`
	Input          map[string]any `json:"input"`
	IdempotencyKey string         `json:"idempotency_key"`
}

func (h *AgentDialogHandler) agentTaskToolInvoke(w http.ResponseWriter, r *http.Request) {
	var payload agentTaskToolInvokeRequest
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	if h.taskRuns == nil || h.taskTools == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "agent.tool.gateway_unavailable")
		return
	}
	run, found, err := h.taskRuns.Get(r.Context(), payload.WorkspaceID, payload.TaskRunID)
	if err != nil || !found || run.Status != agentmodel.AgentTaskRunRunning || strings.TrimSpace(run.Lease.Owner) == "" || run.Lease.FencingToken <= 0 {
		if err == nil {
			h.writeError(w, r, http.StatusForbidden, "agent.tool.task_scope_denied")
		} else {
			h.writeServiceError(w, r, err)
		}
		return
	}
	objects, actions, outcomes := []string(nil), []string(nil), []string(nil)
	if count := len(run.Evidence.Authorization); count > 0 {
		decision := run.Evidence.Authorization[count-1]
		objects, actions, outcomes = decision.AllowedObjects, decision.AllowedActions, decision.AllowedOutcomes
	}
	initiator := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: run.Identity.Initiator.UserID, WorkspaceID: run.Identity.Initiator.WorkspaceID, RoleKey: run.Identity.Initiator.RoleKey, AuthorizationRevision: run.Identity.Initiator.AuthorizationRevision}, CorrelationID: run.CorrelationID}
	result, err := h.taskTools.Invoke(r.Context(), agentruntime.AgentToolInvocationRequest{
		Credential: payload.Credential, WorkspaceID: run.WorkspaceID, ProcessID: run.ProcessID, TaskRunID: run.ID,
		Owner: workerplatform.WorkerID(run.Lease.Owner), FencingToken: workerplatform.FencingToken(run.Lease.FencingToken),
		Initiator: initiator, Identity: agentmodel.AgentTaskIdentity{Mode: run.Identity.Mode, PrincipalKey: run.Identity.ServicePrincipalKey}, ExpectedRotationVersion: run.Identity.ServiceRotationVersion,
		TaskKey: run.TaskKey, TaskVersion: run.TaskVersion, NodeAllowedObjects: objects, NodeAllowedActions: actions, NodeAllowedOutcomes: outcomes,
		Tool: payload.Tool, Input: payload.Input, IdempotencyKey: payload.IdempotencyKey,
	})
	if err != nil {
		h.securityAudit(r, "agent_task_tool_denied", "Agent Task tool invocation failed", map[string]any{"workspace_id": run.WorkspaceID, "task_run_id": run.ID, "tool": payload.Tool})
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
