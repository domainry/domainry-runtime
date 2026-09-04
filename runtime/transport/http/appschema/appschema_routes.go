package appschema

import "net/http"

func (h *ApplicationSchemaHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /application-schema/definitions/{resourceType}/{resourceKey}/validate", h.authenticated(h.validateApplicationDefinition))
	mux.HandleFunc("GET /application-schema/migration-plan", h.authenticated(h.metadataMigrationPlan))
	mux.HandleFunc("GET /application-schema/objects/{objectKey}/record-count", h.authenticated(h.metadataObjectRecordCount))
	mux.HandleFunc("GET /application-schema/diagnostics", h.authenticated(h.opsMetadataDiagnostics))
}
