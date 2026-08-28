package integration

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

var errIntegrationManagementTest = errors.New("integration management test failure")

func integrationManagementPrincipal(permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: permissions})
}

type integrationManagementConfigRepo struct {
	integrationrepository.IntegrationConfigRepository
	err         error
	connections []integrationmodel.IntegrationConnection
	secrets     []integrationmodel.IntegrationSecret
}

func (r *integrationManagementConfigRepo) ListSecrets(context.Context, string) ([]integrationmodel.IntegrationSecret, error) {
	if r.secrets != nil {
		return append([]integrationmodel.IntegrationSecret(nil), r.secrets...), r.err
	}
	return []integrationmodel.IntegrationSecret{{Key: "secret"}}, r.err
}
func (r *integrationManagementConfigRepo) ListAPIKeys(context.Context, string) ([]integrationmodel.IntegrationAPIKey, error) {
	return []integrationmodel.IntegrationAPIKey{{Key: "api"}}, r.err
}
func (r *integrationManagementConfigRepo) ListWebhookSubscriptions(context.Context, string, string, string, string, int) ([]integrationmodel.IntegrationWebhookSubscription, error) {
	return []integrationmodel.IntegrationWebhookSubscription{{Key: "subscription"}}, r.err
}
func (r *integrationManagementConfigRepo) ListExternalIdentities(context.Context, string) ([]integrationmodel.IntegrationExternalIdentity, error) {
	return []integrationmodel.IntegrationExternalIdentity{{Key: "external"}}, r.err
}
func (r *integrationManagementConfigRepo) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	return append([]integrationmodel.IntegrationConnection(nil), r.connections...), r.err
}

type integrationManagementEventRepo struct {
	integrationrepository.IntegrationEventRepository
	event       integrationmodel.IntegrationEvent
	found       bool
	duplicate   bool
	err         error
	updateErr   error
	scheduleErr error
	lastLimit   int
	intent      integrationmodel.IntegrationEventMappingIntent
}

func (r *integrationManagementEventRepo) ListEvents(_ context.Context, _, _, _ string, limit int) ([]integrationmodel.IntegrationEvent, error) {
	r.lastLimit = limit
	return []integrationmodel.IntegrationEvent{r.event}, r.err
}
func (r *integrationManagementEventRepo) GetEvent(context.Context, string, string) (integrationmodel.IntegrationEvent, bool, error) {
	return r.event, r.found, r.err
}
func (r *integrationManagementEventRepo) AcceptEvent(_ context.Context, _ string, event integrationmodel.IntegrationEvent, intent integrationmodel.IntegrationEventMappingIntent) (integrationmodel.IntegrationEvent, bool, error) {
	if r.err != nil {
		return integrationmodel.IntegrationEvent{}, false, r.err
	}
	event.ID = "event"
	r.event, r.found, r.intent = event, true, intent
	return event, r.duplicate, nil
}
func (r *integrationManagementEventRepo) UpdateEventStatus(_ context.Context, _, id, status, errorText string) (integrationmodel.IntegrationEvent, error) {
	if r.updateErr != nil {
		return integrationmodel.IntegrationEvent{}, r.updateErr
	}
	if r.err != nil {
		return integrationmodel.IntegrationEvent{}, r.err
	}
	r.event.ID, r.event.Status, r.event.Error = id, status, errorText
	return r.event, nil
}
func (r *integrationManagementEventRepo) ScheduleEventRetry(_ context.Context, _, id string, _ int, errorText string) (integrationmodel.IntegrationEvent, error) {
	if r.scheduleErr != nil {
		return integrationmodel.IntegrationEvent{}, r.scheduleErr
	}
	if r.err != nil {
		return integrationmodel.IntegrationEvent{}, r.err
	}
	r.event.ID, r.event.Status, r.event.Error, r.event.NextRetryAt = id, "received", errorText, "scheduled"
	return r.event, nil
}

