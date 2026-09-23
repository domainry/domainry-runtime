package appschema

import "net/http"

func (h *ApplicationSchemaHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /application-schema/diagnostics", h.authenticated(h.opsMetadataDiagnostics))
}
