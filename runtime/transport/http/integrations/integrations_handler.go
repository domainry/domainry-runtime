package integrations

import (
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	"net/http"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type IntegrationsHandler struct {
	runtimeExecution  *integrationapplication.IntegrationApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	admin             func(http.HandlerFunc) http.HandlerFunc
	authenticated     func(http.HandlerFunc) http.HandlerFunc
}

type IntegrationsDependencies struct {
	RuntimeExecution  *integrationapplication.IntegrationApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	Admin             func(http.HandlerFunc) http.HandlerFunc
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
}

func NewIntegrationsHandler(deps IntegrationsDependencies) *IntegrationsHandler {
	return &IntegrationsHandler{
		runtimeExecution: deps.RuntimeExecution,
		principal:        deps.Principal,
		writeJSON:        deps.WriteJSON, writeServiceError: deps.WriteServiceError,
		admin: deps.Admin, authenticated: deps.Authenticated,
	}
}
