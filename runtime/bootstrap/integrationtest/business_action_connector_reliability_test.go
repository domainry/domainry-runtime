package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/domainry/domainry-connector-sdk"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type p8GateReliabilityClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *p8GateReliabilityClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *p8GateReliabilityClock) Set(value time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = value.UTC()
}

type p8GateCenteredJitter struct{}

func (p8GateCenteredJitter) Duration(max time.Duration) time.Duration {
	return max / 2
}

type p8GateReliabilityState struct {
	mu               sync.Mutex
	attempts         map[string]int
	externalEffects  map[string]int
	verifiedWebhooks int
}

func newP8GateReliabilityState() *p8GateReliabilityState {
	return &p8GateReliabilityState{attempts: map[string]int{}, externalEffects: map[string]int{}}
}

func (s *p8GateReliabilityState) deliver(request connector.TypedRequest[p8GateRequest]) (connector.DeliveryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bookingID := request.Input.BookingID
	s.attempts[bookingID]++
	switch bookingID {
	case "booking-retry":
		if s.attempts[bookingID] == 1 {
			return connector.DeliveryResult{}, connector.RetryableError(
				"gate.temporarily_unavailable",
				errors.New("provider rejected request before execution"),
			)
		}
	case "booking-unknown":
		s.externalEffects[bookingID]++
		return connector.DeliveryResult{ResponseRef: "gate-access:" + bookingID}, connector.UncertainError(
			"gate.outcome_unknown",
			errors.New("provider accepted request but acknowledgement was lost"),
		)
	}
	s.externalEffects[bookingID]++
	return connector.DeliveryResult{ResponseRef: "gate-access:" + bookingID}, nil
}

func (s *p8GateReliabilityState) snapshot(bookingID string) (attempts, effects, verifiedWebhooks int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts[bookingID], s.externalEffects[bookingID], s.verifiedWebhooks
}

type p8GateReliabilityAdapter struct {
	connector.Adapter
	state *p8GateReliabilityState
}

func (a *p8GateReliabilityAdapter) VerifyWebhook(
	_ context.Context,
	request connector.VerifyWebhookRequest,
) (connector.VerifiedWebhook, error) {
	if signatures := request.Headers["X-Gate-Signature"]; len(signatures) != 1 || signatures[0] != "valid-gate-signature" {
		return connector.VerifiedWebhook{}, connector.PermanentError(
			"gate.webhook_signature_invalid",
			errors.New("gate webhook signature mismatch"),
		)
	}
	var body struct {
		EventID     string `json:"event_id"`
		BookingID   string `json:"booking_id"`
		ResponseRef string `json:"response_ref"`
	}
	if err := json.Unmarshal(request.Body, &body); err != nil || body.EventID == "" || body.BookingID == "" || body.ResponseRef == "" {
		return connector.VerifiedWebhook{}, connector.PermanentError(
			"gate.webhook_payload_invalid",
			err,
		)
	}
	payload, err := json.Marshal(map[string]string{"booking_id": body.BookingID})
	if err != nil {
		return connector.VerifiedWebhook{}, err
	}
	a.state.mu.Lock()
	a.state.verifiedWebhooks++
	a.state.mu.Unlock()
	return connector.VerifiedWebhook{
		EventType: "gate.access.confirmed", ExternalID: body.EventID, Payload: payload,
		DeliveryReceipt: &connector.WebhookDeliveryReceipt{
			ResponseRef: body.ResponseRef, Status: "delivered",
			OccurredAt: time.Date(2026, 7, 23, 19, 5, 0, 0, time.UTC),
		},
	}, nil
}

