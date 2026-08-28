package operations

import (
	"net/http"
	"strings"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
)

func (h *OperationsHandler) captureDiagnostics(w http.ResponseWriter, r *http.Request) {
	var command operationsapplication.OperationsDiagnosticsCommand
	if !h.decodeJSON(w, r, &command) {
		return
	}
	result, err := h.service.CaptureDiagnostics(r.Context(), command, strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
