package discovery

import "net/http"

func (h *DiscoveryHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /i18n/locales", h.i18nLocales)
	mux.HandleFunc("GET /i18n/resources", h.i18nResources)
	mux.HandleFunc("GET /tenant-admin/runtime-schema", h.getSchema)
	mux.HandleFunc("GET /business/runtime-schema", h.getBusinessRuntimeSchema)
	mux.HandleFunc("GET /portal/runtime-schema", h.getPortalRuntimeSchema)
}