func TestProjectGateConnectorRetryUnknownAndWebhookConvergeDeterministically(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "gate-reliability.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(p8GateActionRecordTable); err != nil {
		t.Fatal(err)
	}

	state := newP8GateReliabilityState()
	providers := p8GateReliabilityProviders(t, state)
	configStore := integrationpersistence.NewIntegrationConfigStore(store)
	if _, err := configStore.UpsertConnection(t.Context(), "default", integrationmodel.IntegrationConnection{
		Key: p8GateConnectionKey, WorkspaceID: "default",
		ConnectorKey: p8GateConnectorKey, ProviderKey: p8GateProviderKey,
		Name: "Gym primary gate", Status: "active", Config: map[string]any{}, SecretRefs: map[string]string{},
	}); err != nil {
		t.Fatal(err)
	}
	clock := &p8GateReliabilityClock{now: time.Date(2026, 7, 23, 19, 0, 0, 0, time.UTC)}
	dependencies := objectActionTestDependencies(t.Context(), store)
	dependencies.ConnectorProviders = providers
	dependencies.Worker = workerplatform.Dependencies{Clock: clock, Jitter: p8GateCenteredJitter{}}
	services := NewRuntimeServices(t.Context(), RuntimeServicesConfig{Manifest: p8GateManifest(), Dependencies: dependencies})
	integrations := services.Applications().Integrations
	workerPrincipal := integrationruntime.IntegrationWorkerPrincipal("default")

	actionStore := actionpersistence.NewActionBusinessExecutionStore(store)
	bookingObject := definitionmodel.ObjectSchema{Key: p8GateBookingObjectKey, Fields: []definitionmodel.FieldSchema{
		{Key: "member_id", Type: "text"},
		{Key: "class_id", Type: "text"},
	}}
	retryOutboxID := commitP8GateAction(t, store, actionStore, bookingObject, "retry", false)
	first, err := integrations.ProcessDueIntegrationOutbox(t.Context(), 10, workerPrincipal)
	if err != nil || first.Retried != 1 || first.Sent != 0 || first.ReconciliationRequired != 0 {
		t.Fatalf("retry first attempt=%#v err=%v", first, err)
	}
	retryMessage := p8GateOutbox(t, store, retryOutboxID)
	if retryMessage.Status != "queued" || retryMessage.NextAttemptAt == "" ||
		retryMessage.Error != "backend.integration.provider_retryable" {
		t.Fatalf("retry Outbox after first attempt=%#v", retryMessage)
	}
	if attempts, effects, _ := state.snapshot("booking-retry"); attempts != 1 || effects != 0 {
		t.Fatalf("retry first attempt calls=%d external_effects=%d", attempts, effects)
	}
	notDue, err := integrations.ProcessDueIntegrationOutbox(t.Context(), 10, workerPrincipal)
	if err != nil || len(notDue.Messages) != 0 {
		t.Fatalf("retry ran before durable next_attempt_at: %#v err=%v", notDue, err)
	}
	nextAttemptAt, err := time.Parse(time.RFC3339, retryMessage.NextAttemptAt)
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(nextAttemptAt.Add(time.Second))
	second, err := integrations.ProcessDueIntegrationOutbox(t.Context(), 10, workerPrincipal)
	if err != nil || second.Sent != 1 || second.Retried != 0 || len(second.Messages) != 1 ||
		second.Messages[0].ID != retryOutboxID {
		t.Fatalf("retry second attempt=%#v err=%v", second, err)
	}
	if attempts, effects, _ := state.snapshot("booking-retry"); attempts != 2 || effects != 1 {
		t.Fatalf("retry final calls=%d external_effects=%d", attempts, effects)
	}
	if retryMessage = p8GateOutbox(t, store, retryOutboxID); retryMessage.Status != "sent" ||
		retryMessage.ResponseRef != "gate-access:booking-retry" {
		t.Fatalf("retry final Outbox=%#v", retryMessage)
	}

	unknownOutboxID := commitP8GateAction(t, store, actionStore, bookingObject, "unknown", false)
	unknown, err := integrations.ProcessDueIntegrationOutbox(t.Context(), 10, workerPrincipal)
	if err != nil || unknown.ReconciliationRequired != 1 || unknown.Retried != 0 || unknown.Sent != 0 {
		t.Fatalf("unknown attempt=%#v err=%v", unknown, err)
	}
	unknownMessage := p8GateOutbox(t, store, unknownOutboxID)
	if unknownMessage.Status != "quarantined" ||
		unknownMessage.Error != "backend.integration.outbox.outcome_uncertain" ||
		unknownMessage.ResponseRef != "gate-access:booking-unknown" {
		t.Fatalf("unknown Outbox=%#v", unknownMessage)
	}
	if attempts, effects, _ := state.snapshot("booking-unknown"); attempts != 1 || effects != 1 {
		t.Fatalf("unknown calls=%d external_effects=%d", attempts, effects)
	}
	noBlindRetry, err := integrations.ProcessDueIntegrationOutbox(t.Context(), 10, workerPrincipal)
	if err != nil || len(noBlindRetry.Messages) != 0 {
		t.Fatalf("quarantined Outbox was polled again: %#v err=%v", noBlindRetry, err)
	}
	if attempts, effects, _ := state.snapshot("booking-unknown"); attempts != 1 || effects != 1 {
		t.Fatalf("unknown outcome was blindly resent: calls=%d external_effects=%d", attempts, effects)
	}
	if _, err := integrations.ScheduleIntegrationOutboxRetry(
		t.Context(),
		unknownOutboxID,
		integrationmodel.IntegrationOutboxRetryRequest{},
		workerPrincipal,
	); serviceErrorCode(err) != "backend.integration.outbox.reconciliation_required" {
		t.Fatalf("manual retry accepted quarantined unknown outcome: %v", err)
	}

	webhookBody := []byte(`{"event_id":"gate-event-1","booking_id":"booking-unknown","response_ref":"gate-access:booking-unknown"}`)
	if _, err := integrations.ReceiveIntegrationWebhookValuesForWorkspace(
		t.Context(),
		"default",
		p8GateConnectionKey,
		map[string][]string{"X-Gate-Signature": {"invalid"}},
		nil,
		webhookBody,
	); err == nil {
		t.Fatal("invalid gate Webhook signature was accepted")
	}
	if persisted := p8GateOutbox(t, store, unknownOutboxID); persisted.Status != "quarantined" {
		t.Fatalf("invalid Webhook changed unknown Outbox=%#v", persisted)
	}
	webhook, err := integrations.ReceiveIntegrationWebhookValuesForWorkspace(
		t.Context(),
		"default",
		p8GateConnectionKey,
		map[string][]string{"X-Gate-Signature": {"valid-gate-signature"}},
		nil,
		webhookBody,
	)
	if err != nil || webhook.Event == nil || webhook.Event.ExternalID != "gate-event-1" ||
		webhook.Delivery == nil || webhook.Delivery.ID != unknownOutboxID || webhook.Delivery.Status != "delivered" {
		t.Fatalf("verified gate Webhook=%#v err=%v", webhook, err)
	}
	if attempts, effects, verified := state.snapshot("booking-unknown"); attempts != 1 || effects != 1 || verified != 1 {
		t.Fatalf("Webhook convergence calls=%d external_effects=%d verified=%d", attempts, effects, verified)
	}
	if delivered := p8GateOutbox(t, store, unknownOutboxID); delivered.Status != "delivered" ||
		delivered.ResponseRef != "gate-access:booking-unknown" {
		t.Fatalf("Webhook did not converge unknown Outbox=%#v", delivered)
	}
}

