package discovery

import "net/http"

func (h *DiscoveryHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /discovery/i18n/locales", h.i18nLocales)
	mux.HandleFunc("GET /discovery/i18n/resources", h.i18nResources)
	mux.HandleFunc("GET /discovery/schema/administration", h.getSchema)
	mux.HandleFunc("GET /discovery/schema", h.getPublishedRuntimeSchema)
	mux.HandleFunc("GET /discovery/references/{kind}", h.referenceValues)
}
