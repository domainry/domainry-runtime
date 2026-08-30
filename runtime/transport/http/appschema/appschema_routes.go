package appschema

import "net/http"

func (h *ApplicationSchemaHandler) RegisterRoutes(mux *http.ServeMux) {
	authenticated := h.authenticated
	if authenticated == nil {
		authenticated = h.admin
	}
	mux.HandleFunc("GET /tenant-admin/metadata/definitions/{resourceType}", authenticated(h.listApplicationDefinitions))
	mux.HandleFunc("GET /tenant-admin/metadata/definitions/{resourceType}/{resourceKey}", authenticated(h.getApplicationDefinition))
	mux.HandleFunc("POST /tenant-admin/metadata/definitions/{resourceType}/{resourceKey}/validate", authenticated(h.validateApplicationDefinition))
	mux.HandleFunc("GET /tenant-admin/metadata/definitions/{resourceType}/{resourceKey}/versions", authenticated(h.listApplicationDefinitionVersions))
	mux.HandleFunc("GET /tenant-admin/metadata/definitions/{resourceType}/{resourceKey}/diff", authenticated(h.diffApplicationDefinitionVersions))
	mux.HandleFunc("GET /tenant-admin/metadata/migration-plan", authenticated(h.metadataMigrationPlan))
	mux.HandleFunc("GET /tenant-admin/metadata/objects/{objectKey}/record-count", authenticated(h.metadataObjectRecordCount))
	mux.HandleFunc("GET /tenant-admin/metadata/capabilities", authenticated(h.listCapabilities))
	mux.HandleFunc("GET /operations/metadata/diagnostics", h.admin(h.opsMetadataDiagnostics))

	if h.provisionRequired != nil {
		mux.HandleFunc("POST /tenant-admin/metadata/manifests/validate", h.provisionRequired)
		mux.HandleFunc("POST /tenant-admin/metadata/manifests/review", h.provisionRequired)
		mux.HandleFunc("POST /tenant-admin/metadata/manifests/apply", h.provisionRequired)
		mux.HandleFunc("GET /tenant-admin/metadata/manifests/current", h.provisionRequired)
	}
	mux.HandleFunc("GET /dictionaries/{dictionaryKey}/items", h.getDictionaryItems)
	mux.HandleFunc("GET /tenant-admin/metadata/localized-texts", authenticated(h.listLocalizedTexts))
	mux.HandleFunc("PUT /tenant-admin/metadata/localized-texts", authenticated(h.upsertLocalizedText))
	mux.HandleFunc("GET /tenant-admin/metadata/localized-texts/coverage", authenticated(h.localizedTextCoverage))
	mux.HandleFunc("GET /tenant-admin/metadata/localized-texts/export", authenticated(h.exportLocalizedTexts))
	mux.HandleFunc("GET /tenant-admin/metadata/localized-texts/export.xlsx", authenticated(h.exportLocalizedTextsXLSX))
	mux.HandleFunc("POST /tenant-admin/metadata/localized-texts/import", authenticated(h.importLocalizedTexts))
	mux.HandleFunc("GET /tenant-admin/execution-capabilities", h.executionCapabilities)
	mux.HandleFunc("POST /tenant-admin/metadata/reload", authenticated(h.reloadMetadata))
}
