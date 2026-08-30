package appschema

import (
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	"net/http"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ApplicationSchemaHandler struct {
	definitions       *appschemaapplication.ApplicationSchemaApplicationService
	localizedTexts    *appschemaapplication.ApplicationSchemaApplicationService
	runtimeCatalog    *appschemaapplication.ApplicationSchemaApplicationService
	capabilities      *capabilityapplication.CapabilityAuthoringApplicationService
	audit             *auditapplication.AuditApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	decodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	admin             func(http.HandlerFunc) http.HandlerFunc
	authenticated     func(http.HandlerFunc) http.HandlerFunc
	legacyHeaders     func(http.ResponseWriter)
	provisionRequired http.HandlerFunc
}

type ApplicationSchemaDependencies struct {
	Definitions       *appschemaapplication.ApplicationSchemaApplicationService
	LocalizedTexts    *appschemaapplication.ApplicationSchemaApplicationService
	RuntimeCatalog    *appschemaapplication.ApplicationSchemaApplicationService
	Capabilities      *capabilityapplication.CapabilityAuthoringApplicationService
	Audit             *auditapplication.AuditApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	Admin             func(http.HandlerFunc) http.HandlerFunc
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
	LegacyHeaders     func(http.ResponseWriter)
	ProvisionRequired http.HandlerFunc
}

func NewApplicationSchemaHandler(deps ApplicationSchemaDependencies) *ApplicationSchemaHandler {
	return &ApplicationSchemaHandler{
		definitions: deps.Definitions, localizedTexts: deps.LocalizedTexts, runtimeCatalog: deps.RuntimeCatalog,
		capabilities: deps.Capabilities, audit: deps.Audit,
		principal: deps.Principal, writeJSON: deps.WriteJSON, writeError: deps.WriteError, writeServiceError: deps.WriteServiceError,
		decodeJSON: deps.DecodeJSON, admin: deps.Admin, authenticated: deps.Authenticated, legacyHeaders: deps.LegacyHeaders, provisionRequired: deps.ProvisionRequired,
	}
}
