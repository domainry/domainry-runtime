package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
)

type syncCallDeadlineAdapter struct {
	timeout  time.Duration
	deadline time.Time
}

type syncCallParentCancelDeadlineAdapter struct {
	cancel context.CancelFunc
}

func (a syncCallParentCancelDeadlineAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	a.cancel()
	return integrationcontract.CallResult{}, context.DeadlineExceeded
}

func (syncCallParentCancelDeadlineAdapter) TestConnection(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{}, nil
}

func (syncCallParentCancelDeadlineAdapter) ValidateConfig(integrationmodel.IntegrationConnection) error {
	return nil
}

func (a *syncCallDeadlineAdapter) Call(ctx context.Context, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	a.timeout = request.Timeout
	a.deadline, _ = ctx.Deadline()
	<-ctx.Done()
	return integrationcontract.CallResult{}, ctx.Err()
}

type syncCallFallbackDeliveryRepository struct {
	integrationrepository.IntegrationDeliveryRepository
	updateErr error
}

func (r *syncCallFallbackDeliveryRepository) InsertInvocation(_ context.Context, _ string, value integrationmodel.IntegrationInvocation) (integrationmodel.IntegrationInvocation, error) {
	value.ID = "invocation"
	return value, nil
}
func (r *syncCallFallbackDeliveryRepository) UpdateInvocationStatus(_ context.Context, _, id, status string, duration int64, responseRef, errorText string) (integrationmodel.IntegrationInvocation, error) {
	if r.updateErr != nil {
		return integrationmodel.IntegrationInvocation{}, r.updateErr
	}
	return integrationmodel.IntegrationInvocation{ID: id, Status: status, DurationMS: duration, ResponseRef: responseRef, Error: errorText}, nil
}

func TestExecuteIntegrationSyncCallGuardEdges(t *testing.T) {
	principal := integrationManagementPrincipal("workspace.admin")
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})})
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{}, principal); apperror.CodeOf(err) != "backend.integration.invocation.missing_identity" {
		t.Fatalf("identity error=%v", err)
	}
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "missing"}, principal); apperror.CodeOf(err) != "backend.integration.invocation.missing_identity" {
		t.Fatalf("missing operation error=%v", err)
	}
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "missing", Operation: "call"}, principal); apperror.CodeOf(err) != "backend.integration.connector.not_found" {
		t.Fatalf("connector error=%v", err)
	}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider", OperationKeys: []string{"call"}}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "call", SideEffect: "read"}}}}})
	repository := &connectionResolutionRepo{}
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry})
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", Operation: "call"}, principal); apperror.CodeOf(err) != "backend.integration.connection.missing_key" {
		t.Fatalf("connection identity error=%v", err)
	}
	repository.listErr = errIntegrationManagementTest
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call"}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connection lookup error=%v", err)
	}
	repository.listErr = nil
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call"}, principal); apperror.CodeOf(err) != "backend.integration.connection_unavailable" {
		t.Fatalf("unavailable connection error=%v", err)
	}
	repository.connections = []integrationmodel.IntegrationConnection{{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active"}}
	repository.connections[0].ConnectorKey = "other"
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call"}, principal); apperror.CodeOf(err) != "backend.integration.connection_unavailable" {
		t.Fatalf("connection connector mismatch error=%v", err)
	}
	repository.connections[0].ConnectorKey = "connector"
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "missing"}, principal); apperror.CodeOf(err) != "backend.integration.operation.provider_unsupported" {
		t.Fatalf("unsupported operation error=%v", err)
	}
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call", SideEffect: "write"}, principal); apperror.CodeOf(err) != "backend.integration.invocation.side_effect_mismatch" {
		t.Fatalf("side effect mismatch error=%v", err)
	}
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call", SideEffect: "read"}, principal); apperror.CodeOf(err) != "backend.integration.sync_call.connector_unsupported" {
		t.Fatalf("adapter error=%v", err)
	}

	registry = NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider", OperationKeys: []string{"call"}}},
	}}})
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry})
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call"}, principal); apperror.CodeOf(err) != "backend.automation.connector_operation_not_found" {
		t.Fatalf("operation contract error=%v", err)
	}
}

