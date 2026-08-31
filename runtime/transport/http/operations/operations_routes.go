package operations

import "net/http"

func (h *OperationsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /operations/database-retirements", h.discoverDatabaseRetirement)
	mux.HandleFunc("GET /operations/database-retirements", h.listDatabaseRetirements)
	mux.HandleFunc("GET /operations/database-retirements/{retirementID}", h.getDatabaseRetirement)
	mux.HandleFunc("POST /operations/database-retirements/{retirementID}/preview", h.previewDatabaseRetirement)
	mux.HandleFunc("POST /operations/database-retirements/{retirementID}/advance", h.advanceDatabaseRetirement)
	mux.HandleFunc("POST /operations/database-retirements/{retirementID}/execute", h.executeDatabaseRetirement)
	mux.HandleFunc("POST /operations", h.authenticated(h.submitOperation))
	mux.HandleFunc("GET /operations", h.authenticated(h.listOperations))
	mux.HandleFunc("GET /operations/catalog", h.authenticated(h.operationCatalog))
	mux.HandleFunc("GET /operations/controls", h.authenticated(h.listControls))
	mux.HandleFunc("PUT /operations/controls/{controlKind}/{owner}", h.authenticated(h.setControl))
	mux.HandleFunc("POST /operations/leases/{owner}/{resourceID}/force-release", h.authenticated(h.forceReleaseLease))
	mux.HandleFunc("GET /operations/dead-letters/{owner}/{deadLetterID}", h.authenticated(h.inspectDeadLetter))
	mux.HandleFunc("POST /operations/dead-letters/{owner}/{deadLetterID}/{action}", h.authenticated(h.actOnDeadLetter))
	mux.HandleFunc("POST /operations/bulk/dead-letters/dry-run", h.authenticated(h.dryRunBulkDeadLetters))
	mux.HandleFunc("POST /operations/bulk/dead-letters/apply", h.authenticated(h.applyBulkDeadLetters))
	mux.HandleFunc("POST /operations/diagnostics/snapshots", h.authenticated(h.captureDiagnostics))
	mux.HandleFunc("GET /operations/runbooks/{category}", h.authenticated(h.operationRunbook))
	mux.HandleFunc("POST /operations/break-glass", h.authenticated(h.enableBreakGlass))
	mux.HandleFunc("GET /operations/break-glass", h.authenticated(h.listBreakGlass))
	mux.HandleFunc("POST /operations/break-glass/{grantID}/disable", h.authenticated(h.disableBreakGlass))
	mux.HandleFunc("GET /operations/{operationID}", h.authenticated(h.getOperation))
	mux.HandleFunc("GET /operations/idempotency/receipts", h.authenticated(h.receipts))
	mux.HandleFunc("POST /operations/idempotency/receipts/{owner}/{receiptID}/retry", h.authenticated(h.retry))
	mux.HandleFunc("POST /operations/idempotency/receipts/{owner}/{receiptID}/reset", h.authenticated(h.reset))
	if h.monitoringProxy {
		mux.HandleFunc("GET /operations/monitoring/metrics", h.authenticated(h.monitoringMetrics))
	}
}
