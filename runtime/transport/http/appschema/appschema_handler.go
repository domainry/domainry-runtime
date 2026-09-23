package appschema

import (
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	"net/http"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ApplicationSchemaHandler struct {
	runtimeCatalog    *appschemaapplication.ApplicationSchemaApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	authenticated     func(http.HandlerFunc) http.HandlerFunc
}

type ApplicationSchemaDependencies struct {
	RuntimeCatalog    *appschemaapplication.ApplicationSchemaApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
}

func NewApplicationSchemaHandler(deps ApplicationSchemaDependencies) *ApplicationSchemaHandler {
	return &ApplicationSchemaHandler{
		runtimeCatalog: deps.RuntimeCatalog,
		principal:      deps.Principal, writeJSON: deps.WriteJSON, writeError: deps.WriteError, writeServiceError: deps.WriteServiceError,
		authenticated: deps.Authenticated,
	}
}
