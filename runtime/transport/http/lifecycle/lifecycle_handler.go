// Package lifecycle owns the one Runtime-orchestrated Lifecycle endpoint.
// Product governance HTTP is contributed by the Lifecycle module Adapter.
package lifecycle

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	requestcontext "github.com/domainry/domainry-foundation/requestcontext"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type OperationRunner interface {
	ExecuteOwnerOperation(context.Context, operationsapplication.OperationsOwnerExecutionRequest, principalmodel.Principal, func(context.Context) (any, error)) (operationsapplication.OperationsOwnerExecutionResult, error)
}

type LifecycleDependencies struct {
	Service                   lifecyclesdk.Governance
	Operations                OperationRunner
	Principal                 func(*http.Request) principalmodel.Principal
	CleanupProcessorPrincipal lifecycleaccess.Principal
	WriteJSON                 func(http.ResponseWriter, int, any)
	WriteServiceError         func(http.ResponseWriter, *http.Request, error)
	Authenticated             func(http.HandlerFunc) http.HandlerFunc
}

type LifecycleHandler struct {
	service                   lifecyclesdk.Governance
	operations                OperationRunner
	principal                 func(*http.Request) principalmodel.Principal
	cleanupProcessorPrincipal lifecycleaccess.Principal
	writeJSON                 func(http.ResponseWriter, int, any)
	writeServiceError         func(http.ResponseWriter, *http.Request, error)
	authenticated             func(http.HandlerFunc) http.HandlerFunc
}

func NewLifecycleHandler(deps LifecycleDependencies) *LifecycleHandler {
	return &LifecycleHandler{
		service: deps.Service, operations: deps.Operations, principal: deps.Principal,
		cleanupProcessorPrincipal: deps.CleanupProcessorPrincipal,
		writeJSON:                 deps.WriteJSON, writeServiceError: deps.WriteServiceError,
		authenticated: deps.Authenticated,
	}
}

func (h *LifecycleHandler) runCleanupJob(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		h.writeServiceError(w, r, &lifecyclesdk.Error{StatusCode: http.StatusServiceUnavailable, Code: "lifecycle.unavailable"})
		return
	}
	principal := h.principal(r)
	batch, _ := strconv.Atoi(r.URL.Query().Get("batch_size"))
	jobID := strings.TrimSpace(r.PathValue("jobID"))
	run := func(ctx context.Context) (any, error) {
		return h.service.ProcessCleanupJob(
			ctx, principal.WorkspaceID, jobID, "http-"+requestcontext.NewRequestID(),
			2*time.Minute, batch, time.Now().UTC(), h.cleanupProcessorPrincipal,
		)
	}
	if h.operations == nil {
		result, err := run(r.Context())
		h.writeResult(w, r, result, err)
		return
	}
	result, err := h.operations.ExecuteOwnerOperation(r.Context(), operationsapplication.OperationsOwnerExecutionRequest{
		Kind: "retention.cleanup", ResourceType: "retention_policy", ResourceID: jobID,
		Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), Reason: operationReason(r, "operator requested retention cleanup"),
		Reference: strings.TrimSpace(r.Header.Get("X-Operation-Reference")), Payload: map[string]any{"job_id": jobID, "batch_size": batch},
	}, principal, run)
	writeOperationHeaders(w, result)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result.Value)
}

func (h *LifecycleHandler) writeResult(w http.ResponseWriter, r *http.Request, result any, err error) {
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func operationReason(r *http.Request, fallback string) string {
	if reason := strings.TrimSpace(r.Header.Get("X-Operation-Reason")); reason != "" {
		return reason
	}
	return fallback
}

func writeOperationHeaders(w http.ResponseWriter, result operationsapplication.OperationsOwnerExecutionResult) {
	if strings.TrimSpace(result.Receipt.Command.ID) == "" {
		return
	}
	w.Header().Set("Operation-ID", result.Receipt.Command.ID)
	w.Header().Set("Operation-Location", result.Receipt.StatusURL)
	w.Header().Set("Location", result.Receipt.StatusURL)
	if result.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
}
