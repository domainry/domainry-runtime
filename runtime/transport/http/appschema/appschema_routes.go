package appschema

import "net/http"

func (h *ApplicationSchemaHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /metadata/definitions/{resourceType}/{resourceKey}/validate", h.authenticated(h.validateApplicationDefinition))
	mux.HandleFunc("GET /metadata/migration-plan", h.authenticated(h.metadataMigrationPlan))
	mux.HandleFunc("GET /metadata/objects/{objectKey}/record-count", h.authenticated(h.metadataObjectRecordCount))
	mux.HandleFunc("GET /metadata/capabilities", h.authenticated(h.listCapabilities))
	mux.HandleFunc("GET /metadata/diagnostics", h.authenticated(h.opsMetadataDiagnostics))

	if h.provisionRequired != nil {
		mux.HandleFunc("GET /metadata/manifests/current", h.provisionRequired)
	}
	mux.HandleFunc("GET /metadata/execution-capabilities", h.executionCapabilities)
}
