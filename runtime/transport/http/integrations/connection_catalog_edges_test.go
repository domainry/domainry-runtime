package integrations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

type catalogHTTPRepository struct {
	integrationrepository.IntegrationConfigRepository
	listCalls      int
	errorAt        int
	connections    []integrationmodel.IntegrationConnection
	dropAfterFirst bool
}

func TestExecuteOwnerOperationUsesConfiguredOperationsService(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "operator"}}
	handler := &IntegrationsHandler{
		operations: operationsapplication.NewOperationsApplicationService(nil, nil, nil, nil),
		principal:  func(*http.Request) principalmodel.Principal { return principal },
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	if _, err := handler.executeOwnerOperation(request, "unknown", "unknown", "resource", nil, func(context.Context) (any, error) {
		return nil, nil
	}); err == nil {
		t.Fatal("configured operations service was bypassed")
	}
}

func TestUpsertIntegrationConnectionUsesConfiguredOperationsService(t *testing.T) {
	var captured error
	handler := &IntegrationsHandler{
		connections: integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{ConfigRepository: &catalogHTTPRepository{}}),
		operations:  operationsapplication.NewOperationsApplicationService(nil, nil, nil, nil),
		principal: func(*http.Request) principalmodel.Principal {
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}
		},
		decodeJSON: func(_ http.ResponseWriter, _ *http.Request, target any) bool {
			request := target.(*integrationmodel.IntegrationConnectionUpsertRequest)
			request.ConnectorKey = "webhook"
			return true
		},
		writeServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) { captured = err },
		writeJSON:         func(http.ResponseWriter, int, any) {},
	}
	request := httptest.NewRequest(http.MethodPut, "/", nil)
	request.SetPathValue("connectionKey", "connection")
	handler.upsertIntegrationConnection(httptest.NewRecorder(), request)
	if captured == nil {
		t.Fatal("operations service error was not reported")
	}
}

func (r *catalogHTTPRepository) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	r.listCalls++
	if r.listCalls == r.errorAt {
		return nil, errors.New("list connections failed")
	}
	if r.dropAfterFirst && r.listCalls > 1 {
		return nil, nil
	}
	return r.connections, nil
}

func (r *catalogHTTPRepository) UpsertConnection(_ context.Context, workspaceID string, value integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	value.WorkspaceID = workspaceID
	r.connections = []integrationmodel.IntegrationConnection{value}
	return value, nil
}

type integrationOperationsRepository struct {
	receipt operationsmodel.OperationsReceipt
}

func (r *integrationOperationsRepository) RegisterOperationsCommand(_ context.Context, receipt operationsmodel.OperationsReceipt) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	r.receipt = receipt
	return receipt, operationsmodel.OperationsSubmissionAccepted, nil
}
func (r *integrationOperationsRepository) GetOperationsReceipt(context.Context, operationsmodel.OperationsScope, string) (operationsmodel.OperationsReceipt, bool, error) {
	return r.receipt, r.receipt.Command.ID != "", nil
}
func (*integrationOperationsRepository) ListOperationsReceipts(context.Context, operationsmodel.OperationsScope, operationsmodel.OperationsStatus, int) ([]operationsmodel.OperationsReceipt, error) {
	return nil, nil
}
func (*integrationOperationsRepository) SearchOperationsReceipts(context.Context, operationsmodel.OperationsScope, operationsmodel.OperationsReceiptFilter) (operationsmodel.OperationsReceiptPage, error) {
	return operationsmodel.OperationsReceiptPage{}, nil
}
func (r *integrationOperationsRepository) UpdateOperationsReceipt(_ context.Context, receipt operationsmodel.OperationsReceipt, _ operationsmodel.OperationsStatus) (bool, error) {
	r.receipt = receipt
	return true, nil
}

func TestUpsertIntegrationConnectionOperationsSuccess(t *testing.T) {
	repository := &catalogHTTPRepository{}
	connections := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{
		ConfigRepository: repository,
		Registry: integrationapplication.NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
			Key: "webhook", LifecycleStatus: "active", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "probe"}},
		}}}),
	})
	operations := operationsapplication.NewOperationsApplicationService(&integrationOperationsRepository{}, nil, func() time.Time {
		return time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	}, func() string { return "operation-1" })
	operations.UseDirectAuthoringProjection(func(context.Context, string, principalmodel.Principal) (capabilitycontract.CapabilityAuthoringSuccessProjection, error) {
		return capabilitycontract.CapabilityAuthoringSuccessProjection{SnapshotHash: "snapshot-1"}, nil
	})
	wrote := false
	handler := &IntegrationsHandler{
		connections: connections,
		operations:  operations,
		principal: func(*http.Request) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "builder"}}, accessfixture.Bundle{Permissions: []string{integrationapplication.PermissionConnectionManage}})
		},
		decodeJSON: func(_ http.ResponseWriter, _ *http.Request, target any) bool {
			*target.(*integrationmodel.IntegrationConnectionUpsertRequest) = integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "webhook", ProviderKey: "probe", Status: "configured"}
			return true
		},
		writeServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) { t.Fatalf("upsert error: %v", err) },
		writeJSON:         func(http.ResponseWriter, int, any) { wrote = true },
	}
	request := httptest.NewRequest(http.MethodPut, "/", nil)
	request = request.WithContext(requestcontext.WithRuntimeAuthoringBuilderTaskID(request.Context(), "task-1"))
	request.SetPathValue("connectionKey", "connection")
	request.Header.Set("Builder-Task-ID", "task-1")
	request.Header.Set("Idempotency-Key", "upsert-1")
	request.Header.Set("Expected-Schema-Hash", "empty")
	handler.upsertIntegrationConnection(httptest.NewRecorder(), request)
	if !wrote || len(repository.connections) != 1 {
		t.Fatalf("wrote=%v connections=%#v", wrote, repository.connections)
	}
}

