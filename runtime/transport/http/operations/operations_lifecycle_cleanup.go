package operations

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	requestcontext "github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

func (h *OperationsHandler) lifecycleCleanupPreview(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	result, err := h.lifecycle.PreviewCleanup(r.Context(), principal.WorkspaceID, strings.TrimSpace(r.URL.Query().Get("policy_key")), principal, time.Now().UTC())
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *OperationsHandler) lifecycleCreateCleanupJob(w http.ResponseWriter, r *http.Request) {
	var input lifecyclemodel.CleanupJob
	if !h.decodeJSON(w, r, &input) {
		return
	}
	input.WorkspaceID = h.principal(r).WorkspaceID
	result, err := h.lifecycle.CreateCleanupJob(r.Context(), input, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusAccepted, result)
}

func (h *OperationsHandler) lifecycleRunCleanupJob(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	batchSize, _ := strconv.Atoi(r.URL.Query().Get("batch_size"))
	jobID := strings.TrimSpace(r.PathValue("jobID"))
	operation, err := h.service.ExecuteOwnerOperation(r.Context(), operationsapplication.OperationsOwnerExecutionRequest{
		Kind: "retention.cleanup", ResourceType: "retention_policy", ResourceID: jobID,
		Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), Reason: OwnerOperationReason(r, "operator requested retention cleanup"),
		Reference: strings.TrimSpace(r.Header.Get("X-Operation-Reference")), Payload: map[string]any{"job_id": jobID, "batch_size": batchSize},
	}, principal, func(ctx context.Context) (any, error) {
		return h.lifecycle.ProcessCleanupJob(ctx, principal.WorkspaceID, jobID, "http-"+requestcontext.NewRequestID(), 2*time.Minute, batchSize, time.Now().UTC(), principal)
	})
	WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *OperationsHandler) lifecycleMetrics(w http.ResponseWriter, r *http.Request) {
	result, err := h.lifecycle.Metrics(r.Context(), h.principal(r), time.Now().UTC())
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *OperationsHandler) lifecycleArchiveEntries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	result, err := h.lifecycle.ListArchiveEntries(r.Context(), r.URL.Query().Get("source_table"), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
