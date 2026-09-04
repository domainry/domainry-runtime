package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	schedulerbusiness "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/modulehost"
)

type SchedulerHandler struct {
	service             schedulerService
	operations          *operationsapplication.OperationsApplicationService
	principal           func(*http.Request) principalmodel.Principal
	writeJSON           func(http.ResponseWriter, int, any)
	writeError          func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError   func(http.ResponseWriter, *http.Request, error)
	decodeJSON          func(http.ResponseWriter, *http.Request, any) bool
	authenticated       func(http.HandlerFunc) http.HandlerFunc
	dispatcher          modulehost.Dispatcher
	runtimeID           string
	authenticateService SchedulerServiceAuthenticator
	binding             schedulersdk.Binding
}

type schedulerService interface {
	ManagementDefinitions(context.Context, principalmodel.Principal) ([]schedulerbusiness.ManagementSchedulerDefinitionDTO, error)
	ManagementDefinition(context.Context, string, principalmodel.Principal) (schedulerbusiness.ManagementSchedulerDefinitionDTO, error)
	ManagementDefinitionVersions(context.Context, string, principalmodel.Principal) ([]schedulerbusiness.ManagementSchedulerDefinitionVersionDTO, error)
	ManagementAuthoringContract(context.Context, principalmodel.Principal) (schedulerbusiness.ManagementSchedulerAuthoringContract, error)
	AuthorizeOpsRead(context.Context, principalmodel.Principal) error
	PreviewDefinition(context.Context, map[string]any, principalmodel.Principal) (schedulerbusiness.SchedulerDefinitionPreview, error)
	PreviewSchedule(context.Context, map[string]any, principalmodel.Principal) (schedulerbusiness.SchedulerDefinitionPreview, error)
	SimulateManagementDefinition(context.Context, string, principalmodel.Principal) (schedulerbusiness.SchedulerDefinitionSimulation, error)
}

func (h *SchedulerHandler) ownerBinding() (schedulersdk.Binding, error) {
	if h.binding == nil {
		return nil, fmt.Errorf("Scheduler owner binding is unavailable")
	}
	return h.binding, nil
}