type integrationManagementDeliveryRepo struct {
	integrationrepository.IntegrationDeliveryRepository
	integrationrepository.IntegrationOutboxReader
	invocations   []integrationmodel.IntegrationInvocation
	outboxes      []integrationmodel.IntegrationOutboxMessage
	found         bool
	err           error
	invocationErr error
	scheduleErr   error
	lastLimit     int
}

func (r *integrationManagementDeliveryRepo) ListInvocations(context.Context, string, string, string, string, string, int) ([]integrationmodel.IntegrationInvocation, error) {
	if r.invocationErr != nil {
		return nil, r.invocationErr
	}
	return append([]integrationmodel.IntegrationInvocation(nil), r.invocations...), r.err
}
func (r *integrationManagementDeliveryRepo) UpdateInvocationStatus(_ context.Context, _, id, status string, duration int64, responseRef, errorText string) (integrationmodel.IntegrationInvocation, error) {
	if r.err != nil {
		return integrationmodel.IntegrationInvocation{}, r.err
	}
	return integrationmodel.IntegrationInvocation{ID: id, Status: status, DurationMS: duration, ResponseRef: responseRef, Error: errorText}, nil
}
func (r *integrationManagementDeliveryRepo) ListOutbox(_ context.Context, _, _, _ string, limit int) ([]integrationmodel.IntegrationOutboxMessage, error) {
	r.lastLimit = limit
	return append([]integrationmodel.IntegrationOutboxMessage(nil), r.outboxes...), r.err
}
func (r *integrationManagementDeliveryRepo) InsertOutbox(_ context.Context, _ string, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	if r.err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.err
	}
	value.ID = "outbox-created"
	r.outboxes = append(r.outboxes, value)
	return value, nil
}
func (r *integrationManagementDeliveryRepo) GetOutbox(context.Context, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	if len(r.outboxes) == 0 {
		return integrationmodel.IntegrationOutboxMessage{}, r.found, r.err
	}
	return r.outboxes[0], r.found, r.err
}
func (r *integrationManagementDeliveryRepo) UpdateOutboxStatus(_ context.Context, _, id, status, responseRef, errorText string) (integrationmodel.IntegrationOutboxMessage, error) {
	if r.err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.err
	}
	return integrationmodel.IntegrationOutboxMessage{ID: id, Status: status, ResponseRef: responseRef, Error: errorText}, nil
}
func (r *integrationManagementDeliveryRepo) ScheduleOutboxRetry(_ context.Context, _, id string, _ int, errorText string) (integrationmodel.IntegrationOutboxMessage, error) {
	if r.scheduleErr != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.scheduleErr
	}
	if r.err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.err
	}
	return integrationmodel.IntegrationOutboxMessage{ID: id, Status: "queued", Error: errorText, NextAttemptAt: "scheduled"}, nil
}

