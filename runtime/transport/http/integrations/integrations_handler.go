package integrations

import (
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	"net/http"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type IntegrationsHandler struct {
	connections       *integrationapplication.IntegrationApplicationService
	runtimeExecution  *integrationapplication.IntegrationApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	decodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	admin             func(http.HandlerFunc) http.HandlerFunc
	authenticated     func(http.HandlerFunc) http.HandlerFunc
	webPushProxy      bool
}

type IntegrationsDependencies struct {
	Connections       *integrationapplication.IntegrationApplicationService
	RuntimeExecution  *integrationapplication.IntegrationApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	Admin             func(http.HandlerFunc) http.HandlerFunc
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
	WebPushProxy      bool
}

func NewIntegrationsHandler(deps IntegrationsDependencies) *IntegrationsHandler {
	return &IntegrationsHandler{
		connections: deps.Connections, runtimeExecution: deps.RuntimeExecution,
		principal: deps.Principal,
		writeJSON: deps.WriteJSON, writeServiceError: deps.WriteServiceError,
		decodeJSON: deps.DecodeJSON, admin: deps.Admin, authenticated: deps.Authenticated, webPushProxy: deps.WebPushProxy,
	}
}
