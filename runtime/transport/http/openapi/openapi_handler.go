package openapi

import (
	"net/http"

	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
)

type OpenAPIHandler struct {
	schema           *metadataapplication.MetadataSchemaApplicationService
	writeJSON        func(http.ResponseWriter, int, any)
	productBrandName string
}

type OpenAPIDependencies struct {
	Schema           *metadataapplication.MetadataSchemaApplicationService
	WriteJSON        func(http.ResponseWriter, int, any)
	ProductBrandName string
}

func NewOpenAPIHandler(deps OpenAPIDependencies) *OpenAPIHandler {
	return &OpenAPIHandler{schema: deps.Schema, writeJSON: deps.WriteJSON, productBrandName: productbrand.ResolveName(deps.ProductBrandName)}
}

func (h *OpenAPIHandler) openAPISpec(w http.ResponseWriter, r *http.Request) {
	snapshot := h.schema.Snapshot(r.Context())
	etag := `"` + snapshot.SchemaHash + `-openapi-` + productbrand.NameRevision(h.productBrandName) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=60")
	h.writeJSON(w, http.StatusOK, BuildWithProductBrand(snapshot, h.productBrandName))
}