func TestIntegrationManagementQueriesAndNormalizationEdges(t *testing.T) {
	config := &integrationManagementConfigRepo{connections: []integrationmodel.IntegrationConnection{{Key: "connection"}}}
	events := &integrationManagementEventRepo{event: integrationmodel.IntegrationEvent{ID: "event"}, found: true}
	delivery := &integrationManagementDeliveryRepo{invocations: []integrationmodel.IntegrationInvocation{
		{ID: "one", ProviderKey: "provider", Metadata: map[string]any{"external_principal": "external"}},
		{ID: "two", ProviderKey: "other"},
	}, outboxes: []integrationmodel.IntegrationOutboxMessage{{ID: "outbox"}}, found: true}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, EventRepository: events, DeliveryRepository: delivery})
	admin := integrationManagementPrincipal("workspace.admin", PermissionSecretManage, PermissionCatalogView, PermissionAuditView, PermissionInvoke, PermissionRetry)

	if values, err := service.ListIntegrationSecrets(t.Context(), admin); err != nil || len(values) != 1 {
		t.Fatalf("secrets=%#v err=%v", values, err)
	}
	if values, err := service.ListIntegrationAPIKeys(t.Context(), admin); err != nil || len(values) != 1 {
		t.Fatalf("api keys=%#v err=%v", values, err)
	}
	if values, err := service.ListIntegrationWebhookSubscriptions(t.Context(), " connector ", " event ", " active ", 0, admin); err != nil || len(values) != 1 {
		t.Fatalf("subscriptions=%#v err=%v", values, err)
	}
	if values, err := service.ListIntegrationExternalIdentities(t.Context(), admin); err != nil || len(values) != 1 {
		t.Fatalf("external identities=%#v err=%v", values, err)
	}
	if values, err := service.ListIntegrationEvents(t.Context(), " provider ", " failed ", 999, admin); err != nil || len(values) != 1 || events.lastLimit != 100 {
		t.Fatalf("events=%#v limit=%d err=%v", values, events.lastLimit, err)
	}
	if event, err := service.InspectIntegrationEvent(t.Context(), " event ", admin); err != nil || event.ID != "event" {
		t.Fatalf("event=%#v err=%v", event, err)
	}
	if values, err := service.ListIntegrationOutboxMessages(t.Context(), " connector ", " failed ", 999, admin); err != nil || len(values) != 1 || delivery.lastLimit != 100 {
		t.Fatalf("outbox=%#v limit=%d err=%v", values, delivery.lastLimit, err)
	}
	if message, err := service.InspectIntegrationOutboxMessage(t.Context(), " outbox ", admin); err != nil || message.ID != "outbox" {
		t.Fatalf("message=%#v err=%v", message, err)
	}
	if values, err := service.ListIntegrationInvocations(t.Context(), "", "", "", "", " provider ", " external ", 1, admin); err != nil || len(values) != 1 || values[0].ID != "one" {
		t.Fatalf("filtered invocations=%#v err=%v", values, err)
	}
	if values, err := service.ListIntegrationInvocations(t.Context(), "", "", "", "", "", "", 200, admin); err != nil || len(values) != 2 {
		t.Fatalf("direct invocations=%#v err=%v", values, err)
	}

	for _, value := range []string{"queued", "running", "succeeded", "failed", "cancelled"} {
		if got, err := NormalizeInvocationStatus(value); err != nil || got != value {
			t.Fatalf("invocation status %q=%q err=%v", value, got, err)
		}
	}
	if got, err := NormalizeInvocationStatus(""); err != nil || got != "queued" {
		t.Fatalf("default invocation status=%q err=%v", got, err)
	}
	if _, err := NormalizeInvocationStatus("invalid"); apperror.CodeOf(err) != "backend.integration.invocation.invalid_status" {
		t.Fatalf("invalid invocation status error=%v", err)
	}
	for _, value := range []string{"cancelled", "dead_letter", "delivered", "failed", "quarantined", "queued", "read", "sending", "sent"} {
		if got, err := NormalizeOutboxStatus(value); err != nil || got != value {
			t.Fatalf("outbox status %q=%q err=%v", value, got, err)
		}
	}
	if got, err := NormalizeOutboxStatus(""); err != nil || got != "queued" {
		t.Fatalf("default outbox status=%q err=%v", got, err)
	}
	if _, err := NormalizeOutboxStatus("invalid"); apperror.CodeOf(err) != "backend.integration.outbox.invalid_status" {
		t.Fatalf("invalid outbox status error=%v", err)
	}
	for _, value := range []string{"received", "processing", "processed", "ignored", "failed", "dead_letter", "quarantined"} {
		if got, err := NormalizeEventStatus(value); err != nil || got != value {
			t.Fatalf("event status %q=%q err=%v", value, got, err)
		}
	}
	if got, err := NormalizeEventStatus(""); err != nil || got != "received" {
		t.Fatalf("default event status=%q err=%v", got, err)
	}
	if _, err := NormalizeEventStatus("invalid"); apperror.CodeOf(err) != "backend.integration.event.invalid_status" {
		t.Fatalf("invalid event status error=%v", err)
	}
}

