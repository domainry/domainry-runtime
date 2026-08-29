package operations

import (
	"net/http"
)

func (h *OperationsHandler) monitoringMetrics(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if !principal.HasPermission("workspace.admin") && !principal.HasExactPermission("runtime_ops.capability_status.read") {
		h.writeJSON(w, http.StatusForbidden, map[string]any{"code": "auth.permission_denied"})
		return
	}
	if h.monitoring == nil {
		h.writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "monitoring.metrics_unavailable"})
		return
	}
	h.writeJSON(w, http.StatusOK, h.monitoring.Metrics(r.Context()))
}
