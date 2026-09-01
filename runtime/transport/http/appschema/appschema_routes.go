package appschema

import "net/http"

func (h *ApplicationSchemaHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /tenant-admin/metadata/definitions/{resourceType}/{resourceKey}/validate", h.authenticated(h.validateApplicationDefinition))
	mux.HandleFunc("GET /tenant-admin/metadata/migration-plan", h.authenticated(h.metadataMigrationPlan))
	mux.HandleFunc("GET /tenant-admin/metadata/objects/{objectKey}/record-count", h.authenticated(h.metadataObjectRecordCount))
	mux.HandleFunc("GET /tenant-admin/metadata/capabilities", h.authenticated(h.listCapabilities))
	mux.HandleFunc("GET /operations/metadata/diagnostics", h.authenticated(h.opsMetadataDiagnostics))

	if h.provisionRequired != nil {
		mux.HandleFunc("GET /tenant-admin/metadata/manifests/current", h.provisionRequired)
	}
	mux.HandleFunc("GET /tenant-admin/execution-capabilities", h.executionCapabilities)
}
