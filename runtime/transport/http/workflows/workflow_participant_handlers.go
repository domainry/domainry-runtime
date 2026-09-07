package workflows

import (
	"context"
	"net/http"
	"strings"

	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

func workflowProcessFilterFromRequest(r *http.Request) workflowmodel.WorkflowProcessFilter {
	query := r.URL.Query()
	statuses := query["status"]
	status := query.Get("status")
	if len(statuses) > 1 || strings.Contains(status, ",") {
		status = ""
	}
	return workflowmodel.WorkflowProcessFilter{
		ProcessID: query.Get("resource_id"), WorkflowKey: query.Get("workflow_key"), DefinitionVersion: intQuery(query.Get("definition_version")),
		ObjectKey: query.Get("object_key"), RecordID: query.Get("record_id"), Status: status,
		Statuses:    statuses,
		InitiatorID: query.Get("initiator_id"), ApproverID: query.Get("approver_id"),
		UpdatedFrom: query.Get("updated_from"), UpdatedTo: query.Get("updated_to"), Limit: intQuery(query.Get("limit")),
	}
}

func (h *WorkflowsHandler) listParticipantWorkflowTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.processes.ParticipantWorkflowTasks(
		r.Context(), h.principal(r), r.URL.Query().Get("status"), intQuery(r.URL.Query().Get("limit")),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, tasks)
}

func (h *WorkflowsHandler) listBusinessTeamWorkflowTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.processes.BusinessTeamWorkflowTasks(
		r.Context(), h.principal(r), r.URL.Query().Get("status"), intQuery(r.URL.Query().Get("limit")),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, tasks)
}

func (h *WorkflowsHandler) listParticipantWorkflowProcesses(w http.ResponseWriter, r *http.Request) {
	processes, err := h.processes.ParticipantWorkflowProcesses(r.Context(), h.principal(r), workflowProcessFilterFromRequest(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, processes)
}

func (h *WorkflowsHandler) getParticipantWorkflowProcess(w http.ResponseWriter, r *http.Request) {
	detail, err := h.processes.WorkflowProcess(
		r.Context(), strings.TrimSpace(r.PathValue("processID")), h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, workflowapplication.ProjectParticipantWorkflowProcessDetail(detail))
}

func (h *WorkflowsHandler) listOpsWorkflowExecutions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	executions, err := h.executions.OpsWorkflowExecutions(
		r.Context(), h.principal(r), query.Get("object_key"), query.Get("record_id"), intQuery(query.Get("limit")),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, executions)
}

