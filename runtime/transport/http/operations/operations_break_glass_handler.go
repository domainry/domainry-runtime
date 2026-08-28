package operations

import (
	"net/http"
	"strconv"
	"strings"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
)

func (h *OperationsHandler) enableBreakGlass(w http.ResponseWriter, r *http.Request) {
	var command operationsapplication.OperationsBreakGlassEnableCommand
	if !h.decodeJSON(w, r, &command) {
		return
	}
	result, err := h.service.EnableBreakGlass(r.Context(), command, strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
func (h *OperationsHandler) disableBreakGlass(w http.ResponseWriter, r *http.Request) {
	var command operationsapplication.OperationsBreakGlassDisableCommand
	if !h.decodeJSON(w, r, &command) {
		return
	}
	result, err := h.service.DisableBreakGlass(r.Context(), strings.TrimSpace(r.PathValue("grantID")), command, strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
func (h *OperationsHandler) listBreakGlass(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	grants, err := h.service.ListBreakGlass(r.Context(), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": grants, "count": len(grants)})
}
