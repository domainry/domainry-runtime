package appschema

import (
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	"net/http"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ApplicationSchemaHandler struct {
	definitions       *appschemaapplication.ApplicationSchemaApplicationService
	runtimeCatalog    *appschemaapplication.ApplicationSchemaApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	decodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	authenticated     func(http.HandlerFunc) http.HandlerFunc
}

type ApplicationSchemaDependencies struct {
	Definitions       *appschemaapplication.ApplicationSchemaApplicationService
	RuntimeCatalog    *appschemaapplication.ApplicationSchemaApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
}

func NewApplicationSchemaHandler(deps ApplicationSchemaDependencies) *ApplicationSchemaHandler {
	return &ApplicationSchemaHandler{
		definitions: deps.Definitions, runtimeCatalog: deps.RuntimeCatalog,
		principal: deps.Principal, writeJSON: deps.WriteJSON, writeError: deps.WriteError, writeServiceError: deps.WriteServiceError,
		decodeJSON: deps.DecodeJSON, authenticated: deps.Authenticated,
	}
}