func (h *SchedulerHandler) listManagementSchedulerDefinitions(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ManagementDefinitions(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *SchedulerHandler) getManagementSchedulerDefinition(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.ManagementDefinition(r.Context(), strings.TrimSpace(r.PathValue("definitionID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, item)
}

func (h *SchedulerHandler) listManagementSchedulerDefinitionVersions(w http.ResponseWriter, r *http.Request) {
	items, err := h.service.ManagementDefinitionVersions(r.Context(), strings.TrimSpace(r.PathValue("definitionID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *SchedulerHandler) getManagementSchedulerAuthoringContract(w http.ResponseWriter, r *http.Request) {
	contract, err := h.service.ManagementAuthoringContract(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, contract)
}

type SchedulerDependencies struct {
	Service             *schedulerbusiness.SchedulerApplicationService
	Operations          *operationsapplication.OperationsApplicationService
	Principal           func(*http.Request) principalmodel.Principal
	WriteJSON           func(http.ResponseWriter, int, any)
	WriteError          func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError   func(http.ResponseWriter, *http.Request, error)
	DecodeJSON          func(http.ResponseWriter, *http.Request, any) bool
	Authenticated       func(http.HandlerFunc) http.HandlerFunc
	Dispatcher          modulehost.Dispatcher
	RuntimeID           string
	AuthenticateService SchedulerServiceAuthenticator
	Binding             schedulersdk.Binding
}

func NewSchedulerHandler(deps SchedulerDependencies) *SchedulerHandler {
	return &SchedulerHandler{
		service: deps.Service, operations: deps.Operations, principal: deps.Principal, writeJSON: deps.WriteJSON,
		writeError: deps.WriteError, writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON,
		authenticated: deps.Authenticated,
		dispatcher:    deps.Dispatcher, runtimeID: strings.TrimSpace(deps.RuntimeID), authenticateService: deps.AuthenticateService, binding: deps.Binding,
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

func (h *SchedulerHandler) simulateSchedulerJob(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.SimulateManagementDefinition(r.Context(), strings.TrimSpace(r.PathValue("definitionID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *SchedulerHandler) runSchedulerJob(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.PathValue("definitionID"))
	operation, err := h.executeOwnerOperation(r, "scheduler.job.run", "scheduler_definition", resourceID, nil, func(ctx context.Context) (any, error) {
		binding, bindErr := h.ownerBinding()
		if bindErr != nil {
			return nil, bindErr
		}
		return triggerSchedulerNow(ctx, binding, resourceID)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) runOpsSchedulerJob(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.PathValue("definitionID"))
	operation, err := h.executeOwnerOperation(r, "scheduler.job.run", "scheduler_definition", resourceID, nil, func(ctx context.Context) (any, error) {
		binding, bindErr := h.ownerBinding()
		if bindErr != nil {
			return nil, bindErr
		}
		run, executeErr := triggerSchedulerNow(ctx, binding, resourceID)
		return projectSDKRun(run), executeErr
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
	resourceID := strings.TrimSpace(r.PathValue("definitionID"))
	operation, err := h.executeOwnerOperation(r, "scheduler.definition.reschedule", "scheduler_definition", resourceID, request, func(ctx context.Context) (any, error) {
		binding, bindErr := h.ownerBinding()
		if bindErr != nil {
			return nil, bindErr
		}
		if executeErr := binding.Reschedule(ctx, resourceID, nextRunAt, "operator requested scheduler.definition.reschedule"); executeErr != nil {
			return nil, executeErr
		}
		return map[string]any{"status": "rescheduled", "definition_key": resourceID, "next_run_at": nextRunAt.UTC()}, nil
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) retrySchedulerRun(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.PathValue("runID"))
	operation, err := h.executeOwnerOperation(r, "scheduler.run.retry", "scheduler_run", resourceID, nil, func(ctx context.Context) (any, error) {
		binding, bindErr := h.ownerBinding()
		if bindErr != nil {
			return nil, bindErr
		}
		return binding.RetryRun(ctx, resourceID, "operator requested scheduler.run.retry")
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) retryOpsSchedulerRun(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.PathValue("runID"))
	operation, err := h.executeOwnerOperation(r, "scheduler.run.retry", "scheduler_run", resourceID, nil, func(ctx context.Context) (any, error) {
		binding, bindErr := h.ownerBinding()
		if bindErr != nil {
			return nil, bindErr
		}
		run, executeErr := binding.RetryRun(ctx, resourceID, "operator requested scheduler.run.retry")
		return projectSDKRun(run), executeErr
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) cancelSchedulerRun(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.PathValue("runID"))
	operation, err := h.executeOwnerOperation(r, "scheduler.run.cancel", "scheduler_run", resourceID, nil, func(ctx context.Context) (any, error) {
		binding, bindErr := h.ownerBinding()
		if bindErr != nil {
			return nil, bindErr
		}
		return binding.CancelRun(ctx, resourceID, "operator requested scheduler.run.cancel")
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *SchedulerHandler) cancelOpsSchedulerRun(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.PathValue("runID"))
	operation, err := h.executeOwnerOperation(r, "scheduler.run.cancel", "scheduler_run", resourceID, nil, func(ctx context.Context) (any, error) {
		binding, bindErr := h.ownerBinding()
		if bindErr != nil {
			return nil, bindErr
		}
		run, executeErr := binding.CancelRun(ctx, resourceID, "operator requested scheduler.run.cancel")
		return projectSDKRun(run), executeErr
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
	resourceID := strings.TrimSpace(r.PathValue("deadLetterID"))
	operation, err := h.executeOwnerOperation(r, "scheduler.dead_letter.resolve", "scheduler_dead_letter", resourceID, request, func(ctx context.Context) (any, error) {
		binding, bindErr := h.ownerBinding()
		if bindErr != nil {
			return nil, bindErr
		}
		return binding.ResolveDeadLetter(ctx, resourceID, request.Note)
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
	resourceID := strings.TrimSpace(r.PathValue("deadLetterID"))
	operation, err := h.executeOwnerOperation(r, "scheduler.dead_letter.resolve", "scheduler_dead_letter", resourceID, request, func(ctx context.Context) (any, error) {
		binding, bindErr := h.ownerBinding()
		if bindErr != nil {
			return nil, bindErr
		}
		return binding.ResolveDeadLetter(ctx, resourceID, request.Note)
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
	resourceID := strings.TrimSpace(r.PathValue("deadLetterID"))
	operation, err := h.executeOwnerOperation(r, "scheduler.dead_letter.requeue", "scheduler_dead_letter", resourceID, request, func(ctx context.Context) (any, error) {
		binding, bindErr := h.ownerBinding()
		if bindErr != nil {
			return nil, bindErr
		}
		run, executeErr := binding.RequeueDeadLetter(ctx, resourceID, request.Note)
		return projectSDKRun(run), executeErr
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
