package workflows

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
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

func (h *WorkflowsHandler) requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		h.writeError(w, r, http.StatusBadRequest, idempotency.ErrorCodeMissingKey)
		return "", false
	}
	return key, true
}

func intQuery(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}

func (h *WorkflowsHandler) executeOwnerOperation(r *http.Request, kind, resourceType, resourceID string, payload any, execute func(context.Context) (any, error)) (operationsapplication.OperationsOwnerExecutionResult, error) {
	if h.operations == nil {
		value, err := execute(r.Context())
		return operationsapplication.OperationsOwnerExecutionResult{Value: value}, err
	}
	principal := h.principal(r)
	replayReadiness := func(ctx context.Context, _ any) error {
		switch resourceType {
		case "workflow_execution":
			_, err := h.executions.InspectWorkflowExecution(ctx, resourceID, principal)
			return err
		case "workflow_process":
			_, err := h.processes.OpsWorkflowProcess(ctx, resourceID, principal)
			return err
		default:
			return apperror.New(apperror.KindInternal, "backend.operations.replay_readiness_unavailable", nil, nil)
		}
	}
	return h.operations.ExecuteOwnerOperation(r.Context(), operationsapplication.OperationsOwnerExecutionRequest{
		Kind: kind, ResourceType: resourceType, ResourceID: resourceID, Payload: payload,
		Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), Reason: operationshttp.OwnerOperationReason(r, "operator requested "+kind), Reference: strings.TrimSpace(r.Header.Get("X-Operation-Reference")),
		ReplayReadiness: replayReadiness,
	}, principal, execute)
}

func (h *WorkflowsHandler) simulateWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Payload map[string]any `json:"payload"`
	}
	if r.Body != nil && r.ContentLength != 0 && !h.decodeJSON(w, r, &req) {
		return
	}
	result, err := h.executions.SimulateWorkflow(r.Context(), strings.TrimSpace(r.PathValue("workflowKey")), req.Payload, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
