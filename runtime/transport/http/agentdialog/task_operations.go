package agentdialog

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/idempotency"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

func (h *AgentDialogHandler) listAgentTaskRuns(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if !agentTaskPermission(principal, "agent.task.read") {
		h.writeError(w, r, http.StatusForbidden, "agent.task.read_permission_required")
		return
	}
	if h.taskRuns == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "agent.task.repository_unavailable")
		return
	}
	query := r.URL.Query()
	statuses := make([]agentmodel.AgentTaskRunStatus, 0, len(query["status"]))
	for _, status := range query["status"] {
		if value := strings.TrimSpace(status); value != "" {
			statuses = append(statuses, agentmodel.AgentTaskRunStatus(value))
		}
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	runs, err := h.taskRuns.List(r.Context(), principal.WorkspaceID, agentrepository.AgentTaskRunFilter{Statuses: statuses, ProcessID: strings.TrimSpace(query.Get("process_id")), TaskKey: strings.TrimSpace(query.Get("task_key")), Limit: limit})
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	items := make([]agentTaskRunProjection, 0, len(runs))
	for _, run := range runs {
		items = append(items, projectAgentTaskRun(run))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *AgentDialogHandler) getAgentTaskRun(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if !agentTaskPermission(principal, "agent.task.read") {
		h.writeError(w, r, http.StatusForbidden, "agent.task.read_permission_required")
		return
	}
	if h.taskRuns == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "agent.task.repository_unavailable")
		return
	}
	run, found, err := h.taskRuns.Get(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.PathValue("taskRunID")))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !found {
		h.writeError(w, r, http.StatusNotFound, "agent.task.not_found")
		return
	}
	h.writeJSON(w, http.StatusOK, projectAgentTaskRun(run))
}

func (h *AgentDialogHandler) retryAgentTaskRun(w http.ResponseWriter, r *http.Request) {
	h.operateAgentTaskRun(w, r, "retry")
}
func (h *AgentDialogHandler) resolveAgentTaskRun(w http.ResponseWriter, r *http.Request) {
	h.operateAgentTaskRun(w, r, "resolve")
}
func (h *AgentDialogHandler) reconcileAgentTaskRun(w http.ResponseWriter, r *http.Request) {
	h.operateAgentTaskRun(w, r, "reconcile")
}

func (h *AgentDialogHandler) cancelAgentTaskRun(w http.ResponseWriter, r *http.Request) {
	h.executeAgentTaskOperation(w, r, "cancel", func(ctx context.Context, principal principalmodel.Principal, runID, key, reason string) (any, error) {
		run, replayed, err := h.taskRuns.RequestCancel(ctx, principal.WorkspaceID, runID, reason)
		return map[string]any{"task": projectAgentTaskRun(run), "replayed": replayed, "idempotency_key": key}, err
	})
}

func (h *AgentDialogHandler) operateAgentTaskRun(w http.ResponseWriter, r *http.Request, kind string) {
	h.executeAgentTaskOperation(w, r, kind, func(ctx context.Context, principal principalmodel.Principal, runID, key, reason string) (any, error) {
		run, replayed, err := h.taskRuns.Operate(ctx, principal.WorkspaceID, runID, kind, key, reason, principal)
		return map[string]any{"task": projectAgentTaskRun(run), "replayed": replayed}, err
	})
}

func (h *AgentDialogHandler) executeAgentTaskOperation(w http.ResponseWriter, r *http.Request, kind string, execute func(context.Context, principalmodel.Principal, string, string, string) (any, error)) {
	principal, key := h.principal(r), strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !agentTaskPermission(principal, "agent.task.operate") {
		h.writeError(w, r, http.StatusForbidden, "agent.task.operate_permission_required")
		return
	}
	if h.taskRuns == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "agent.task.repository_unavailable")
		return
	}
	if key == "" {
		h.writeError(w, r, http.StatusBadRequest, idempotency.ErrorCodeMissingKey)
		return
	}
	request := struct {
		Reason string `json:"reason"`
	}{}
	if r.ContentLength != 0 && !h.decodeJSON(w, r, &request) {
		return
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" {
		h.writeError(w, r, http.StatusBadRequest, "agent.task.operation_evidence_required")
		return
	}
	runID := strings.TrimSpace(r.PathValue("taskRunID"))
	operation, err := h.executeAgentOwnerOperation(r, "agent.task."+kind, runID, request, func(ctx context.Context) (any, error) { return execute(ctx, principal, runID, key, reason) })
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *AgentDialogHandler) executeAgentOwnerOperation(r *http.Request, kind, resourceID string, payload any, execute func(context.Context) (any, error)) (operationsapplication.OperationsOwnerExecutionResult, error) {
	if h.operations == nil {
		value, err := execute(r.Context())
		return operationsapplication.OperationsOwnerExecutionResult{Value: value}, err
	}
	return h.operations.ExecuteOwnerOperation(r.Context(), operationsapplication.OperationsOwnerExecutionRequest{Kind: kind, ResourceType: "agent_task_run", ResourceID: resourceID, Payload: payload, Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), Reason: operationshttp.OwnerOperationReason(r, "operator requested "+kind), Reference: strings.TrimSpace(r.Header.Get("X-Operation-Reference"))}, h.principal(r), execute)
}

func agentTaskPermission(principal principalmodel.Principal, permission string) bool {
	if !principal.Known {
		return false
	}
	return principal.HasExactPermission(permission) || principal.HasPermission("workspace.admin")
}
