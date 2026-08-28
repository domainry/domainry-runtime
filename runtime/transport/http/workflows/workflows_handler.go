package workflows

import (
	"context"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"net/http"
	"strconv"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

type WorkflowsHandler struct {
	definitions       *workflowapplication.WorkflowApplicationService
	processes         *workflowapplication.WorkflowApplicationService
	executions        *workflowapplication.WorkflowApplicationService
	operations        *operationsapplication.OperationsApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	decodeJSON        func(http.ResponseWriter, *http.Request, any) bool
}

func (h *WorkflowsHandler) requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		h.writeError(w, r, http.StatusBadRequest, idempotency.ErrorCodeMissingKey)
		return "", false
	}
	return key, true
}

type WorkflowsDependencies struct {
	Definitions       *workflowapplication.WorkflowApplicationService
	Processes         *workflowapplication.WorkflowApplicationService
	Executions        *workflowapplication.WorkflowApplicationService
	Operations        *operationsapplication.OperationsApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
}

func NewWorkflowsHandler(deps WorkflowsDependencies) *WorkflowsHandler {
	return &WorkflowsHandler{
		definitions: deps.Definitions, processes: deps.Processes, executions: deps.Executions, operations: deps.Operations,
		principal: deps.Principal, writeJSON: deps.WriteJSON,
		writeError: deps.WriteError, writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON,
	}
}

func intQuery(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}

func (h *WorkflowsHandler) listWorkflowExecutions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	executions, err := h.executions.WorkflowExecutions(
		r.Context(),
		h.principal(r),
		query.Get("object_key"),
		query.Get("record_id"),
		intQuery(query.Get("limit")),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, executions)
}

func (h *WorkflowsHandler) processWorkflowExecutions(w http.ResponseWriter, r *http.Request) {
	result, err := h.executions.ProcessWorkflowExecutions(r.Context(), intQuery(r.URL.Query().Get("limit")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *WorkflowsHandler) retryWorkflowExecution(w http.ResponseWriter, r *http.Request) {
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
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *WorkflowsHandler) resolveWorkflowExecution(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !h.decodeJSON(w, r, &req) {
			return
		}
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("executionID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "workflow.execution.resolve", "workflow_execution", resourceID, req, func(ctx context.Context) (any, error) {
		return h.executions.ResolveWorkflowExecution(ctx, resourceID, req.Reason, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *WorkflowsHandler) executeOwnerOperation(r *http.Request, kind, resourceType, resourceID string, payload any, execute func(context.Context) (any, error)) (operationsapplication.OperationsOwnerExecutionResult, error) {
	if h.operations == nil {
		value, err := execute(r.Context())
		return operationsapplication.OperationsOwnerExecutionResult{Value: value}, err
	}
	return h.operations.ExecuteOwnerOperation(r.Context(), operationsapplication.OperationsOwnerExecutionRequest{
		Kind: kind, ResourceType: resourceType, ResourceID: resourceID, Payload: payload,
		Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), Reason: operationshttp.OwnerOperationReason(r, "operator requested "+kind), Reference: strings.TrimSpace(r.Header.Get("X-Operation-Reference")),
	}, h.principal(r), execute)
}

func (h *WorkflowsHandler) runWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Payload map[string]any `json:"payload"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !h.decodeJSON(w, r, &req) {
			return
		}
	}
	result, err := h.executions.RunWorkflow(r.Context(), strings.TrimSpace(r.PathValue("workflowKey")), req.Payload, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *WorkflowsHandler) simulateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Payload map[string]any `json:"payload"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !h.decodeJSON(w, r, &req) {
			return
		}
	}
	result, err := h.executions.SimulateWorkflow(r.Context(), strings.TrimSpace(r.PathValue("workflowKey")), req.Payload, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
