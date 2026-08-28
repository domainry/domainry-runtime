package integration

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func providerHealth(kind, state, previous string) integrationmodel.IntegrationProviderResourceHealth {
	percent := 90
	return integrationmodel.IntegrationProviderResourceHealth{ObservationID: kind + ":2026072812", Kind: kind, State: state, PreviousState: previous, EvidenceSource: "provider_api", QuotaUsedPercent: &percent, CapabilityBlocked: state == "exhausted" || state == "payment_required", ObservedAt: "2026-07-28T12:00:00Z"}
}

func TestIntegrationProviderResourceHealthIntentMatrix(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "llm", WorkspaceID: "workspace", ProviderKey: "provider", Name: "AI provider", CreatedBy: "owner", UpdatedAt: "v1"}
	for _, test := range []struct {
		name, kind, state, previous, eventType, severity, alert string
		action                                                  string
	}{
		{name: "quota warning", kind: "quota", state: "warning", eventType: "integration.quota.warning", severity: "warning", alert: "firing"},
		{name: "quota exhausted", kind: "quota", state: "exhausted", eventType: "integration.quota.exhausted", severity: "critical", alert: "firing"},
		{name: "quota recovered", kind: "quota", state: "healthy", previous: "warning", eventType: "integration.quota.recovered", severity: "info", alert: "resolved", action: "completed"},
		{name: "billing required", kind: "billing", state: "payment_required", eventType: "integration.billing.payment_required", severity: "critical", alert: "firing"},
		{name: "billing recovered", kind: "billing", state: "healthy", previous: "payment_required", eventType: "integration.billing.recovered", severity: "info", alert: "resolved", action: "completed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := providerHealth(test.kind, test.state, test.previous)
			if test.kind == "billing" {
				report.QuotaUsedPercent, report.BalanceBand = nil, "low"
			}
			intent := integrationProviderResourceHealthIntent(connection, report)
			if intent.EventType != test.eventType || intent.Severity != test.severity || string(intent.AlertState) != test.alert || string(intent.ActionState) != test.action {
				t.Fatalf("intent=%+v", intent)
			}
			if intent.RecipientUserIDs[0] != "owner" || intent.SubjectID != "llm" || intent.GroupKey != "integration_resource:"+test.kind+":llm" || intent.OccurredAt != "2026-07-28T12:00:00Z" {
				t.Fatalf("routing=%+v", intent)
			}
			if test.kind == "quota" && intent.Variables["quota_used_percent"] != 90 {
				t.Fatalf("quota variables=%+v", intent.Variables)
			}
			if test.kind == "billing" && intent.Variables["balance_band"] != "low" {
				t.Fatalf("billing variables=%+v", intent.Variables)
			}
		})
	}
	report := providerHealth("quota", "exhausted", "warning")
	report.QuotaUsedPercent, report.EvidenceSource, report.ErrorCode = nil, "provider_error", "provider.quota_exhausted"
	if variables := integrationProviderResourceHealthIntent(connection, report).Variables; variables["error_code"] != "provider.quota_exhausted" {
		t.Fatalf("safe error variables=%+v", variables)
	}
	connection.Name = ""
	if got := integrationProviderResourceHealthIntent(connection, report).Variables["connection_name"]; got != "llm" {
		t.Fatalf("connection fallback=%v", got)
	}
}

