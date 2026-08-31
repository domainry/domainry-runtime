package openapi

import (
	"net/http"

	"github.com/domainry/domainry-foundation/modulehttp"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
)

type OpenAPIHandler struct {
	schema           *appschemaapplication.ApplicationSchemaQueryApplicationService
	writeJSON        func(http.ResponseWriter, int, any)
	productBrandName string
	moduleSurfaces   []modulehttp.Surface
}

type OpenAPIDependencies struct {
	Schema           *appschemaapplication.ApplicationSchemaQueryApplicationService
	WriteJSON        func(http.ResponseWriter, int, any)
	ProductBrandName string
	ModuleSurfaces   []modulehttp.Surface
}

func NewOpenAPIHandler(deps OpenAPIDependencies) *OpenAPIHandler {
	return &OpenAPIHandler{schema: deps.Schema, writeJSON: deps.WriteJSON, productBrandName: productbrand.ResolveName(deps.ProductBrandName), moduleSurfaces: append([]modulehttp.Surface(nil), deps.ModuleSurfaces...)}
}

func (h *OpenAPIHandler) openAPISpec(w http.ResponseWriter, r *http.Request) {
	snapshot := h.schema.Snapshot(r.Context())
	etag := `"` + snapshot.SchemaHash + `-openapi-` + productbrand.NameRevision(h.productBrandName) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=60")
	h.writeJSON(w, http.StatusOK, BuildWithModuleHTTPSurfaces(snapshot, h.productBrandName, h.moduleSurfaces))
}
