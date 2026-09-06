package openapi

import (
	"net/http"

	"github.com/domainry/domainry-foundation/modulehttp"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
)

type OpenAPIHandler struct {
	schema             *appschemaapplication.ApplicationSchemaQueryApplicationService
	writeJSON          func(http.ResponseWriter, int, any)
	productBrandName   string
	moduleAdapters     []modulehttp.Adapter
	workspaceBootstrap runtimeext.WorkspaceBootstrapParticipant
}

type OpenAPIDependencies struct {
	Schema             *appschemaapplication.ApplicationSchemaQueryApplicationService
	WriteJSON          func(http.ResponseWriter, int, any)
	ProductBrandName   string
	ModuleAdapters     []modulehttp.Adapter
	WorkspaceBootstrap runtimeext.WorkspaceBootstrapParticipant
}

func NewOpenAPIHandler(deps OpenAPIDependencies) *OpenAPIHandler {
	return &OpenAPIHandler{schema: deps.Schema, writeJSON: deps.WriteJSON, productBrandName: productbrand.ResolveName(deps.ProductBrandName), moduleAdapters: append([]modulehttp.Adapter(nil), deps.ModuleAdapters...), workspaceBootstrap: deps.WorkspaceBootstrap}
}

func (h *OpenAPIHandler) openAPISpec(w http.ResponseWriter, r *http.Request) {
	snapshot := h.schema.Snapshot(r.Context())
	etag := `"` + snapshot.SchemaHash + `-openapi-` + productbrand.NameRevision(h.productBrandName) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=60")
	h.writeJSON(w, http.StatusOK, BuildWithWorkspaceBootstrap(snapshot, h.productBrandName, h.moduleAdapters, h.workspaceBootstrap))
}
