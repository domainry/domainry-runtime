package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
	"github.com/domainry/domainry-runtime/runtime/platform/resilience"
)

type integrationAutomationHelperProbe struct{ err error }

type integrationResilienceStoreProbe struct {
	beforeErr     error
	recordErr     error
	beforeKey     string
	recordKey     string
	recordSuccess bool
	before        resilience.Config
	record        resilience.Config
}

func (p *integrationResilienceStoreProbe) Before(_ context.Context, key string, config resilience.Config, _ time.Time) error {
	p.beforeKey = key
	p.before = config
	return p.beforeErr
}
func (p *integrationResilienceStoreProbe) Record(_ context.Context, key string, config resilience.Config, success bool, _ time.Time) error {
	p.recordKey = key
	p.recordSuccess = success
	p.record = config
	return p.recordErr
}
func (*integrationResilienceStoreProbe) Semantics() string { return resilience.SemanticsInstanceLocal }

func (p integrationAutomationHelperProbe) ValidateIntegrationOutput(automationmodel.AutomationInstructionSchema, map[string]any) error {
	return p.err
}
func (integrationAutomationHelperProbe) ExecuteOutboxMessage(context.Context, integrationmodel.IntegrationOutboxMessage) error {
	return nil
}

func TestMockAdapterScenarioAndFallbackEdges(t *testing.T) {
	adapter := MockAdapter{}
	request := integrationcontract.CallRequest{Operation: "send", Request: map[string]any{"kind": "match"}}
	request.Connection.Config = map[string]any{"scenarios": map[string]any{"send": []any{
		"invalid",
		map[string]any{"when": map[string]any{}},
		map[string]any{"when": map[string]any{"kind": "other"}, "response": map[string]any{"ignored": true}},
		map[string]any{"when": map[string]any{"kind": "match"}, "delay_ms": 1, "response": map[string]any{"ok": true}},
	}}}
	result, err := adapter.Call(t.Context(), request)
	if err != nil || result.Response["ok"] != true || result.ResponseRef != "mock:send:scenario" {
		t.Fatalf("scenario result=%#v err=%v", result, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := adapter.Call(cancelled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("scenario cancellation=%v", err)
	}
	request.Connection.Config = map[string]any{"scenarios": map[string]any{"send": []map[string]any{{"when": map[string]any{"kind": "match"}, "error": "timeout"}}}}
	if _, err := adapter.Call(t.Context(), request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("scenario timeout=%v", err)
	}
	request.Connection.Config = map[string]any{"scenarios": map[string]any{"send": []any{map[string]any{"when": map[string]any{"kind": "match"}, "error": "provider.failed"}}}}
	if _, err := adapter.Call(t.Context(), request); err == nil || err.Error() != "provider.failed" {
		t.Fatalf("scenario provider error=%v", err)
	}
	request.Connection.Config = map[string]any{"scenarios": map[string]any{"send": []any{map[string]any{"when": map[string]any{"kind": "match"}}}}}
	if _, err := adapter.Call(t.Context(), request); err == nil {
		t.Fatal("scenario missing response accepted")
	}
	request.Connection.Config = map[string]any{"responses": map[string]any{"send": map[string]any{"source": "operation"}}}
	result, err = adapter.Call(t.Context(), request)
	if err != nil || result.Response["source"] != "operation" {
		t.Fatalf("operation response=%#v err=%v", result, err)
	}
	request.Connection.Config = map[string]any{"response": map[string]any{"source": "default"}}
	result, err = adapter.Call(t.Context(), request)
	if err != nil || result.Response["source"] != "default" {
		t.Fatalf("default response=%#v err=%v", result, err)
	}
	request.Connection.Config = map[string]any{}
	if _, err := adapter.Call(t.Context(), request); err == nil {
		t.Fatal("missing mock response accepted")
	}
	if mockScenarioMatches(nil, nil) || mockScenarioMatches(map[string]any{"key": "value"}, map[string]any{"key": "other"}) || !mockScenarioMatches(map[string]any{"key": 1}, map[string]any{"key": "1"}) {
		t.Fatal("mock scenario matcher mismatch")
	}
	if len(mockMap("invalid")) != 0 || len(mockMap(map[string]any{"ok": true})) != 1 || mockMapSlice("invalid") != nil || len(mockMapSlice([]map[string]any{{"a": true}})) != 1 {
		t.Fatal("mock conversion mismatch")
	}
}

func TestIntegrationWebhookAndRuntimeMapHelpers(t *testing.T) {
	config := map[string]any{"nil": nil, "blank": " ", "text": " value ", "int": 2, "int64": int64(3), "float": float64(4), "json": json.Number("5"), "string": "6", "bad": "bad"}
	if integrationConfigStringValue(config, "missing", "nil", "blank", "text") != "value" || integrationConfigStringValue(config, "missing") != "" {
		t.Fatal("config string lookup mismatch")
	}
	for key, want := range map[string]int64{"int": 2, "int64": 3, "float": 4, "json": 5, "string": 6} {
		if got := integrationConfigInt64(config, 9, key); got != want {
			t.Fatalf("config int %s=%d want=%d", key, got, want)
		}
	}
	if got := integrationConfigInt64(config, 9, "missing", "nil", "bad"); got != 9 {
		t.Fatalf("config int fallback=%d", got)
	}
	strategy := webhookSignatureStrategy(" hmac ", "", 5, "", "", "")
	if strategy["secret_ref_name"] != "webhook_secret" || strategy["signature_header"] != "X-Integration-Signature" || strategy["replay_protection"] != true {
		t.Fatalf("default strategy=%#v", strategy)
	}
	connection := integrationmodel.IntegrationConnection{Config: map[string]any{"signature_algorithm": "sha256", "signature_secret_ref_name": "custom", "max_skew_seconds": "7", "signature_header": "Sig", "signature_timestamp_header": "Time", "signature_nonce_header": "Nonce"}}
	strategy = webhookSignatureStrategyFromConnection(connection)
	if strategy["algorithm"] != "sha256" || strategy["secret_ref_name"] != "custom" || strategy["max_skew_seconds"] != int64(7) {
		t.Fatalf("connection strategy=%#v", strategy)
	}
	if value := integrationConfigStringValue(map[string]any{"value": "<nil>"}, "value"); value != "" {
		t.Fatalf("legacy nil string=%q", value)
	}
	if value := integrationConfigInt64(map[string]any{"value": "invalid"}, 9, "value"); value != 9 {
		t.Fatalf("invalid integer fallback=%d", value)
	}
	if value := integrationConfigInt64(map[string]any{"value": json.Number("invalid")}, 9, "value"); value != 9 {
		t.Fatalf("invalid JSON integer fallback=%d", value)
	}
	input := map[string]any{"value": true}
	if integrationMapFromAny(input)["value"] != true || len(integrationMapFromAny(nil)) != 0 || len(integrationMapFromAny(map[string]any(nil))) != 0 {
		t.Fatal("runtime map normalization mismatch")
	}
}

func TestIntegrationResiliencePolicyEdges(t *testing.T) {
	now := time.Now().UTC()
	connection := integrationmodel.IntegrationConnection{WorkspaceID: "workspace", Key: "connection", Config: map[string]any{"min_interval_seconds": 1, "circuit_failure_threshold": 0, "circuit_cooldown_seconds": 0}}
	withoutStore := NewIntegrationApplicationService(ApplicationDependencies{})
	if err := withoutStore.BeforeGenericWebhookSend(t.Context(), connection, now); err != nil {
		t.Fatalf("nil before policy=%v", err)
	}
	if err := withoutStore.AfterGenericWebhookSend(t.Context(), connection, true, now); err != nil {
		t.Fatalf("nil after policy=%v", err)
	}
	if err := withoutStore.BeforeGenericWebhookSend(t.Context(), integrationmodel.IntegrationConnection{}, now); err == nil {
		t.Fatal("invalid workspace accepted before policy")
	}
	if err := withoutStore.AfterGenericWebhookSend(t.Context(), integrationmodel.IntegrationConnection{}, true, now); err == nil {
		t.Fatal("invalid workspace accepted after policy")
	}
	store := resilience.NewMemoryStore(16)
	service := NewIntegrationApplicationService(ApplicationDependencies{PolicyStore: store})
	if err := service.BeforeGenericWebhookSend(t.Context(), connection, now); err != nil {
		t.Fatal(err)
	}
	if err := service.AfterGenericWebhookSend(t.Context(), connection, true, now); err != nil {
		t.Fatalf("record success=%v", err)
	}
	if err := service.BeforeGenericWebhookSend(t.Context(), connection, now); err == nil || err.Error() != "backend.integration.outbox.rate_limited" {
		t.Fatalf("rate limit error=%v", err)
	}
	if err := service.AfterGenericWebhookSend(t.Context(), connection, false, now); err != nil {
		t.Fatalf("record failure=%v", err)
	}
	if connectionCircuitKey(connection) != "workspace:connection" {
		t.Fatalf("circuit key=%q", connectionCircuitKey(connection))
	}
	req := SyncCallRequest{ConnectorKey: "connector", Operation: "send"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	if err := service.beforeSyncPolicy(t.Context(), req, connection, principal, now); err != nil || service.afterSyncPolicy(t.Context(), req, connection, principal, true, now) != nil {
		t.Fatalf("disabled sync policy before=%v", err)
	}
	req.RateLimitCount = 1
	if err := service.beforeSyncPolicy(t.Context(), req, connection, principal, now); err != nil {
		t.Fatalf("rate-only sync policy before=%v", err)
	}
	req.RateLimitCount = 0

	probe := &integrationResilienceStoreProbe{}
	service = NewIntegrationApplicationService(ApplicationDependencies{PolicyStore: probe})
	req.CircuitThreshold, req.RateLimitCount = 2, 3
	probe.beforeErr = resilience.ErrCircuitOpen
	if err := service.beforeSyncPolicy(t.Context(), req, connection, principal, now); err == nil || err.Error() != "backend.integration.sync_call.circuit_open" {
		t.Fatalf("sync circuit error=%v", err)
	}
	probe.beforeErr = resilience.ErrRateLimited
	if err := service.beforeSyncPolicy(t.Context(), req, connection, principal, now); err == nil || err.Error() != "backend.integration.sync_call.rate_limited" {
		t.Fatalf("sync rate error=%v", err)
	}
	probe.beforeErr = errIntegrationManagementTest
	if err := service.beforeSyncPolicy(t.Context(), req, connection, principal, now); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("sync store error=%v", err)
	}
	probe.beforeErr = nil
	probe.recordErr = errIntegrationManagementTest
	if err := service.afterSyncPolicy(t.Context(), req, connection, principal, true, now); !errors.Is(err, errIntegrationManagementTest) || probe.record.Cooldown != time.Minute {
		t.Fatalf("sync record error=%v config=%#v", err, probe.record)
	}
	req.CircuitCooldown = 2 * time.Second
	probe.recordErr = nil
	if err := service.afterSyncPolicy(t.Context(), req, connection, principal, false, now); err != nil || probe.record.Cooldown != 2*time.Second {
		t.Fatalf("sync record config=%#v err=%v", probe.record, err)
	}
	probe.beforeErr = resilience.ErrCircuitOpen
	if err := service.BeforeGenericWebhookSend(t.Context(), connection, now); err == nil || err.Error() != "backend.integration.outbox.circuit_open" {
		t.Fatalf("outbox circuit error=%v", err)
	}
	probe.beforeErr = resilience.ErrRateLimited
	if err := service.BeforeGenericWebhookSend(t.Context(), connection, now); err == nil || err.Error() != "backend.integration.outbox.rate_limited" {
		t.Fatalf("outbox rate error=%v", err)
	}
	probe.beforeErr = errIntegrationManagementTest
	if err := service.BeforeGenericWebhookSend(t.Context(), connection, now); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("outbox store error=%v", err)
	}
	probe.beforeErr, probe.recordErr = nil, nil
	if err := service.AfterGenericWebhookSend(t.Context(), connection, false, now); err != nil || probe.record.FailureThreshold != 5 || probe.record.Cooldown != time.Minute {
		t.Fatalf("outbox record config=%#v err=%v", probe.record, err)
	}
	connection.Config = map[string]any{"circuit_failure_threshold": 7, "circuit_cooldown_seconds": 9}
	if err := service.AfterGenericWebhookSend(t.Context(), connection, true, now); err != nil || probe.record.FailureThreshold != 7 || probe.record.Cooldown != 9*time.Second {
		t.Fatalf("explicit outbox record config=%#v err=%v", probe.record, err)
	}
}

func TestNormalizeSyncCallGovernanceRejectsNonPositiveOverrides(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Config: map[string]any{
		"sync_timeout_seconds": 0, "sync_circuit_failure_threshold": 0,
		"sync_circuit_cooldown_seconds": 0, "sync_rate_limit_count": -1, "sync_rate_limit_window_seconds": 0,
	}}
	operation := integrationmodel.ConnectorOperationSchema{TimeoutDefaultSeconds: 0, TimeoutMaxSeconds: 0}
	request := normalizeSyncCallGovernance(SyncCallRequest{}, connection, operation)
	if request.Timeout != defaultSyncCallTimeout || request.CircuitThreshold != defaultSyncCircuitThreshold ||
		request.CircuitCooldown != defaultSyncCircuitCooldown || request.RateLimitCount != 0 || request.RateLimitWindow != defaultSyncRateLimitWindow {
		t.Fatalf("request=%+v", request)
	}
}

