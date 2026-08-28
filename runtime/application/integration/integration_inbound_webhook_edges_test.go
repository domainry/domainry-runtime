package integration

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type integrationInboundEventRepository struct {
	integrationrepository.IntegrationEventRepository
	acceptErr     error
	nonceErr      error
	updateErr     error
	duplicate     bool
	accepted      integrationmodel.IntegrationEvent
	intent        integrationmodel.IntegrationEventMappingIntent
	statusUpdates []string
	nonces        map[string]bool
	acceptCount   int
}

func (r *integrationInboundEventRepository) AcceptEvent(_ context.Context, _ string, event integrationmodel.IntegrationEvent, intent integrationmodel.IntegrationEventMappingIntent) (integrationmodel.IntegrationEvent, bool, error) {
	if r.acceptErr != nil {
		return integrationmodel.IntegrationEvent{}, false, r.acceptErr
	}
	event.ID = "event-id"
	r.accepted, r.intent = event, intent
	r.acceptCount++
	return event, r.duplicate, nil
}

func (r *integrationInboundEventRepository) RecordWebhookNonce(_ context.Context, _, scope, nonce, _, _ string) (bool, error) {
	if r.nonceErr != nil {
		return false, r.nonceErr
	}
	if r.nonces == nil {
		r.nonces = map[string]bool{}
	}
	key := scope + ":" + nonce
	duplicate := r.nonces[key]
	r.nonces[key] = true
	return duplicate, nil
}

func (r *integrationInboundEventRepository) UpdateEventStatus(_ context.Context, _, eventID, status, errorText string) (integrationmodel.IntegrationEvent, error) {
	r.statusUpdates = append(r.statusUpdates, eventID+":"+status+":"+errorText)
	if r.updateErr != nil {
		return integrationmodel.IntegrationEvent{}, r.updateErr
	}
	return integrationmodel.IntegrationEvent{ID: eventID, Status: status, Error: errorText}, nil
}

type integrationInboundDeliveryRepository struct {
	integrationrepository.IntegrationDeliveryRepository
	updated     integrationmodel.IntegrationOutboxMessage
	found       bool
	err         error
	responseRef string
	status      string
	errorText   string
}

func (r *integrationInboundDeliveryRepository) UpdateOutboxStatusByResponseRef(_ context.Context, _, _, responseRef, status, errorText string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	r.responseRef, r.status, r.errorText = responseRef, status, errorText
	return r.updated, r.found, r.err
}

type integrationInboundConfigFaultRepository struct {
	*independentConfigRepository
	err error
}

func (r integrationInboundConfigFaultRepository) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	return nil, r.err
}

func integrationInboundWebhookService(connection integrationmodel.IntegrationConnection, verified integrationcontract.VerifiedInboundWebhook) (*IntegrationApplicationService, *integrationWebhookVerifierAdapter, *integrationInboundEventRepository, *integrationInboundDeliveryRepository, *[]string) {
	config := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{connection.Key: connection}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	events := &integrationInboundEventRepository{}
	delivery := &integrationInboundDeliveryRepository{}
	adapter := &integrationWebhookVerifierAdapter{verified: verified}
	audits := []string{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: connection.ConnectorKey, Provider: connection.ProviderKey, Providers: []integrationmodel.ConnectorProviderSchema{{Key: connection.ProviderKey}}}}})
	registerTestRegistryProvider(registry, connection.ConnectorKey, connection.ProviderKey, adapter)
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: config, EventRepository: events, DeliveryRepository: delivery, Registry: registry,
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, _ map[string]any) {
			audits = append(audits, event)
		},
	})
	return service, adapter, events, delivery, &audits
}