func catalogHTTPHandler(repository *catalogHTTPRepository, connector integrationmodel.ConnectorSchema, capture *error) *IntegrationsHandler {
	service := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{
		ConfigRepository: repository,
		Registry:         integrationapplication.NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}}),
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{integrationapplication.PermissionCatalogView}})
	return &IntegrationsHandler{
		connections: service,
		principal:   func(*http.Request) principalmodel.Principal { return principal },
		locale:      func(*http.Request) string { return "en-US" },
		writeJSON:   func(http.ResponseWriter, int, any) {},
		writeServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) {
			*capture = err
		},
	}
}

func TestListIntegrationConnectorsHandlesOptionalConnectionsAndHashFailure(t *testing.T) {
	var captured error
	repository := &catalogHTTPRepository{errorAt: 2}
	handler := catalogHTTPHandler(repository, integrationmodel.ConnectorSchema{Key: "webhook", LifecycleStatus: "active"}, &captured)
	handler.listIntegrationConnectors(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/tenant-admin/integrations/connectors", nil))
	if captured != nil || repository.listCalls != 2 {
		t.Fatalf("optional connections error=%v calls=%d", captured, repository.listCalls)
	}

	captured = nil
	repository = &catalogHTTPRepository{}
	handler = catalogHTTPHandler(repository, integrationmodel.ConnectorSchema{Key: "invalid", LifecycleStatus: "active", Config: map[string]any{"unsupported": func() {}}}, &captured)
	handler.listIntegrationConnectors(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/tenant-admin/integrations/connectors", nil))
	if captured == nil {
		t.Fatal("connector contract hash failure was not reported")
	}
}

func TestIntegrationCatalogAndRouteFinalErrorBranches(t *testing.T) {
	var captured error
	repository := &catalogHTTPRepository{errorAt: 1}
	handler := catalogHTTPHandler(repository, integrationmodel.ConnectorSchema{Key: "webhook", LifecycleStatus: "active"}, &captured)
	handler.listIntegrationConnectors(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if captured == nil {
		t.Fatal("catalog repository error was not reported")
	}

	captured = nil
	repository = &catalogHTTPRepository{errorAt: 1}
	handler = catalogHTTPHandler(repository, integrationmodel.ConnectorSchema{Key: "webhook", LifecycleStatus: "active"}, &captured)
	handler.listIntegrationConnections(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if captured == nil {
		t.Fatal("connection list error was not reported")
	}

	for _, repository := range []*catalogHTTPRepository{
		{connections: []integrationmodel.IntegrationConnection{{Key: "connection"}}, errorAt: 2},
		{connections: []integrationmodel.IntegrationConnection{{Key: "connection"}}, dropAfterFirst: true},
	} {
		captured = nil
		handler = catalogHTTPHandler(repository, integrationmodel.ConnectorSchema{Key: "webhook", LifecycleStatus: "active"}, &captured)
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.SetPathValue("connectionKey", "connection")
		handler.getIntegrationConnection(httptest.NewRecorder(), request)
		if repository.dropAfterFirst && captured != nil {
			t.Fatalf("missing authoring hash should remain optional: %v", captured)
		}
		if !repository.dropAfterFirst && captured == nil {
			t.Fatal("authoring hash failure was not reported")
		}
	}

	executed := false
	handler = &IntegrationsHandler{}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	result, err := handler.executeOwnerOperation(request, "kind", "type", "id", nil, func(context.Context) (any, error) {
		executed = true
		return "value", nil
	})
	if err != nil || !executed || result.Value != "value" {
		t.Fatalf("owner result=%#v executed=%v error=%v", result, executed, err)
	}

	identity := func(next http.HandlerFunc) http.HandlerFunc { return next }
	handler = &IntegrationsHandler{admin: identity, entrypoint: identity}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	if handler.authenticated == nil {
		t.Fatal("admin fallback was not retained for all authenticated routes")
	}
}