func (h *WorkflowsHandler) listOpsWorkflowProcesses(w http.ResponseWriter, r *http.Request) {
	processes, err := h.processes.OpsWorkflowProcesses(r.Context(), h.principal(r), workflowProcessFilterFromRequest(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, processes)
}

func (h *WorkflowsHandler) getOpsWorkflowProcess(w http.ResponseWriter, r *http.Request) {
	detail, err := h.processes.OpsWorkflowProcess(r.Context(), strings.TrimSpace(r.PathValue("processID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, detail)
}

func (h *WorkflowsHandler) decideParticipantWorkflowTask(w http.ResponseWriter, r *http.Request, decision string) {
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
	h.writeJSON(w, http.StatusOK, workflowapplication.ProjectParticipantWorkflowProcess(process))
}

func (h *WorkflowsHandler) approveParticipantWorkflowTask(w http.ResponseWriter, r *http.Request) {
	h.decideParticipantWorkflowTask(w, r, "approved")
}

func (h *WorkflowsHandler) rejectParticipantWorkflowTask(w http.ResponseWriter, r *http.Request) {
	h.decideParticipantWorkflowTask(w, r, "rejected")
}

func (h *WorkflowsHandler) returnParticipantWorkflowTask(w http.ResponseWriter, r *http.Request) {
	h.decideParticipantWorkflowTask(w, r, "returned")
}

func (h *WorkflowsHandler) withdrawParticipantWorkflowProcess(w http.ResponseWriter, r *http.Request) {
	key, ok := h.requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	process, err := h.processes.CancelWorkflowProcessWithKey(
		r.Context(), strings.TrimSpace(r.PathValue("processID")), key, h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, workflowapplication.ProjectParticipantWorkflowProcess(process))
}

func (h *WorkflowsHandler) retryParticipantWorkflowProcess(w http.ResponseWriter, r *http.Request) {
	key, ok := h.requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	process, err := h.processes.RetryWorkflowProcessWithKey(
		r.Context(), strings.TrimSpace(r.PathValue("processID")), key, h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, workflowapplication.ProjectParticipantWorkflowProcess(process))
}

func (h *WorkflowsHandler) runParticipantWorkflow(w http.ResponseWriter, r *http.Request) {
	key, ok := h.requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	var request struct {
		Payload map[string]any `json:"payload"`
	}
	if r.Body != nil && r.ContentLength != 0 && !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.executions.RunWorkflowWithKey(r.Context(), strings.TrimSpace(r.PathValue("workflowKey")), request.Payload, key, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, workflowapplication.ProjectParticipantWorkflowRun(result))
}

func (h *WorkflowsHandler) processOpsWorkflowExecutions(w http.ResponseWriter, r *http.Request) {
	result, err := h.executions.ProcessWorkflowExecutions(r.Context(), intQuery(r.URL.Query().Get("limit")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	projected := workflowapplication.OpsWorkflowProcessBatchDTO{Processed: result.Processed}
	for _, execution := range result.Executions {
		projected.Executions = append(projected.Executions, workflowapplication.ProjectOpsWorkflowExecution(execution))
	}
	h.writeJSON(w, http.StatusOK, projected)
}

func (h *WorkflowsHandler) retryOpsWorkflowExecution(w http.ResponseWriter, r *http.Request) {
	key, ok := h.requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("executionID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "workflow.execution.retry", "workflow_execution", resourceID, nil, func(ctx context.Context) (any, error) {
		return h.executions.RetryWorkflowExecutionWithKey(ctx, resourceID, key, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	result, ok := operation.Value.(workflowmodel.WorkflowRunResult)
	if !ok {
		h.writeError(w, r, http.StatusInternalServerError, "backend.workflow.ops_projection_failed")
		return
	}
	h.writeJSON(w, http.StatusOK, workflowapplication.OpsWorkflowExecutionCommandDTO{
		Status: result.Status, Execution: workflowapplication.ProjectOpsWorkflowExecution(result.Execution),
	})
}

func (h *WorkflowsHandler) resolveOpsWorkflowExecution(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireIdempotencyKey(w, r); !ok {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil && r.ContentLength != 0 && !h.decodeJSON(w, r, &request) {
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("executionID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "workflow.execution.resolve", "workflow_execution", resourceID, request, func(ctx context.Context) (any, error) {
		return h.executions.ResolveWorkflowExecution(ctx, resourceID, request.Reason, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	result, ok := operation.Value.(workflowmodel.WorkflowResolveResult)
	if !ok {
		h.writeError(w, r, http.StatusInternalServerError, "backend.workflow.ops_projection_failed")
		return
	}
	h.writeJSON(w, http.StatusOK, workflowapplication.OpsWorkflowExecutionCommandDTO{
		Status: result.Status, Execution: workflowapplication.ProjectOpsWorkflowExecution(result.Execution),
	})
}

func (h *WorkflowsHandler) mutateOpsWorkflowProcess(w http.ResponseWriter, r *http.Request, operationKind string) {
	key, ok := h.requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	request := struct {
		Note string `json:"note"`
	}{}
	if operationKind == "resolve" && !h.decodeJSON(w, r, &request) {
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("processID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "workflow.process."+operationKind, "workflow_process", resourceID, request, func(ctx context.Context) (any, error) {
		if operationKind == "retry" {
			return h.processes.RetryOpsWorkflowProcessWithKey(ctx, resourceID, key, principal)
		}
		return h.processes.ResolveOpsWorkflowProcessFailure(ctx, resourceID, request.Note, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	process, ok := operation.Value.(workflowapplication.OpsWorkflowProcessDTO)
	if !ok {
		h.writeError(w, r, http.StatusInternalServerError, "backend.workflow.ops_projection_failed")
		return
	}
	h.writeJSON(w, http.StatusOK, process)
}

func (h *WorkflowsHandler) retryOpsWorkflowProcess(w http.ResponseWriter, r *http.Request) {
	h.mutateOpsWorkflowProcess(w, r, "retry")
}

func (h *WorkflowsHandler) resolveOpsWorkflowProcess(w http.ResponseWriter, r *http.Request) {
	h.mutateOpsWorkflowProcess(w, r, "resolve")
}
