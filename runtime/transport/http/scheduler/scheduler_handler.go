package scheduler

import (
	"context"
	"net/http"
	"strings"
	"time"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	schedulerbusiness "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

type SchedulerHandler struct {
	service           schedulerService
	operations        *operationsapplication.OperationsApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	decodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	admin             func(http.HandlerFunc) http.HandlerFunc
	authenticated     func(http.HandlerFunc) http.HandlerFunc
}

type schedulerService interface {
	TenantAdminDefinitions(context.Context, principalmodel.Principal) ([]schedulerbusiness.TenantAdminSchedulerDefinitionDTO, error)
	TenantAdminDefinition(context.Context, string, principalmodel.Principal) (schedulerbusiness.TenantAdminSchedulerDefinitionDTO, error)
	TenantAdminDefinitionVersions(context.Context, string, principalmodel.Principal) ([]schedulerbusiness.TenantAdminSchedulerDefinitionVersionDTO, error)
	TenantAdminAuthoringContract(context.Context, principalmodel.Principal) (schedulerbusiness.TenantAdminSchedulerAuthoringContract, error)
	OpsState(context.Context, principalmodel.Principal) (schedulerbusiness.OpsSchedulerStateDTO, error)
	PreviewDefinition(context.Context, map[string]any, principalmodel.Principal) (schedulerbusiness.SchedulerDefinitionPreview, error)
	PreviewSchedule(context.Context, map[string]any, principalmodel.Principal) (schedulerbusiness.SchedulerDefinitionPreview, error)
	SimulateTenantAdminDefinition(context.Context, string, principalmodel.Principal) (schedulerbusiness.SchedulerOperationResult, error)
	GetDefinition(context.Context, string, principalmodel.Principal) (recordmodel.Record, error)
	DefinitionVersions(context.Context, string, principalmodel.Principal) ([]schedulerbusiness.SchedulerDefinitionVersion, error)
	SimulateJob(context.Context, string, principalmodel.Principal) (schedulerbusiness.SchedulerOperationResult, error)
	RunJob(context.Context, string, string, principalmodel.Principal) (schedulerbusiness.SchedulerOperationResult, error)
	RescheduleDefinition(context.Context, string, time.Time, principalmodel.Principal) (schedulerbusiness.SchedulerOperationResult, error)
	RetryRun(context.Context, string, string, principalmodel.Principal) (schedulerbusiness.SchedulerOperationResult, error)
	CancelRun(context.Context, string, string, principalmodel.Principal) (schedulerbusiness.SchedulerOperationResult, error)
	ResolveDeadLetter(context.Context, string, string, string, principalmodel.Principal) (schedulerbusiness.SchedulerOperationResult, error)
	RequeueDeadLetter(context.Context, string, string, string, principalmodel.Principal) (schedulerbusiness.SchedulerOperationResult, error)
}

func (h *SchedulerHandler) listTenantAdminSchedulerDefinitions(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.TenantAdminDefinitions(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *SchedulerHandler) getTenantAdminSchedulerDefinition(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.TenantAdminDefinition(r.Context(), strings.TrimSpace(r.PathValue("definitionID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, item)
}

func (h *SchedulerHandler) listTenantAdminSchedulerDefinitionVersions(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.TenantAdminDefinitionVersions(r.Context(), strings.TrimSpace(r.PathValue("definitionID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *SchedulerHandler) getTenantAdminSchedulerAuthoringContract(w http.ResponseWriter, r *http.Request) {
	contract, err := h.service.TenantAdminAuthoringContract(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, contract)
}

func (h *SchedulerHandler) getOpsSchedulerState(w http.ResponseWriter, r *http.Request) {
	state, err := h.service.OpsState(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, state)
}

type SchedulerDependencies struct {
	Service           *schedulerbusiness.SchedulerApplicationService
	Operations        *operationsapplication.OperationsApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	Admin             func(http.HandlerFunc) http.HandlerFunc
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
}

func NewSchedulerHandler(deps SchedulerDependencies) *SchedulerHandler {
	authenticated := deps.Authenticated
	if authenticated == nil {
		authenticated = deps.Admin
	}
	return &SchedulerHandler{
		service: deps.Service, operations: deps.Operations, principal: deps.Principal, writeJSON: deps.WriteJSON,
		writeError: deps.WriteError, writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON, admin: deps.Admin,
		authenticated: authenticated,
	}
}

func (h *SchedulerHandler) previewSchedulerJob(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Data map[string]any `json:"data"`
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.PreviewDefinition(r.Context(), request.Data, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *SchedulerHandler) previewSchedulerSchedule(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{}
	if !h.decodeJSON(w, r, &data) {
		return
	}
	result, err := h.service.PreviewSchedule(r.Context(), data, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *SchedulerHandler) getSchedulerJob(w http.ResponseWriter, r *http.Request) {
	record, err := h.service.GetDefinition(r.Context(), strings.TrimSpace(r.PathValue("definitionID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, record)
}

func (h *SchedulerHandler) listSchedulerJobVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := h.service.DefinitionVersions(r.Context(), strings.TrimSpace(r.PathValue("definitionID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": versions, "count": len(versions)})
}

func (h *SchedulerHandler) simulateSchedulerJob(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.SimulateTenantAdminDefinition(r.Context(), strings.TrimSpace(r.PathValue("definitionID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *SchedulerHandler) runSchedulerJob(w http.ResponseWriter, r *http.Request) {
	resourceID, key, principal := strings.TrimSpace(r.PathValue("definitionID")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "scheduler.job.run", "scheduler_definition", resourceID, nil, func(ctx context.Context) (any, error) {
		return h.service.RunJob(ctx, resourceID, key, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) runOpsSchedulerJob(w http.ResponseWriter, r *http.Request) {
	resourceID, key, principal := strings.TrimSpace(r.PathValue("definitionID")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "scheduler.job.run", "scheduler_definition", resourceID, nil, func(ctx context.Context) (any, error) {
		result, executeErr := h.service.RunJob(ctx, resourceID, key, principal)
		return schedulerbusiness.ProjectOpsSchedulerOperation(result), executeErr
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) rescheduleOpsSchedulerDefinition(w http.ResponseWriter, r *http.Request) {
	var request struct {
		NextRunAt string `json:"next_run_at"`
	}
	if !h.decodeJSON(w, r, &request) {
		return
	}
	nextRunAt, err := time.Parse(time.RFC3339, strings.TrimSpace(request.NextRunAt))
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "backend.scheduler.next_run_at_invalid")
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("definitionID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "scheduler.definition.reschedule", "scheduler_definition", resourceID, request, func(ctx context.Context) (any, error) {
		result, executeErr := h.service.RescheduleDefinition(ctx, resourceID, nextRunAt, principal)
		return schedulerbusiness.ProjectOpsSchedulerOperation(result), executeErr
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) retrySchedulerRun(w http.ResponseWriter, r *http.Request) {
	resourceID, key, principal := strings.TrimSpace(r.PathValue("runID")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "scheduler.run.retry", "job_run", resourceID, nil, func(ctx context.Context) (any, error) {
		return h.service.RetryRun(ctx, resourceID, key, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) retryOpsSchedulerRun(w http.ResponseWriter, r *http.Request) {
	resourceID, key, principal := strings.TrimSpace(r.PathValue("runID")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "scheduler.run.retry", "job_run", resourceID, nil, func(ctx context.Context) (any, error) {
		result, executeErr := h.service.RetryRun(ctx, resourceID, key, principal)
		return schedulerbusiness.ProjectOpsSchedulerOperation(result), executeErr
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) cancelSchedulerRun(w http.ResponseWriter, r *http.Request) {
	resourceID, key, principal := strings.TrimSpace(r.PathValue("runID")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "scheduler.run.cancel", "job_run", resourceID, nil, func(ctx context.Context) (any, error) {
		return h.service.CancelRun(ctx, resourceID, key, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) cancelOpsSchedulerRun(w http.ResponseWriter, r *http.Request) {
	resourceID, key, principal := strings.TrimSpace(r.PathValue("runID")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "scheduler.run.cancel", "job_run", resourceID, nil, func(ctx context.Context) (any, error) {
		result, executeErr := h.service.CancelRun(ctx, resourceID, key, principal)
		return schedulerbusiness.ProjectOpsSchedulerOperation(result), executeErr
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) resolveSchedulerDeadLetter(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Note string `json:"note"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !h.decodeJSON(w, r, &request) {
			return
		}
	}
	resourceID, key, principal := strings.TrimSpace(r.PathValue("deadLetterID")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "scheduler.dead_letter.resolve", "job_dead_letter", resourceID, request, func(ctx context.Context) (any, error) {
		return h.service.ResolveDeadLetter(ctx, resourceID, request.Note, key, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) resolveOpsSchedulerDeadLetter(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Note string `json:"note"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !h.decodeJSON(w, r, &request) {
			return
		}
	}
	resourceID, key, principal := strings.TrimSpace(r.PathValue("deadLetterID")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "scheduler.dead_letter.resolve", "job_dead_letter", resourceID, request, func(ctx context.Context) (any, error) {
		result, executeErr := h.service.ResolveDeadLetter(ctx, resourceID, request.Note, key, principal)
		return schedulerbusiness.ProjectOpsSchedulerOperation(result), executeErr
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) requeueOpsSchedulerDeadLetter(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Note string `json:"note"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !h.decodeJSON(w, r, &request) {
			return
		}
	}
	resourceID, key, principal := strings.TrimSpace(r.PathValue("deadLetterID")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "scheduler.dead_letter.requeue", "job_dead_letter", resourceID, request, func(ctx context.Context) (any, error) {
		result, executeErr := h.service.RequeueDeadLetter(ctx, resourceID, request.Note, key, principal)
		return schedulerbusiness.ProjectOpsSchedulerOperation(result), executeErr
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) executeOwnerOperation(r *http.Request, kind, resourceType, resourceID string, payload any, execute func(context.Context) (any, error)) (operationsapplication.OperationsOwnerExecutionResult, error) {
	if h.operations == nil {
		value, err := execute(r.Context())
		return operationsapplication.OperationsOwnerExecutionResult{Value: value}, err
	}
	return h.operations.ExecuteOwnerOperation(r.Context(), operationsapplication.OperationsOwnerExecutionRequest{
		Kind: kind, ResourceType: resourceType, ResourceID: resourceID, Payload: payload,
		Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), Reason: operationshttp.OwnerOperationReason(r, "operator requested "+kind), Reference: strings.TrimSpace(r.Header.Get("X-Operation-Reference")),
	}, h.principal(r), execute)
}