func TestIntegrationManagementMutationEdges(t *testing.T) {
	events := &integrationManagementEventRepo{}
	delivery := &integrationManagementDeliveryRepo{outboxes: []integrationmodel.IntegrationOutboxMessage{{ID: "outbox", ConnectorKey: "__automation__", Status: "failed"}}, found: true}
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: events, DeliveryRepository: delivery})
	invoker := integrationManagementPrincipal(PermissionInvoke)
	retry := integrationManagementPrincipal(PermissionRetry)
	if _, err := service.ScheduleIntegrationEventRetry(t.Context(), "event", integrationmodel.IntegrationEventRetryRequest{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("event retry authorization error=%v", err)
	}

	for _, request := range []integrationmodel.IntegrationEventRecordRequest{
		{}, {Provider: "provider"}, {Provider: "provider", EventType: "event"},
	} {
		if _, _, err := service.RecordIntegrationEvent(t.Context(), request, invoker); apperror.CodeOf(err) != "backend.integration.event.missing_identity" {
			t.Fatalf("missing event identity request=%#v err=%v", request, err)
		}
	}
	request := integrationmodel.IntegrationEventRecordRequest{Provider: " provider ", EventType: " event ", ExternalID: " external ", Payload: map[string]any{eventContextKey: "secret"}}
	invalidStatus := request
	invalidStatus.Status = "invalid"
	if _, _, err := service.RecordIntegrationEvent(t.Context(), invalidStatus, invoker); apperror.CodeOf(err) != "backend.integration.event.invalid_status" {
		t.Fatalf("invalid record status error=%v", err)
	}
	saved, duplicate, err := service.RecordIntegrationEvent(t.Context(), request, invoker)
	if err != nil || duplicate || saved.Payload[eventContextKey] != nil {
		t.Fatalf("recorded event=%#v duplicate=%v err=%v", saved, duplicate, err)
	}
	events.duplicate = true
	if _, duplicate, err := service.RecordIntegrationEvent(t.Context(), request, invoker); err != nil || !duplicate {
		t.Fatalf("duplicate=%v err=%v", duplicate, err)
	}
	events.err = errIntegrationManagementTest
	if _, _, err := service.RecordIntegrationEvent(t.Context(), request, invoker); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("record event error=%v", err)
	}
	events.err = nil
	if _, err := service.UpdateIntegrationEventStatus(t.Context(), " event ", integrationmodel.IntegrationEventStatusRequest{Status: "bad"}, invoker); err == nil {
		t.Fatal("invalid event update accepted")
	}
	if event, err := service.UpdateIntegrationEventStatus(t.Context(), " event ", integrationmodel.IntegrationEventStatusRequest{Status: "failed", Error: " failure "}, invoker); err != nil || event.Status != "failed" || event.Error != "failure" {
		t.Fatalf("updated event=%#v err=%v", event, err)
	}

	if _, err := service.ScheduleIntegrationEventRetry(t.Context(), " ", integrationmodel.IntegrationEventRetryRequest{}, retry); err == nil {
		t.Fatal("empty event retry accepted")
	}
	events.err = errIntegrationManagementTest
	if _, err := service.ScheduleIntegrationEventRetry(t.Context(), "event", integrationmodel.IntegrationEventRetryRequest{}, retry); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("event retry read error=%v", err)
	}
	events.err = nil
	events.found = false
	if _, err := service.ScheduleIntegrationEventRetry(t.Context(), "event", integrationmodel.IntegrationEventRetryRequest{}, retry); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("missing event retry error=%v", err)
	}
	events.found, events.event = true, integrationmodel.IntegrationEvent{ID: "event", Status: "received"}
	if _, err := service.ScheduleIntegrationEventRetry(t.Context(), "event", integrationmodel.IntegrationEventRetryRequest{}, retry); apperror.CodeOf(err) != "backend.integration.event.not_retryable" {
		t.Fatalf("non-retryable event error=%v", err)
	}
	events.event = integrationmodel.IntegrationEvent{ID: "event", Status: "failed", NextRetryAt: "already"}
	if event, err := service.ScheduleIntegrationEventRetry(t.Context(), "event", integrationmodel.IntegrationEventRetryRequest{}, retry); err != nil || event.NextRetryAt != "already" {
		t.Fatalf("already scheduled event=%#v err=%v", event, err)
	}
	for _, status := range []string{"dead_letter", "quarantined"} {
		events.event = integrationmodel.IntegrationEvent{ID: "event", Status: status}
		if _, err := service.ScheduleIntegrationEventRetry(t.Context(), "event", integrationmodel.IntegrationEventRetryRequest{}, retry); err != nil {
			t.Fatalf("retry status %q error=%v", status, err)
		}
	}
	events.event = integrationmodel.IntegrationEvent{ID: "event", Status: "failed", NextRetryAt: ""}
	events.event.NextRetryAt = ""
	events.scheduleErr = errIntegrationManagementTest
	if _, err := service.ScheduleIntegrationEventRetry(t.Context(), "event", integrationmodel.IntegrationEventRetryRequest{}, retry); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("event retry write error=%v", err)
	}
	events.scheduleErr = nil
	if event, err := service.ScheduleIntegrationEventRetry(t.Context(), "event", integrationmodel.IntegrationEventRetryRequest{DelaySeconds: 2, Error: " retry "}, retry); err != nil || event.NextRetryAt == "" {
		t.Fatalf("scheduled event=%#v err=%v", event, err)
	}

	if _, err := service.UpdateIntegrationInvocationStatus(t.Context(), "invocation", integrationmodel.IntegrationInvocationStatusRequest{DurationMS: -1}, invoker); err == nil {
		t.Fatal("negative invocation duration accepted")
	}
	if invocation, err := service.UpdateIntegrationInvocationStatus(t.Context(), " invocation ", integrationmodel.IntegrationInvocationStatusRequest{Status: "succeeded", DurationMS: 3, ResponseRef: " ref "}, invoker); err != nil || invocation.ID != "invocation" {
		t.Fatalf("invocation=%#v err=%v", invocation, err)
	}
	if message, err := service.UpdateIntegrationOutboxStatus(t.Context(), " outbox ", integrationmodel.IntegrationOutboxStatusRequest{Status: "sent", ResponseRef: " ref "}, invoker); err != nil || message.ID != "outbox" {
		t.Fatalf("outbox=%#v err=%v", message, err)
	}
	if message, err := service.ScheduleIntegrationOutboxRetry(t.Context(), " outbox ", integrationmodel.IntegrationOutboxRetryRequest{DelaySeconds: 2}, retry); err != nil || message.NextAttemptAt == "" {
		t.Fatalf("outbox retry=%#v err=%v", message, err)
	}
}

