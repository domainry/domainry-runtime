package lifecycle

import "net/http"

func (h *LifecycleHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /lifecycle/cleanup/jobs/{jobID}/run", h.authenticated(h.runCleanupJob))
}
