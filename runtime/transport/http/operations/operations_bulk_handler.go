package operations

import (
	"net/http"
	"strings"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
)

func (h *OperationsHandler) dryRunBulkDeadLetters(w http.ResponseWriter, r *http.Request) {
	var request operationsapplication.OperationsBulkDryRunRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.DryRunBulkDeadLetters(r.Context(), request, strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *OperationsHandler) applyBulkDeadLetters(w http.ResponseWriter, r *http.Request) {
	var request operationsapplication.OperationsBulkApplyRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.ApplyBulkDeadLetters(r.Context(), request, strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