func TestReceiveIntegrationWebhookGuardsAndDependencyFailures(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "webhook", ProviderKey: "provider", Status: "active"}
	verified := integrationcontract.VerifiedInboundWebhook{EventType: "created", ExternalID: "external", Payload: map[string]any{"value": "ok"}}
	service, adapter, events, _, _ := integrationInboundWebhookService(connection, verified)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(ctx, "workspace", "primary", nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	for name, input := range map[string]struct {
		workspace  string
		connection string
		code       string
	}{
		"workspace":  {connection: "primary", code: "backend.workspace_scope_required"},
		"connection": {workspace: "workspace", code: "backend.integration.webhook.connection_required"},
		"missing":    {workspace: "workspace", connection: "missing", code: "backend.integration.connection.not_found"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), input.workspace, input.connection, nil, nil, nil)
			if apperror.CodeOf(err) != input.code {
				t.Fatalf("error = %v", err)
			}
		})
	}
	service.configRepo = integrationInboundConfigFaultRepository{independentConfigRepository: &independentConfigRepository{}, err: errIntegrationManagementTest}
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connection list error = %v", err)
	}
	service, _, _, _, _ = integrationInboundWebhookService(integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "webhook", ProviderKey: "provider", Status: "disabled"}, verified)
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); apperror.CodeOf(err) != "backend.integration.connection.not_found" {
		t.Fatalf("disabled error = %v", err)
	}
	connection.SecretRefs = map[string]string{"token": "plaintext"}
	service, _, _, _, _ = integrationInboundWebhookService(connection, verified)
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); apperror.CodeOf(err) != "backend.integration.secret_ref.must_be_reference" {
		t.Fatalf("secret error = %v", err)
	}
	connection.SecretRefs = nil
	service, adapter, _, _, _ = integrationInboundWebhookService(connection, verified)
	adapter.err = errIntegrationManagementTest
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); apperror.CodeOf(err) != errIntegrationManagementTest.Error() {
		t.Fatalf("verify error = %v", err)
	}
	service, adapter, _, _, _ = integrationInboundWebhookService(connection, verified)
	adapter.verified.ExternalID = ""
	adapter.verified.Payload = map[string]any{"unsupported": func() {}}
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); err == nil || !strings.Contains(err.Error(), "json") {
		t.Fatalf("fingerprint error = %v", err)
	}
	service, _, events, _, _ = integrationInboundWebhookService(connection, verified)
	events.acceptErr = errIntegrationManagementTest
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("accept error = %v", err)
	}
}

func TestInboundWebhookRemainingProjectionAndSecurityBoundaries(t *testing.T) {
	values := firstIntegrationValues(map[string][]string{"empty": {}, "present": {"first"}})
	if _, ok := values["empty"]; ok || values["present"] != "first" {
		t.Fatalf("values=%#v", values)
	}
	service := NewIntegrationApplicationService(ApplicationDependencies{})
	connection := integrationmodel.IntegrationConnection{Config: map[string]any{"inbound_security_profile": "invalid"}}
	if _, err := service.validateInboundWebhookSecurity(t.Context(), connection, integrationcontract.VerifiedInboundWebhook{}, time.Now()); apperror.CodeOf(err) != "backend.integration.webhook.security_profile_invalid" {
		t.Fatalf("invalid profile err=%v", err)
	}
	connection.Config["inbound_security_profile"] = integrationmodel.IntegrationInboundSecurityProfileDevice
	if _, err := service.validateInboundWebhookSecurity(t.Context(), connection, integrationcontract.VerifiedInboundWebhook{}, time.Now()); err == nil {
		t.Fatal("device webhook without security evidence accepted")
	}
}

func TestReceiveIntegrationWebhookFingerprintFailure(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "webhook", ProviderKey: "provider", Status: "active"}
	service, _, _, _, _ := integrationInboundWebhookService(connection, integrationcontract.VerifiedInboundWebhook{EventType: "created", ExternalID: "external"})
	original := integrationWebhookExternalEventID
	integrationWebhookExternalEventID = func(string, string, string, string, map[string]any) (string, bool, error) {
		return "", false, errIntegrationManagementTest
	}
	t.Cleanup(func() { integrationWebhookExternalEventID = original })
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("fingerprint err=%v", err)
	}
}