func TestReplayIntegrationEventStatusConditionEdges(t *testing.T) {
	events := &integrationManagementEventRepo{found: true}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	registry.RegisterEventHandler("provider", integrationEventWorkerHandlerFunc(func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
		return EventProcessDecision{}, nil
	}))
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: events, Registry: registry})
	principal := integrationManagementPrincipal(PermissionRetry)
	events.event = integrationmodel.IntegrationEvent{ID: "event", WorkspaceID: "workspace", Provider: "provider", Status: "received", Error: "backend.integration.event.manual_replay_queued"}
	if event, err := service.ReplayIntegrationEvent(t.Context(), "event", principal); err != nil || event.Status != "received" {
		t.Fatalf("already queued replay=%#v err=%v", event, err)
	}
	events.event.NextRetryAt = "scheduled"
	if _, err := service.ReplayIntegrationEvent(t.Context(), "event", principal); apperror.CodeOf(err) != "backend.integration.event.not_replayable" {
		t.Fatalf("scheduled received replay error=%v", err)
	}
	for _, status := range []string{"failed", "dead_letter", "quarantined", "ignored"} {
		events.event = integrationmodel.IntegrationEvent{ID: "event", WorkspaceID: "workspace", Provider: "provider", Status: status}
		if _, err := service.ReplayIntegrationEvent(t.Context(), "event", principal); err != nil {
			t.Fatalf("replay status %q error=%v", status, err)
		}
	}
}

