package workflows

import (
	"context"
	"net/http"
	"strings"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

func (h *WorkflowsHandler) listWorkflowProcesses(w http.ResponseWriter, r *http.Request) {
	processes, err := h.processes.WorkflowProcesses(r.Context(), h.principal(r), workflowProcessFilterFromRequest(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, processes)
}

func (h *WorkflowsHandler) listMyWorkflowTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.processes.MyWorkflowTasks(r.Context(), h.principal(r), r.URL.Query().Get("status"), intQuery(r.URL.Query().Get("limit")))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, tasks)
}

func (h *WorkflowsHandler) approveWorkflowTask(w http.ResponseWriter, r *http.Request) {
	h.decideWorkflowTask(w, r, "approved")
}

func (h *WorkflowsHandler) rejectWorkflowTask(w http.ResponseWriter, r *http.Request) {
	h.decideWorkflowTask(w, r, "rejected")
}

func (h *WorkflowsHandler) returnWorkflowTask(w http.ResponseWriter, r *http.Request) {
	h.decideWorkflowTask(w, r, "returned")
}

func (h *WorkflowsHandler) decideWorkflowTask(w http.ResponseWriter, r *http.Request, decision string) {
	key, ok := h.requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	request := workflowmodel.WorkflowTaskDecisionRequest{Decision: decision}
	if r.ContentLength != 0 && !h.decodeJSON(w, r, &request) {
		return
	}
	request.Decision = decision
	request.IdempotencyKey = key
	process, err := h.processes.DecideTask(r.Context(), strings.TrimSpace(r.PathValue("taskID")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, process)
}

func (h *WorkflowsHandler) getWorkflowProcess(w http.ResponseWriter, r *http.Request) {
	detail, err := h.processes.WorkflowProcess(r.Context(), strings.TrimSpace(r.PathValue("processID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, detail)
}

func (h *WorkflowsHandler) cancelWorkflowProcess(w http.ResponseWriter, r *http.Request) {
	key, ok := h.requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("processID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "workflow.process.cancel", "workflow_process", resourceID, nil, func(ctx context.Context) (any, error) {
		return h.processes.CancelWorkflowProcessWithKey(ctx, resourceID, key, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *WorkflowsHandler) retryWorkflowProcess(w http.ResponseWriter, r *http.Request) {
	key, ok := h.requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("processID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "workflow.process.retry", "workflow_process", resourceID, nil, func(ctx context.Context) (any, error) {
		return h.processes.RetryWorkflowProcessWithKey(ctx, resourceID, key, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *WorkflowsHandler) resolveWorkflowProcessFailure(w http.ResponseWriter, r *http.Request) {
	request := struct {
		Note string `json:"note"`
	}{}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("processID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "workflow.process.resolve", "workflow_process", resourceID, request, func(ctx context.Context) (any, error) {
		return h.processes.ResolveWorkflowProcessFailure(ctx, resourceID, request.Note, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}
