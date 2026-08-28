package automation

import (
	"net/http"
	"strings"
)

func (h *AutomationHandler) validateAutomationAuthoringFragment(w http.ResponseWriter, r *http.Request) {
	fragment := map[string]any{}
	if !h.decodeJSON(w, r, &fragment) {
		return
	}
	result, err := h.commands.ValidateAutomationAuthoringFragment(r.Context(), strings.TrimSpace(r.PathValue("capabilityKey")), fragment, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