func TestExecuteIntegrationSyncCallResponseAndFallbackRepositoryEdges(t *testing.T) {
	config := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{"connection": {Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active"}}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "call", SideEffect: "read"}}}}})
	delivery := &syncCallFallbackDeliveryRepository{}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: delivery, Registry: registry})
	registerTestProviderAdapter(service, "connector", "provider", lifecycleAdapter{})
	principal := integrationManagementPrincipal("workspace.admin")
	schema := &definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "ok", Type: "boolean", Required: true}}}
	if result, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call", ResponseSchema: schema}, principal); err != nil || result.Response["ok"] != true {
		t.Fatalf("validated response=%#v err=%v", result, err)
	}
	degraded := config.connections["connection"]
	degraded.Status = "degraded"
	config.connections["connection"] = degraded
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call"}, principal); err != nil {
		t.Fatalf("degraded recovery err=%v", err)
	}
	degraded.Status = "active"
	config.connections["connection"] = degraded
	invalidSchema := &definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "ok", Type: "number", Required: true}}}
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call", ResponseSchema: invalidSchema}, principal); apperror.CodeOf(err) != "backend.integration.sync_call.response_schema_invalid" {
		t.Fatalf("invalid response schema error=%v", err)
	}
	delivery.updateErr = errors.New("outcome update failed")
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call"}, principal); !errors.Is(err, delivery.updateErr) {
		t.Fatalf("fallback outcome update error=%v", err)
	}
}

func TestExecuteIntegrationSyncCallCapacityAndPolicyReferenceEdges(t *testing.T) {
	config := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{"connection": {Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active"}}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "call", SideEffect: "read"}}}}})
	controller := capacityplatform.NewController(capacityplatform.Limits{GlobalInFlight: 1, WorkspaceInFlight: 1, UseCaseInFlight: 1}, nil)
	hold, decision := controller.Acquire(t.Context(), capacityplatform.Request{WorkspaceID: "other", UseCase: "other", Essential: true})
	if !decision.Allowed {
		t.Fatalf("capacity setup rejected=%#v", decision)
	}
	defer hold.Release()
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: &independentDeliveryRepository{}, Registry: registry, ConnectorCapacity: controller})
	registerTestProviderAdapter(service, "connector", "provider", lifecycleAdapter{})
	principal := integrationManagementPrincipal("workspace.admin")
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call"}, principal); apperror.KindOf(err) != apperror.KindUnavailable {
		t.Fatalf("process capacity error kind=%q err=%v", apperror.KindOf(err), err)
	}
	hold.Release()
	for _, code := range []string{"backend.integration.sync_call.circuit_open", "backend.integration.sync_call.rate_limited"} {
		adapter := &adapterEdge{callErr: errors.New(code)}
		policyService := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: &independentDeliveryRepository{}, Registry: registry})
		registerTestProviderAdapter(policyService, "connector", "provider", adapter)
		if _, err := policyService.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call"}, principal); err == nil {
			t.Fatalf("policy error %q accepted", code)
		}
	}
	probe := &integrationResilienceStoreProbe{beforeErr: errIntegrationManagementTest}
	policyService := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: &independentDeliveryRepository{}, Registry: registry, PolicyStore: probe})
	registerTestProviderAdapter(policyService, "connector", "provider", lifecycleAdapter{})
	if _, err := policyService.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call", CircuitThreshold: 1}, principal); err == nil || err.Error() != errIntegrationManagementTest.Error() {
		t.Fatalf("pre-call policy error=%v", err)
	}
}

func TestSyncCallGovernanceComesFromOperationAndConnection(t *testing.T) {
	request := SyncCallRequest{
		Timeout: time.Hour, CircuitThreshold: 99, CircuitCooldown: time.Hour,
		RateLimitCount: 99, RateLimitWindow: time.Hour,
	}
	connection := integrationmodel.IntegrationConnection{Config: map[string]any{
		"sync_timeout_seconds": 8, "sync_circuit_failure_threshold": 3,
		"sync_circuit_cooldown_seconds": 7, "sync_rate_limit_count": 11,
		"sync_rate_limit_window_seconds": 13,
	}}
	operation := integrationmodel.ConnectorOperationSchema{TimeoutDefaultSeconds: 4, TimeoutMaxSeconds: 6}

	normalized := normalizeSyncCallGovernance(request, connection, operation)
	if normalized.Timeout != 6*time.Second {
		t.Fatalf("timeout=%s, want operation maximum 6s", normalized.Timeout)
	}
	if normalized.CircuitThreshold != 3 || normalized.CircuitCooldown != 7*time.Second {
		t.Fatalf("circuit policy=%d/%s", normalized.CircuitThreshold, normalized.CircuitCooldown)
	}
	if normalized.RateLimitCount != 11 || normalized.RateLimitWindow != 13*time.Second {
		t.Fatalf("rate policy=%d/%s", normalized.RateLimitCount, normalized.RateLimitWindow)
	}

	defaults := normalizeSyncCallGovernance(request, integrationmodel.IntegrationConnection{}, integrationmodel.ConnectorOperationSchema{})
	if defaults.Timeout != defaultSyncCallTimeout || defaults.CircuitThreshold != defaultSyncCircuitThreshold || defaults.CircuitCooldown != defaultSyncCircuitCooldown || defaults.RateLimitCount != 0 || defaults.RateLimitWindow != defaultSyncRateLimitWindow {
		t.Fatalf("default governance=%#v", defaults)
	}
}