func TestReceiveIntegrationWebhookChallengeAndAcceptedEvent(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "webhook", ProviderKey: "provider", Status: "verified"}
	service, adapter, events, _, audits := integrationInboundWebhookService(connection, integrationcontract.VerifiedInboundWebhook{Challenge: "challenge", ChallengeFormat: "plain"})
	result, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), " workspace ", " primary ", map[string]string{"header": "value"}, map[string]string{"query": "value"}, []byte("body"))
	if err != nil || result.Challenge != "challenge" || result.ChallengeFormat != "plain" || events.accepted.ID != "" {
		t.Fatalf("challenge result=%#v event=%#v err=%v", result, events.accepted, err)
	}
	if adapter.request.Headers["header"] != "value" || len(adapter.request.HeaderValues["header"]) != 1 || adapter.request.HeaderValues["header"][0] != "value" {
		t.Fatalf("legacy webhook value projection=%#v", adapter.request)
	}
	result, err = service.ReceiveIntegrationWebhookValuesForWorkspace(t.Context(), "workspace", "primary", map[string][]string{"header": {"first", "second"}}, map[string][]string{"query": {"one", "two"}}, []byte("body"))
	if err != nil || result.Challenge != "challenge" || adapter.request.Headers["header"] != "first" || !reflect.DeepEqual(adapter.request.HeaderValues["header"], []string{"first", "second"}) || adapter.request.Query["query"] != "one" || !reflect.DeepEqual(adapter.request.QueryValues["query"], []string{"one", "two"}) {
		t.Fatalf("multi-value webhook projection=%#v result=%#v err=%v", adapter.request, result, err)
	}
	verified := integrationcontract.VerifiedInboundWebhook{EventType: "created", Payload: map[string]any{"password": "secret", "value": "ok"}}
	service, adapter, events, _, audits = integrationInboundWebhookService(connection, verified)
	events.duplicate = true
	result, err = service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil)
	if err != nil || result.Event == nil || !result.Duplicate || !strings.HasPrefix(result.Event.ExternalID, "fallback:") {
		t.Fatalf("accepted result=%#v err=%v", result, err)
	}
	if fallback, _ := events.accepted.Payload["_integration_external_id_fallback"].(bool); !fallback || events.accepted.Payload["password"] == "secret" || events.intent.TargetType != "unmatched" {
		t.Fatalf("accepted=%#v intent=%#v", events.accepted, events.intent)
	}
	if len(*audits) != 1 || (*audits)[0] != "integration_webhook_received" || adapter.request.ReceivedAt.IsZero() {
		t.Fatalf("audits=%#v request=%#v", *audits, adapter.request)
	}
	select {
	case locator := <-IntegrationEventWakeups(service):
		if locator != (IntegrationEventLocator{WorkspaceID: connection.WorkspaceID, EventID: result.Event.ID}) {
			t.Fatalf("webhook wake locator=%#v", locator)
		}
	default:
		t.Fatal("accepted webhook did not wake its exact event")
	}
}

func TestReceiveIntegrationWebhookExternalIdentityAndDeliveryReceiptFailures(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "webhook", ProviderKey: "provider", Status: "active", Config: map[string]any{
		"external_identity_mappings": map[string]any{"subject": map[string]any{"actor_id": "actor", "role_key": "member"}},
	}}
	verified := integrationcontract.VerifiedInboundWebhook{EventType: "created", ExternalID: "external", ExternalIdentity: &integrationcontract.WebhookExternalIdentity{Subject: "subject"}}
	service, _, events, _, _ := integrationInboundWebhookService(connection, verified)
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); apperror.CodeOf(err) != "backend.integration.webhook.external_identity_mapping_invalid" || len(events.statusUpdates) != 1 {
		t.Fatalf("identity error=%v updates=%#v", err, events.statusUpdates)
	}
	verified.ExternalIdentity = nil
	verified.DeliveryReceipt = &integrationcontract.WebhookDeliveryReceipt{ResponseRef: "response", Status: "invalid"}
	service, _, events, _, _ = integrationInboundWebhookService(connection, verified)
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); apperror.CodeOf(err) != "backend.integration.outbox.invalid_status" || len(events.statusUpdates) != 1 {
		t.Fatalf("status error=%v updates=%#v", err, events.statusUpdates)
	}
	verified.DeliveryReceipt.Status = "delivered"
	service, _, _, delivery, _ := integrationInboundWebhookService(connection, verified)
	delivery.err = errIntegrationManagementTest
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("delivery update error = %v", err)
	}
	service, _, _, delivery, _ = integrationInboundWebhookService(connection, verified)
	if result, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil); err != nil || result.Delivery != nil {
		t.Fatalf("missing delivery result=%#v err=%v", result, err)
	}
}

func TestReceiveIntegrationWebhookAppliesDeliveryReceipt(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "webhook", ProviderKey: "provider", Status: "active"}
	verified := integrationcontract.VerifiedInboundWebhook{EventType: "delivered", ExternalID: "external", DeliveryReceipt: &integrationcontract.WebhookDeliveryReceipt{ResponseRef: " response ", Status: " delivered ", Error: " provider detail "}}
	service, _, _, delivery, audits := integrationInboundWebhookService(connection, verified)
	delivery.found = true
	delivery.updated = integrationmodel.IntegrationOutboxMessage{ID: "outbox", WorkspaceID: "workspace", ConnectorKey: "webhook", ConnectionKey: "primary", RequestRef: "request", Status: "delivered"}
	result, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", "primary", nil, nil, nil)
	if err != nil || result.Delivery == nil || result.Delivery.ID != "outbox" || delivery.responseRef != "response" || delivery.status != "delivered" || delivery.errorText != "provider detail" {
		t.Fatalf("result=%#v delivery=%#v err=%v", result, delivery, err)
	}
	if len(*audits) != 2 || (*audits)[0] != "integration_outbox_delivery_receipt_applied" || (*audits)[1] != "integration_webhook_received" {
		t.Fatalf("audits = %#v", *audits)
	}
}