func p8GateReliabilityProviders(t *testing.T, state *p8GateReliabilityState) *connector.Registry {
	t.Helper()
	operation := connector.EnqueueOperation[p8GateRequest]{
		ConnectorKey: p8GateConnectorKey, ProviderKey: p8GateProviderKey,
		Key: p8GateOperationKey, ContractSHA256: p8GateContractSHA256,
		Reliability: connector.ReliabilityContract{
			Effect: connector.EffectWrite,
			Idempotency: connector.IdempotencyContract{
				Strategy: connector.IdempotencyProviderKey, KeyRetentionSeconds: 86400,
			},
			Reconciliation: connector.ReconciliationNone,
			Compensation:   connector.CompensationContract{Mode: connector.CompensationNone},
		},
	}
	bound, err := connector.BindEnqueueDelivery(
		operation,
		func(_ context.Context, request connector.TypedRequest[p8GateRequest]) (connector.DeliveryResult, error) {
			return state.deliver(request)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	base, err := connector.NewProvider(connector.ProviderSchema{
		ConnectorKey: p8GateConnectorKey, ProviderKey: p8GateProviderKey, ProviderRevision: "project-gate-v1",
	}, bound)
	if err != nil {
		t.Fatal(err)
	}
	registry := connector.NewRegistry()
	if err := registry.RegisterProviderSet(connector.ProviderSet{Providers: []connector.Adapter{
		&p8GateReliabilityAdapter{Adapter: base, state: state},
	}}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	return registry
}

func p8GateOutbox(
	t *testing.T,
	store *persistence.RuntimeStore,
	outboxID string,
) integrationmodel.IntegrationOutboxMessage {
	t.Helper()
	message, found, err := integrationpersistence.NewIntegrationDeliveryStore(store).GetOutbox(
		t.Context(),
		"default",
		outboxID,
	)
	if err != nil || !found {
		t.Fatalf("get gate Outbox %s: found=%v err=%v", outboxID, found, err)
	}
	return message
}
