package integrations

import (
	"context"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	"net/http"
	"strconv"
	"strings"

	integrationvalidation "github.com/domainry/domainry-runtime/runtime/domain/integration/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

type IntegrationsHandler struct {
	connections       *integrationapplication.IntegrationApplicationService
	bindings          *integrationapplication.IntegrationApplicationService
	runtimeExecution  *integrationapplication.IntegrationApplicationService
	webhooks          *integrationapplication.IntegrationApplicationService
	operations        *operationsapplication.OperationsApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	decodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	admin             func(http.HandlerFunc) http.HandlerFunc
	authenticated     func(http.HandlerFunc) http.HandlerFunc
	entrypoint        func(http.HandlerFunc) http.HandlerFunc
	locale            func(*http.Request) string
	identity          identitysdk.Binding
	identityAudience  string
	productName       string
}

func (h *IntegrationsHandler) readQueryLimit(w http.ResponseWriter, r *http.Request, maximum int) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		h.writeServiceError(w, r, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.integration.query_limit_invalid", Params: map[string]string{"minimum": "1", "maximum": strconv.Itoa(maximum), "actual": raw}, Err: err})
		return 0, false
	}
	if err := integrationvalidation.IntegrationValidateReadLimit(limit, maximum); err != nil {
		h.writeServiceError(w, r, err)
		return 0, false
	}
	return limit, true
}

type IntegrationsDependencies struct {
	Connections       *integrationapplication.IntegrationApplicationService
	Bindings          *integrationapplication.IntegrationApplicationService
	RuntimeExecution  *integrationapplication.IntegrationApplicationService
	Webhooks          *integrationapplication.IntegrationApplicationService
	Operations        *operationsapplication.OperationsApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	Admin             func(http.HandlerFunc) http.HandlerFunc
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
	Entrypoint        func(http.HandlerFunc) http.HandlerFunc
	Locale            func(*http.Request) string
	Identity          identitysdk.Binding
	IdentityAudience  string
	ProductName       string
}

func NewIntegrationsHandler(deps IntegrationsDependencies) *IntegrationsHandler {
	return &IntegrationsHandler{
		connections: deps.Connections, bindings: deps.Bindings,
		runtimeExecution: deps.RuntimeExecution, webhooks: deps.Webhooks, operations: deps.Operations,
		principal: deps.Principal,
		writeJSON: deps.WriteJSON, writeError: deps.WriteError, writeServiceError: deps.WriteServiceError,
		decodeJSON: deps.DecodeJSON, admin: deps.Admin, authenticated: deps.Authenticated,
		entrypoint: deps.Entrypoint, locale: deps.Locale,
		identity: deps.Identity, identityAudience: strings.TrimSpace(deps.IdentityAudience), productName: strings.TrimSpace(deps.ProductName),
	}
}

func (h *IntegrationsHandler) executeOwnerOperation(r *http.Request, kind, resourceType, resourceID string, payload any, execute func(context.Context) (any, error)) (operationsapplication.OperationsOwnerExecutionResult, error) {
	if h.operations == nil {
		value, err := execute(r.Context())
		return operationsapplication.OperationsOwnerExecutionResult{Value: value}, err
	}
	return h.operations.ExecuteOwnerOperation(r.Context(), operationsapplication.OperationsOwnerExecutionRequest{
		Kind: kind, ResourceType: resourceType, ResourceID: resourceID, Payload: payload,
		Key: strings.TrimSpace(r.Header.Get("Idempotency-Key")), Reason: operationshttp.OwnerOperationReason(r, "operator requested "+kind), Reference: strings.TrimSpace(r.Header.Get("X-Operation-Reference")),
	}, h.principal(r), execute)
}