func TestReplayIntegrationEventEdges(t *testing.T) {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	registry.RegisterEventHandler("provider", replayReadinessHandler{})
	events := &integrationManagementEventRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: events, Registry: registry})
	retry := integrationManagementPrincipal(PermissionRetry)

	if _, err := service.ReplayIntegrationEvent(t.Context(), "event", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown replay error=%v", err)
	}
	if _, err := service.ReplayIntegrationEvent(t.Context(), "event", integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("denied replay error=%v", err)
	}
	if _, err := service.ReplayIntegrationEvent(t.Context(), " ", retry); apperror.CodeOf(err) != "backend.integration.event.missing_identity" {
		t.Fatalf("empty replay error=%v", err)
	}
	events.err = errIntegrationManagementTest
	if _, err := service.ReplayIntegrationEvent(t.Context(), "event", retry); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("repository replay error=%v", err)
	}
	events.err, events.found = nil, false
	if _, err := service.ReplayIntegrationEvent(t.Context(), "event", retry); apperror.CodeOf(err) != "backend.integration.event.not_found" {
		t.Fatalf("missing replay error=%v", err)
	}
	events.found = true
	events.event = integrationmodel.IntegrationEvent{ID: "event", WorkspaceID: "other", Provider: "provider", Status: "failed"}
	if _, err := service.ReplayIntegrationEvent(t.Context(), "event", retry); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("replay readiness error=%v", err)
	}
	events.event = integrationmodel.IntegrationEvent{ID: "event", WorkspaceID: "workspace", Provider: "provider", Status: "processing"}
	if _, err := service.ReplayIntegrationEvent(t.Context(), "event", retry); apperror.CodeOf(err) != "backend.integration.event.not_replayable" {
		t.Fatalf("non-replayable error=%v", err)
	}
	events.event.Status = "received"
	if event, err := service.ReplayIntegrationEvent(t.Context(), "event", retry); err != nil || event.Status != "received" {
		t.Fatalf("idempotent replay event=%#v err=%v", event, err)
	}
	events.event.Error = "other"
	if _, err := service.ReplayIntegrationEvent(t.Context(), "event", retry); apperror.CodeOf(err) != "backend.integration.event.not_replayable" {
		t.Fatalf("received replay with error=%v", err)
	}
	events.event.Status, events.event.Error = "failed", "failure"
	if event, err := service.ReplayIntegrationEvent(t.Context(), " event ", retry); err != nil || event.Status != "received" || event.Error != "backend.integration.event.manual_replay_queued" {
		t.Fatalf("replayed event=%#v err=%v", event, err)
	}
	events.event.Status, events.updateErr = "failed", errIntegrationManagementTest
	if _, err := service.ReplayIntegrationEvent(t.Context(), "event", retry); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("replay update error=%v", err)
	}
}

func TestAcceptIntegrationEventIntentEdges(t *testing.T) {
	repository := &integrationManagementEventRepo{}
	event := integrationmodel.IntegrationEvent{WorkspaceID: "workspace", Provider: "provider", EventType: "created", ExternalID: "external"}
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: repository})
	if _, _, err := service.acceptIntegrationEvent(t.Context(), event); err != nil || repository.intent.TargetType != "unmatched" || repository.intent.Status != "pending" {
		t.Fatalf("unmatched intent=%#v err=%v", repository.intent, err)
	}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{{Key: "mapping", Provider: "provider", EventType: "created", TargetType: "workflow"}}})
	service = NewIntegrationApplicationService(ApplicationDependencies{EventRepository: repository, Registry: registry})
	if _, _, err := service.acceptIntegrationEvent(t.Context(), event); err != nil || repository.intent.MappingKey != "mapping" || repository.intent.TargetType != "workflow" {
		t.Fatalf("mapping intent=%#v err=%v", repository.intent, err)
	}
	registry.ReplaceSchema(integrationmodel.IntegrationSchema{})
	registry.RegisterEventHandler("provider", replayReadinessHandler{})
	if _, _, err := service.acceptIntegrationEvent(t.Context(), event); err != nil || repository.intent.MappingKey != "provider:provider" || repository.intent.TargetType != "provider_handler" {
		t.Fatalf("handler intent=%#v err=%v", repository.intent, err)
	}
}