func TestReceiveDeviceWebhookRequiresSecurityEvidenceAndRejectsOneHundredReplaysBeforeBusinessDispatch(t *testing.T) {
	now := time.Now().UTC()
	connection := integrationmodel.IntegrationConnection{
		Key: "device-primary", WorkspaceID: "workspace", ConnectorKey: "device_gateway", ProviderKey: "sandbox", Status: "active",
		Config: map[string]any{"inbound_security_profile": "device", "inbound_event_max_skew_seconds": 300},
	}
	verified := integrationcontract.VerifiedInboundWebhook{
		EventType: "credential.presented", ExternalID: "device-event-1", Payload: map[string]any{"credential_ref": "opaque"},
		Security: &integrationcontract.WebhookSecurityEvidence{SignatureVerified: true, Nonce: "nonce-1", DeviceIdentity: "device-1", EventTime: now.Format(time.RFC3339Nano)},
	}
	service, adapter, events, _, _ := integrationInboundWebhookService(connection, verified)
	result, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", connection.Key, nil, nil, []byte("signed"))
	if err != nil || result.Event == nil || events.acceptCount != 1 {
		t.Fatalf("first device event result=%#v count=%d err=%v", result, events.acceptCount, err)
	}
	security, _ := events.accepted.Payload["_integration_security"].(map[string]any)
	if security["profile"] != "device" || security["device_identity"] != "device-1" || security["signature_verified"] != true {
		t.Fatalf("security evidence not preserved: %#v", events.accepted.Payload)
	}
	for replay := 1; replay < 100; replay++ {
		if _, replayErr := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", connection.Key, nil, nil, []byte("signed")); apperror.CodeOf(replayErr) != "backend.integration.webhook.replay_detected" {
			t.Fatalf("replay %d err=%v", replay, replayErr)
		}
	}
	if events.acceptCount != 1 {
		t.Fatalf("100 identical device deliveries created %d accepted events", events.acceptCount)
	}

	tests := []struct {
		name string
		edit func(*integrationcontract.VerifiedInboundWebhook)
		code string
	}{
		{name: "signature", edit: func(value *integrationcontract.VerifiedInboundWebhook) { value.Security.SignatureVerified = false }, code: "backend.integration.webhook.signature_required"},
		{name: "nonce", edit: func(value *integrationcontract.VerifiedInboundWebhook) { value.Security.Nonce = "" }, code: "backend.integration.webhook.nonce_required"},
		{name: "device", edit: func(value *integrationcontract.VerifiedInboundWebhook) { value.Security.DeviceIdentity = "" }, code: "backend.integration.webhook.device_identity_required"},
		{name: "event time", edit: func(value *integrationcontract.VerifiedInboundWebhook) { value.Security.EventTime = "invalid" }, code: "backend.integration.webhook.event_time_invalid"},
		{name: "external id", edit: func(value *integrationcontract.VerifiedInboundWebhook) { value.ExternalID = "" }, code: "backend.integration.webhook.external_event_id_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := verified
			securityCopy := *verified.Security
			candidate.Security = &securityCopy
			test.edit(&candidate)
			adapter.verified = candidate
			before := events.acceptCount
			if _, candidateErr := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", connection.Key, nil, nil, nil); apperror.CodeOf(candidateErr) != test.code {
				t.Fatalf("err=%v want=%s", candidateErr, test.code)
			}
			if events.acceptCount != before {
				t.Fatalf("invalid device event reached persistence")
			}
		})
	}
	adapter.verified = verified
	events.nonceErr = errIntegrationManagementTest
	verified.Security.Nonce = "nonce-2"
	adapter.verified = verified
	if _, err := service.ReceiveIntegrationWebhookForWorkspace(t.Context(), "workspace", connection.Key, nil, nil, nil); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("nonce repository error=%v", err)
	}
}
