package lifecycle

import "net/http"

func (h *LifecycleHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /operations/lifecycle/policies", h.authenticated(h.policies))
	mux.HandleFunc("POST /operations/lifecycle/policies", h.authenticated(h.publishPolicy))
	mux.HandleFunc("POST /operations/lifecycle/legal-holds", h.authenticated(h.createLegalHold))
	mux.HandleFunc("POST /operations/lifecycle/legal-holds/{holdID}/end", h.authenticated(h.endLegalHold))
	mux.HandleFunc("GET /operations/lifecycle/cleanup/preview", h.authenticated(h.cleanupPreview))
	mux.HandleFunc("POST /operations/lifecycle/cleanup/jobs", h.authenticated(h.createCleanupJob))
	mux.HandleFunc("POST /operations/lifecycle/cleanup/jobs/{jobID}/run", h.authenticated(h.runCleanupJob))
	mux.HandleFunc("GET /operations/lifecycle/metrics", h.authenticated(h.metrics))
	mux.HandleFunc("GET /operations/lifecycle/archive", h.authenticated(h.archiveEntries))
	mux.HandleFunc("POST /operations/lifecycle/subjects", h.authenticated(h.createSubjectRequest))
	mux.HandleFunc("POST /operations/lifecycle/subjects/{requestID}/verify", h.authenticated(h.verifySubjectRequest))
	mux.HandleFunc("POST /operations/lifecycle/subjects/{requestID}/preview", h.authenticated(h.previewSubjectRequest))
	mux.HandleFunc("POST /operations/lifecycle/subjects/{requestID}/approve", h.authenticated(h.approveSubjectRequest))
	mux.HandleFunc("POST /operations/lifecycle/subjects/{requestID}/execute", h.authenticated(h.executeSubjectRequest))
	mux.HandleFunc("GET /operations/lifecycle/subjects/{requestID}/download", h.authenticated(h.downloadSubjectExport))
	mux.HandleFunc("GET /operations/lifecycle/external-erasures", h.authenticated(h.externalErasures))
	mux.HandleFunc("POST /operations/lifecycle/external-erasures/{erasureID}/reconcile", h.authenticated(h.reconcileExternalErasure))
	mux.HandleFunc("POST /operations/lifecycle/deletions/replay", h.authenticated(h.replayDeletions))
}
