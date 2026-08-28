package changeplans

import "net/http"

func (h *ChangePlansHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /tenant-admin/change-plans/validate", h.validate)
	mux.HandleFunc("GET /tenant-admin/change-plans/{planID}", h.getDraft)
	mux.HandleFunc("PUT /tenant-admin/change-plans/{planID}", h.saveDraft)
	mux.HandleFunc("POST /tenant-admin/change-plans/{planID}/clone-current", h.cloneCurrent)
	mux.HandleFunc("POST /tenant-admin/change-plans/{planID}/simulate", h.simulateScenarios)
	mux.HandleFunc("POST /tenant-admin/change-plans/{planID}/review", h.review)
	mux.HandleFunc("POST /tenant-admin/change-plans/{planID}/approve", h.approve)
	mux.HandleFunc("GET /tenant-admin/change-plans/{planID}/export", h.exportPackage)
	mux.HandleFunc("PUT /tenant-admin/change-plans/{planID}/package", h.importPackage)
	mux.HandleFunc("POST /tenant-admin/change-plans/apply", h.apply)

	mux.HandleFunc("GET /domain-maintenance/rollback-policy", h.rollbackPolicy)
}
