package operations

import (
	"net/http"
	"strings"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
)

func (h *OperationsHandler) inspectDeadLetter(w http.ResponseWriter, r *http.Request) {
	item, err := h.service.InspectDeadLetter(r.Context(), strings.TrimSpace(r.PathValue("owner")), strings.TrimSpace(r.PathValue("deadLetterID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, item)
}

func (h *OperationsHandler) actOnDeadLetter(w http.ResponseWriter, r *http.Request) {
	var request operationsapplication.OperationsDeadLetterActionRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.ActOnDeadLetter(
		r.Context(), strings.TrimSpace(r.PathValue("owner")), strings.TrimSpace(r.PathValue("deadLetterID")), strings.TrimSpace(r.PathValue("action")), request, strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
