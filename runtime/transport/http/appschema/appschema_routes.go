package appschema

import "net/http"

func (h *ApplicationSchemaHandler) RegisterRoutes(mux *http.ServeMux) {
	authenticated := h.authenticated
	if authenticated == nil {
		authenticated = h.admin
	}
	mux.HandleFunc("POST /tenant-admin/metadata/definitions/{resourceType}/{resourceKey}/validate", authenticated(h.validateApplicationDefinition))
	mux.HandleFunc("GET /tenant-admin/metadata/migration-plan", authenticated(h.metadataMigrationPlan))
	mux.HandleFunc("GET /tenant-admin/metadata/objects/{objectKey}/record-count", authenticated(h.metadataObjectRecordCount))
	mux.HandleFunc("GET /tenant-admin/metadata/capabilities", authenticated(h.listCapabilities))
	mux.HandleFunc("GET /operations/metadata/diagnostics", h.admin(h.opsMetadataDiagnostics))

	if h.provisionRequired != nil {
		mux.HandleFunc("GET /tenant-admin/metadata/manifests/current", h.provisionRequired)
	}
	mux.HandleFunc("GET /tenant-admin/execution-capabilities", h.executionCapabilities)
}