func TestPublishProviderResourceHealthBoundaries(t *testing.T) {
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "publish provider resource health")
	connection := integrationmodel.IntegrationConnection{Key: "llm", WorkspaceID: "workspace", ProviderKey: "provider", Name: "AI", CreatedBy: "owner"}
	report := providerHealth("quota", "warning", "")
	var published notificationmodel.NotificationIntent
	service := NewIntegrationApplicationService(ApplicationDependencies{NotificationPublisher: func(_ context.Context, intent notificationmodel.NotificationIntent, actual principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error) {
		published = intent
		if actual.Purpose != scope.Purpose {
			t.Fatalf("scope=%+v", actual)
		}
		return notificationmodel.NotificationEvent{ID: intent.ID, EventType: intent.EventType}, true, nil
	}})
	if _, _, err := service.PublishProviderResourceHealth(t.Context(), connection, report, principalmodel.SystemScope{}); apperror.CodeOf(err) != "backend.system_scope_required" {
		t.Fatalf("scope error=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := service.PublishProviderResourceHealth(cancelled, connection, report, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	invalid := report
	invalid.ObservationID = "unsafe observation"
	if _, _, err := service.PublishProviderResourceHealth(t.Context(), connection, invalid, scope); apperror.CodeOf(err) != "backend.integration.resource_health.observation_id_invalid" {
		t.Fatalf("validation error=%v", err)
	}
	event, created, err := service.PublishProviderResourceHealth(t.Context(), connection, report, scope)
	if err != nil || !created || event.EventType != "integration.quota.warning" || published.SourceEventID == "" {
		t.Fatalf("event=%+v created=%v intent=%+v err=%v", event, created, published, err)
	}
	for _, missing := range []integrationmodel.IntegrationConnection{
		{Key: "llm", WorkspaceID: "workspace", CreatedBy: ""},
		{Key: "", WorkspaceID: "workspace", CreatedBy: "owner"},
		{Key: "llm", WorkspaceID: "", CreatedBy: "owner"},
	} {
		if _, created, err := service.PublishProviderResourceHealth(t.Context(), missing, report, scope); err != nil || created {
			t.Fatalf("missing connection=%+v created=%v err=%v", missing, created, err)
		}
	}
	if _, created, err := NewIntegrationApplicationService(ApplicationDependencies{}).PublishProviderResourceHealth(t.Context(), connection, report, scope); err != nil || created {
		t.Fatalf("missing publisher created=%v err=%v", created, err)
	}
	var nilService *IntegrationApplicationService
	if _, created, err := nilService.PublishProviderResourceHealth(t.Context(), connection, report, scope); err != nil || created {
		t.Fatalf("nil service created=%v err=%v", created, err)
	}
}

func TestNormalizeProviderResourceObservedAt(t *testing.T) {
	if got := normalizeProviderResourceObservedAt(" 2026-07-28T12:00:00+08:00 "); got != "2026-07-28T04:00:00Z" {
		t.Fatalf("normalized=%q", got)
	}
}

func TestSyncAndOutboxConnectorCallsPublishExplicitProviderResourceHealth(t *testing.T) {
	var intents []notificationmodel.NotificationIntent
	publisher := func(_ context.Context, intent notificationmodel.NotificationIntent, _ principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error) {
		intents = append(intents, intent)
		return notificationmodel.NotificationEvent{ID: intent.ID, EventType: intent.EventType}, true, nil
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "worker"}}, accessfixture.Bundle{Key: "integration_worker"})

	quota := providerHealth("quota", "warning", "")
	connection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active", CreatedBy: "owner"}
	config := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{"primary": connection}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "connector", Provider: "provider", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}},
		Operations: []integrationmodel.ConnectorOperationSchema{{Key: "call", SideEffect: "read"}},
	}}})
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: &independentDeliveryRepository{}, Registry: registry, NotificationPublisher: publisher})
	registerTestProviderAdapter(service, "connector", "provider", &integrationOutboxAdapter{result: integrationcontract.CallResult{Response: map[string]any{"ok": true}, ResourceHealth: &quota}})
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "connector", ConnectionKey: "primary", Operation: "call"}, principal); err != nil {
		t.Fatalf("sync call error=%v", err)
	}

	billing := providerHealth("billing", "payment_required", "")
	billing.QuotaUsedPercent, billing.BalanceBand = nil, "zero"
	outboxConnection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "email", ProviderKey: "provider", Status: "active", CreatedBy: "owner"}
	outboxAdapter := &integrationOutboxAdapter{result: integrationcontract.CallResult{ResponseRef: "provider:payment_required", ResourceHealth: &billing}, err: errors.New("provider payment required")}
	outboxService := integrationOutboxAdapterService(outboxConnection, outboxAdapter, &independentDeliveryRepository{})
	outboxService.publishNotification = publisher
	message := integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "workspace", ConnectorKey: "email", ConnectionKey: "primary", Operation: "send"}
	if _, err := outboxService.SendAdapterOutboxMessage(t.Context(), message, principal); err == nil {
		t.Fatal("outbox provider error was lost")
	}

	if len(intents) != 2 || intents[0].EventType != "integration.quota.warning" || intents[1].EventType != "integration.billing.payment_required" {
		t.Fatalf("published intents=%+v", intents)
	}
	for _, intent := range intents {
		if len(intent.RecipientUserIDs) != 1 || intent.RecipientUserIDs[0] != "owner" || intent.SubjectID != "primary" {
			t.Fatalf("resource health routing=%+v", intent)
		}
	}
}

func TestConnectorCallNotificationRecordingBranches(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", Status: "active", CreatedBy: "owner"}
	report := providerHealth("quota", "warning", "")
	publishErr := errors.New("publisher unavailable")
	service := NewIntegrationApplicationService(ApplicationDependencies{NotificationPublisher: func(context.Context, notificationmodel.NotificationIntent, principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error) {
		return notificationmodel.NotificationEvent{}, false, publishErr
	}})
	service.recordProviderResourceHealthFromCall(t.Context(), connection, nil)
	service.recordProviderResourceHealthFromCall(t.Context(), connection, &report)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	service.recordProviderResourceHealthFromCall(cancelled, connection, &report)

	invalidPrincipal := principalmodel.Principal{}
	service.recordCredentialRefreshRecovery(t.Context(), connection, invalidPrincipal)
	service.recordCredentialRefreshFailure(t.Context(), connection, invalidPrincipal, errors.New("refresh failed"))

	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	configured := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "worker"}}
	configured.recordCredentialRefreshRecovery(t.Context(), connection, principal)
	configured.recordCredentialRefreshFailure(t.Context(), connection, principal, errors.New("refresh failed"))
	if repository.connections["primary"].Status != "degraded" {
		t.Fatalf("credential failure was not recorded: %+v", repository.connections["primary"])
	}
}