func TestIntegrationManagementRepositoryAndPermissionFailures(t *testing.T) {
	config := &integrationManagementConfigRepo{err: errIntegrationManagementTest, connections: []integrationmodel.IntegrationConnection{{Key: "connection"}}}
	events := &integrationManagementEventRepo{err: errIntegrationManagementTest}
	delivery := &integrationManagementDeliveryRepo{err: errIntegrationManagementTest, found: false}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, EventRepository: events, DeliveryRepository: delivery})
	admin := integrationManagementPrincipal("workspace.admin", PermissionSecretManage, PermissionCatalogView, PermissionAuditView, PermissionInvoke, PermissionRetry)
	checks := []func() error{
		func() error { _, err := service.ListIntegrationSecrets(t.Context(), admin); return err },
		func() error { _, err := service.ListIntegrationAPIKeys(t.Context(), admin); return err },
		func() error { _, err := service.ListIntegrationEvents(t.Context(), "", "", 1, admin); return err },
		func() error { _, err := service.InspectIntegrationEvent(t.Context(), "event", admin); return err },
		func() error {
			_, err := service.ListIntegrationOutboxMessages(t.Context(), "", "", 1, admin)
			return err
		},
		func() error {
			_, err := service.InspectIntegrationOutboxMessage(t.Context(), "outbox", admin)
			return err
		},
		func() error {
			_, err := service.UpdateIntegrationInvocationStatus(t.Context(), "invocation", integrationmodel.IntegrationInvocationStatusRequest{}, admin)
			return err
		},
		func() error {
			_, err := service.UpdateIntegrationOutboxStatus(t.Context(), "outbox", integrationmodel.IntegrationOutboxStatusRequest{}, admin)
			return err
		},
	}
	for index, check := range checks {
		if err := check(); !errors.Is(err, errIntegrationManagementTest) {
			t.Fatalf("check %d error=%v", index, err)
		}
	}

	denied := integrationManagementPrincipal()
	for index, check := range []func() error{
		func() error { _, err := service.ListIntegrationSecrets(t.Context(), denied); return err },
		func() error { _, err := service.ListIntegrationAPIKeys(t.Context(), denied); return err },
		func() error { _, err := service.ListIntegrationEvents(t.Context(), "", "", 1, denied); return err },
		func() error {
			_, err := service.ListIntegrationWebhookSubscriptions(t.Context(), "", "", "", 1, denied)
			return err
		},
		func() error { _, err := service.ListIntegrationExternalIdentities(t.Context(), denied); return err },
		func() error {
			_, _, err := service.RecordIntegrationEvent(t.Context(), integrationmodel.IntegrationEventRecordRequest{}, denied)
			return err
		},
		func() error {
			_, err := service.UpdateIntegrationEventStatus(t.Context(), "", integrationmodel.IntegrationEventStatusRequest{}, denied)
			return err
		},
		func() error {
			_, err := service.ScheduleIntegrationEventRetry(t.Context(), "", integrationmodel.IntegrationEventRetryRequest{}, denied)
			return err
		},
		func() error {
			_, err := service.UpdateIntegrationInvocationStatus(t.Context(), "", integrationmodel.IntegrationInvocationStatusRequest{}, denied)
			return err
		},
		func() error {
			_, err := service.UpdateIntegrationOutboxStatus(t.Context(), "", integrationmodel.IntegrationOutboxStatusRequest{}, denied)
			return err
		},
	} {
		if apperror.CodeOf(check()) != "auth.permission_denied" {
			t.Fatalf("denied check %d", index)
		}
	}

	unsupported := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: &integrationManagementDeliveryRepo{found: false}})
	if _, err := unsupported.InspectIntegrationOutboxMessage(t.Context(), "missing", admin); apperror.CodeOf(err) != "backend.integration.outbox.not_found" {
		t.Fatalf("missing outbox error=%v", err)
	}
	nonReader := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: workspaceAuthorizationDeliveryProbe{calls: new(int)}})
	if _, err := nonReader.InspectIntegrationOutboxMessage(t.Context(), "missing", admin); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("unsupported outbox reader error=%v", err)
	}
}