func TestExecuteIntegrationSyncCallEnforcesRuntimeDeadline(t *testing.T) {
	config := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{
		"connection": {Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active"},
	}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}},
		Operations: []integrationmodel.ConnectorOperationSchema{{Key: "call", SideEffect: "read", TimeoutDefaultSeconds: 1, TimeoutMaxSeconds: 1}},
	}}})
	adapter := &syncCallDeadlineAdapter{}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: &independentDeliveryRepository{}, Registry: registry})
	registerTestProviderAdapter(service, "connector", "provider", adapter)
	started := time.Now()
	_, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call", Timeout: time.Hour}, integrationManagementPrincipal("workspace.admin"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error=%v", err)
	}
	if adapter.timeout != time.Second || adapter.deadline.IsZero() {
		t.Fatalf("adapter governance timeout=%s deadline=%s", adapter.timeout, adapter.deadline)
	}
	if len(service.deliveryRepo.(*independentDeliveryRepository).invocations) == 0 {
		t.Fatal("timed out call did not persist an invocation outcome")
	}
	outcome := service.deliveryRepo.(*independentDeliveryRepository).invocations[len(service.deliveryRepo.(*independentDeliveryRepository).invocations)-1]
	if outcome.Status != "failed" || outcome.ResponseRef != "policy:timeout" || outcome.Error != "backend.integration.sync_call.timeout" {
		t.Fatalf("timeout outcome=%#v", outcome)
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("runtime deadline elapsed=%s", elapsed)
	}
}

func TestExecuteIntegrationSyncCallActionContractIdentityAndCanceledParentEdges(t *testing.T) {
	config := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{
		"connection": {Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active"},
	}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	principal := integrationManagementPrincipal("workspace.admin")
	newService := func(operation integrationmodel.ConnectorOperationSchema, adapter integrationcontract.Adapter) *IntegrationApplicationService {
		registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
			Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}},
			Operations: []integrationmodel.ConnectorOperationSchema{operation},
		}}})
		service := NewIntegrationApplicationService(ApplicationDependencies{
			ConfigRepository: config, DeliveryRepository: &independentDeliveryRepository{}, Registry: registry,
		})
		registerTestProviderAdapter(service, "connector", "provider", adapter)
		return service
	}

	writeService := newService(
		integrationmodel.ConnectorOperationSchema{Key: "call", ExecutionMode: "sync", SideEffect: "write"},
		lifecycleAdapter{},
	)
	if _, err := writeService.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{
		ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call", ActionExecution: true,
	}, principal); apperror.CodeOf(err) != "backend.connector.action_side_effect_requires_outbox" {
		t.Fatalf("action write error=%v", err)
	}

	readOperation := integrationmodel.ConnectorOperationSchema{Key: "call", ExecutionMode: "sync", SideEffect: "read"}
	readService := newService(readOperation, lifecycleAdapter{})
	if result, err := readService.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{
		ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call", ActionExecution: true,
	}, principal); err != nil || result.ActionInvocation.Status != "succeeded" {
		t.Fatalf("action read result=%#v error=%v", result, err)
	}
	if _, err := readService.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{
		ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call",
		OperationMode: "wrong", ContractSHA256: "wrong",
	}, principal); err == nil {
		t.Fatal("operation identity mismatch was accepted")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancelAdapter := syncCallParentCancelDeadlineAdapter{cancel: cancel}
	cancelRegistry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider", OperationKeys: []string{"call"}}},
		Operations: []integrationmodel.ConnectorOperationSchema{readOperation},
	}}})
	cancelService := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository:   config,
		DeliveryRepository: &independentDeliveryRepository{},
		Registry: providerOverrideRegistry{
			Registry: cancelRegistry,
			adapter:  cancelAdapter,
			found:    true,
		},
	})
	if _, err := cancelService.ExecuteIntegrationSyncCall(ctx, SyncCallRequest{
		ConnectorKey: "connector", ConnectionKey: "connection", Operation: "call",
	}, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled parent error=%v", err)
	}
}
