package discovery

import (
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	"net/http"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"github.com/domainry/domainry-runtime/runtime/platform/localization"
)

type DiscoveryHandler struct {
	schema     *appschemaapplication.ApplicationSchemaQueryApplicationService
	principal  func(*http.Request) principalmodel.Principal
	writeJSON  func(http.ResponseWriter, int, any)
	writeError func(http.ResponseWriter, *http.Request, int, string, ...string)
}

type DiscoveryDependencies struct {
	Schema     *appschemaapplication.ApplicationSchemaQueryApplicationService
	Principal  func(*http.Request) principalmodel.Principal
	WriteJSON  func(http.ResponseWriter, int, any)
	WriteError func(http.ResponseWriter, *http.Request, int, string, ...string)
}

func NewDiscoveryHandler(deps DiscoveryDependencies) *DiscoveryHandler {
	return &DiscoveryHandler{schema: deps.Schema, principal: deps.Principal, writeJSON: deps.WriteJSON, writeError: deps.WriteError}
}

func (h *DiscoveryHandler) i18nLocales(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{
		"default_locale": localization.DefaultLocale, "locales": localization.SupportedLocales(),
		"catalog_version": localization.CatalogVersion(),
	})
}

func (h *DiscoveryHandler) i18nResources(w http.ResponseWriter, r *http.Request) {
	payload, ok := localization.Resources(r.URL.Query().Get("locale"), r.URL.Query().Get("namespace"))
	if !ok {
		h.writeError(w, r, http.StatusBadRequest, "localization.unsupported_locale")
		return
	}
	etag := `"` + payload.CatalogVersion + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=60")
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.writeJSON(w, http.StatusOK, payload)
}

func (h *DiscoveryHandler) getSchema(w http.ResponseWriter, r *http.Request) {
	snapshot := h.schema.ForPrincipalLocale(r.Context(), h.principal(r), r.URL.Query().Get("locale"))
	etag := `"` + snapshot.SchemaHash + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.writeJSON(w, http.StatusOK, snapshot)
}

func (h *DiscoveryHandler) getBusinessRuntimeSchema(w http.ResponseWriter, r *http.Request) {
	h.getPublishedRuntimeSchema(w, r)
}

func (h *DiscoveryHandler) getPortalRuntimeSchema(w http.ResponseWriter, r *http.Request) {
	h.getPublishedRuntimeSchema(w, r)
}

func (h *DiscoveryHandler) getPublishedRuntimeSchema(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.schema.PublishedRuntimeSchema(r.Context(), h.principal(r))
	if err != nil {
		h.writeError(w, r, http.StatusForbidden, "auth.permission_denied")
		return
	}
	etag := `"` + snapshot.SchemaHash + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.writeJSON(w, http.StatusOK, snapshot)
}

func (h *DiscoveryHandler) getBusinessSurfaceContext(w http.ResponseWriter, r *http.Request) {
	h.getPublishedSurfaceContext(w, r, "business_workspace")
}

func (h *DiscoveryHandler) getPortalSurfaceContext(w http.ResponseWriter, r *http.Request) {
	h.getPublishedSurfaceContext(w, r, "consumer_portal")
}

func (h *DiscoveryHandler) getPublishedSurfaceContext(w http.ResponseWriter, r *http.Request, surface string) {
	context, err := h.schema.PublishedSurfaceContext(r.Context(), surface, h.principal(r))
	if err != nil {
		h.writeError(w, r, http.StatusForbidden, "auth.permission_denied")
		return
	}
	h.writeJSON(w, http.StatusOK, context)
}