func TestIntegrationRuntimeOrchestrationAndMetricsHelpers(t *testing.T) {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
		{Key: "mock", Provider: "mock", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "mock"}}},
		{Key: "webhook", Provider: "http", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "http"}}},
		{Key: "email", Provider: "smtp", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "smtp"}}},
	}})
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{
		"connection": {Key: "connection", WorkspaceID: "workspace", ConnectorKey: "webhook", ProviderKey: "http"},
	}}
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry, ConfigRepository: repository})
	principal := integrationManagementPrincipal(PermissionCatalogView)
	if _, err := service.IntegrationConnectionReferences(t.Context(), "connection", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}); err == nil {
		t.Fatal("reference query accepted missing workspace")
	}
	if references, err := service.IntegrationConnectionReferences(t.Context(), "connection", principal); err != nil || references != nil {
		t.Fatalf("nil schema references=%#v err=%v", references, err)
	}
	service.schema = func(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
		return metadatamodel.MetadataSchemaSnapshot{
			Actions:         []definitionmodel.ActionSchema{{Key: "action"}},
			AutomationRules: []automationmodel.AutomationRuleSchema{{Key: "automation", Instructions: []automationmodel.AutomationInstructionSchema{{ConnectionKey: "connection"}}}},
			Workflows:       []definitionmodel.WorkflowSchema{{Key: "workflow", Action: map[string]any{"connection_key": "connection"}}},
			Integrations:    integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{{Key: "mapping", Payload: map[string]any{"connection_key": "connection"}}}},
		}
	}
	if references, err := service.IntegrationConnectionReferences(t.Context(), "connection", principal); err != nil || len(references) != 3 {
		t.Fatalf("schema references=%#v err=%v", references, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.IntegrationConnectionReferences(cancelled, "connection", principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled references error=%v", err)
	}
	if _, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "webhook", "", "http", ""); err == nil {
		t.Fatal("delivery provider accepted missing workspace")
	}
	if _, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "missing", "", "", "workspace"); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("missing connector provider=%v", err)
	}
	if _, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "email", "connection", "", "workspace"); apperror.CodeOf(err) != "backend.integration.binding.connection_connector_mismatch" {
		t.Fatalf("connection mismatch=%v", err)
	}
	if provider, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "webhook", "connection", "", "workspace"); err != nil || provider != "http" {
		t.Fatalf("connection provider=%q err=%v", provider, err)
	}
	if provider, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "email", "", "smtp", "workspace"); err != nil || provider != "smtp" {
		t.Fatalf("requested provider=%q err=%v", provider, err)
	}
	service.RegisterSharedIntegrationOutboxSenders()
	service.RegisterDefaultIntegrationOutboxSenders()
	for _, key := range []string{"webhook", "email", "__automation__"} {
		if _, ok := registry.OutboxSender(key); !ok {
			t.Fatalf("outbox sender %q missing", key)
		}
	}
	if registry.HasEventHandlers() {
		t.Fatal("unexpected event handlers")
	}
	if err := service.ValidateAutomationOperationOutput(automationmodel.AutomationInstructionSchema{}, nil); err != nil {
		t.Fatalf("nil automation validation=%v", err)
	}
	service.automation = integrationAutomationHelperProbe{err: errIntegrationManagementTest}
	if err := service.ValidateAutomationOperationOutput(automationmodel.AutomationInstructionSchema{}, nil); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("automation validation=%v", err)
	}
	if (*IntegrationApplicationService)(nil).OperationalMetricsOpenMetrics(t.Context()) != "" || (*IntegrationApplicationService)(nil).QueueBackpressureActive(t.Context()) {
		t.Fatal("nil operational metrics mismatch")
	}
	service.UseQueueBackpressureThresholds(t.Context(), 3, time.Second)
	service.UseQueueBackpressureThresholds(t.Context(), 0, 0)
	if output := service.OperationalMetricsOpenMetrics(t.Context()); output == "" {
		t.Fatal("open metrics missing")
	}
	service.UseConnectorCapacity(t.Context(), nil)
	controller := capacityplatform.NewController(capacityplatform.Limits{GlobalInFlight: 1}, nil)
	service.UseConnectorCapacity(t.Context(), controller)
	if service.connectorCapacity != controller {
		t.Fatal("connector capacity was not replaced")
	}
	(*IntegrationApplicationService)(nil).UseConnectorCapacity(t.Context(), nil)
}

func TestCredentialKindCompatibilityEdges(t *testing.T) {
	for _, pair := range [][2]string{{"", "anything"}, {"generic_secret", "anything"}, {"anything", "generic_secret"}, {"api_key", "api_key"}, {"bearer_token", "api_key"}, {"signing_secret", "webhook_secret"}, {"connection_string", "database_password"}, {"identifier", "api_key"}, {"service_account", "api_key"}} {
		if !CredentialKindCompatible(pair[0], pair[1]) {
			t.Fatalf("expected compatible: %#v", pair)
		}
	}
	for _, pair := range [][2]string{{"bearer_token", "password"}, {"signing_secret", "api_key"}, {"connection_string", "api_key"}, {"identifier", "password"}, {"service_account", "password"}, {"unknown", "other"}} {
		if CredentialKindCompatible(pair[0], pair[1]) {
			t.Fatalf("unexpected compatible: %#v", pair)
		}
	}
}
