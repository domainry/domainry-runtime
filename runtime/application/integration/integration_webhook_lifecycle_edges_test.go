package integration

import (
	"context"
	"errors"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type integrationWebhookLifecycleRepository struct {
	*independentConfigRepository
	connectionErr   error
	subscriptionErr error
	upsertErr       error
	deleteErr       error
	deleteResult    bool
	cancelDelete    context.CancelFunc
}

func (r *integrationWebhookLifecycleRepository) DeleteWebhookSubscription(_ context.Context, workspaceID, subscriptionKey string) (bool, error) {
	if r.cancelDelete != nil {
		r.cancelDelete()
		r.cancelDelete = nil
	}
	if r.deleteErr != nil {
		return false, r.deleteErr
	}
	if !r.deleteResult {
		return false, nil
	}
	for index, subscription := range r.subscriptions {
		if subscription.WorkspaceID == workspaceID && subscription.Key == subscriptionKey {
			r.subscriptions = append(r.subscriptions[:index], r.subscriptions[index+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func newIntegrationWebhookLifecycleRepository() *integrationWebhookLifecycleRepository {
	return &integrationWebhookLifecycleRepository{independentConfigRepository: &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{}, subscriptions: []integrationmodel.IntegrationWebhookSubscription{},
	}}
}

func (r *integrationWebhookLifecycleRepository) ListConnections(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationConnection, error) {
	if r.connectionErr != nil {
		return nil, r.connectionErr
	}
	return r.independentConfigRepository.ListConnections(ctx, workspaceID)
}

func (r *integrationWebhookLifecycleRepository) ListWebhookSubscriptions(ctx context.Context, workspaceID, connectorKey, eventType, status string, limit int) ([]integrationmodel.IntegrationWebhookSubscription, error) {
	if r.subscriptionErr != nil {
		return nil, r.subscriptionErr
	}
	return r.independentConfigRepository.ListWebhookSubscriptions(ctx, workspaceID, connectorKey, eventType, status, limit)
}

func (r *integrationWebhookLifecycleRepository) UpsertWebhookSubscription(ctx context.Context, workspaceID string, value integrationmodel.IntegrationWebhookSubscription) (integrationmodel.IntegrationWebhookSubscription, error) {
	if r.upsertErr != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, r.upsertErr
	}
	return r.independentConfigRepository.UpsertWebhookSubscription(ctx, workspaceID, value)
}

type integrationWebhookDeliveryRepository struct {
	integrationrepository.IntegrationDeliveryRepository
	insertErr error
	inserted  []integrationmodel.IntegrationOutboxMessage
}

func (r *integrationWebhookDeliveryRepository) InsertOutbox(_ context.Context, _ string, message integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	if r.insertErr != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.insertErr
	}
	message.ID = "outbox"
	r.inserted = append(r.inserted, message)
	return message, nil
}

func TestUpsertIntegrationWebhookSubscriptionFailureEdges(t *testing.T) {
	principal := integrationRotationPrincipal(PermissionConnectionManage)
	repository := newIntegrationWebhookLifecycleRepository()
	repository.connections["connection"] = integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "webhook", Status: "active"}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, ConnectorExists: func(key string) bool { return key == "webhook" }})
	valid := integrationmodel.IntegrationWebhookSubscriptionUpsertRequest{ConnectorKey: "webhook", ConnectionKey: "connection", EventTypes: []string{"created"}}
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", valid, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace guard=%v", err)
	}
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", valid, integrationRotationPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission guard=%v", err)
	}
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", integrationmodel.IntegrationWebhookSubscriptionUpsertRequest{}, principal); apperror.CodeOf(err) != "backend.integration.webhook_subscription.missing_connection" {
		t.Fatalf("missing connection=%v", err)
	}
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", integrationmodel.IntegrationWebhookSubscriptionUpsertRequest{ConnectorKey: "webhook"}, principal); apperror.CodeOf(err) != "backend.integration.webhook_subscription.missing_connection" {
		t.Fatalf("missing connection key=%v", err)
	}
	missingConnector := valid
	missingConnector.ConnectorKey = "missing"
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", missingConnector, principal); apperror.CodeOf(err) != "backend.integration.connector.not_found" {
		t.Fatalf("missing connector=%v", err)
	}
	repository.connectionErr = errors.New("connection list failed")
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", valid, principal); !errors.Is(err, repository.connectionErr) {
		t.Fatalf("connection list error=%v", err)
	}
	repository.connectionErr = nil
	valid.ConnectionKey = "missing"
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", valid, principal); apperror.CodeOf(err) != "backend.integration.connection.not_found" {
		t.Fatalf("missing connection=%v", err)
	}
	valid.ConnectionKey = "connection"
	repository.connections["connection"] = integrationmodel.IntegrationConnection{Key: "connection", ConnectorKey: "other", Status: "active"}
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", valid, principal); apperror.CodeOf(err) != "backend.integration.webhook_subscription.connector_mismatch" {
		t.Fatalf("connector mismatch=%v", err)
	}
	repository.connections["connection"] = integrationmodel.IntegrationConnection{Key: "connection", ConnectorKey: "webhook", Status: "disabled"}
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", valid, principal); apperror.CodeOf(err) != "backend.integration.connection.disabled" {
		t.Fatalf("disabled connection=%v", err)
	}
	repository.connections["connection"] = integrationmodel.IntegrationConnection{Key: "connection", ConnectorKey: "webhook", Status: "active"}
	for name, request := range map[string]integrationmodel.IntegrationWebhookSubscriptionUpsertRequest{
		"event type": {ConnectorKey: "webhook", ConnectionKey: "connection", EventTypes: []string{" "}},
		"status":     {ConnectorKey: "webhook", ConnectionKey: "connection", Status: "invalid"},
	} {
		if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", request, principal); err == nil {
			t.Fatalf("invalid %s accepted", name)
		}
	}
	repository.subscriptionErr = errors.New("subscription list failed")
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", valid, principal); !errors.Is(err, repository.subscriptionErr) {
		t.Fatalf("subscription list error=%v", err)
	}
	repository.subscriptionErr = nil
	repository.upsertErr = errors.New("subscription write failed")
	if _, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "key", valid, principal); !errors.Is(err, repository.upsertErr) {
		t.Fatalf("subscription write error=%v", err)
	}
}

func TestUpsertAndDisableIntegrationWebhookSubscriptionAuditEdges(t *testing.T) {
	principal := integrationRotationPrincipal(PermissionConnectionManage)
	repository := newIntegrationWebhookLifecycleRepository()
	repository.connections["connection"] = integrationmodel.IntegrationConnection{Key: "connection", ConnectorKey: "webhook", Status: "active"}
	var auditEvents []string
	var before map[string]any
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		ConnectorExists:  func(key string) bool { return key == "webhook" },
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, gotBefore, _ map[string]any, _ map[string]any) {
			auditEvents, before = append(auditEvents, event), gotBefore
		},
	})
	request := integrationmodel.IntegrationWebhookSubscriptionUpsertRequest{Name: " Generated ", ConnectorKey: "webhook", ConnectionKey: "connection", Description: " Description "}
	created, err := service.UpsertIntegrationWebhookSubscription(t.Context(), "", request, principal)
	if err != nil || created.Key == "" || created.Name != "Generated" || created.Description != "Description" || len(created.EventTypes) != 1 || created.EventTypes[0] != "*" || before != nil {
		t.Fatalf("created=%#v before=%#v err=%v", created, before, err)
	}
	created.EventTypes = nil
	repository.subscriptions = []integrationmodel.IntegrationWebhookSubscription{created}
	updated, err := service.UpsertIntegrationWebhookSubscription(t.Context(), created.Key, request, principal)
	if err != nil || before["key"] != created.Key || before["event_types"] == nil {
		t.Fatalf("updated=%#v before=%#v err=%v", updated, before, err)
	}

	if _, err := service.DisableIntegrationWebhookSubscription(t.Context(), created.Key, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("disable workspace=%v", err)
	}
	if _, err := service.DisableIntegrationWebhookSubscription(t.Context(), created.Key, integrationRotationPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("disable permission=%v", err)
	}
	repository.subscriptionErr = errors.New("subscription list failed")
	if _, err := service.DisableIntegrationWebhookSubscription(t.Context(), created.Key, principal); !errors.Is(err, repository.subscriptionErr) {
		t.Fatalf("disable list error=%v", err)
	}
	repository.subscriptionErr = nil
	if _, err := service.DisableIntegrationWebhookSubscription(t.Context(), "missing", principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("disable missing=%v", err)
	}
	repository.upsertErr = errors.New("subscription write failed")
	if _, err := service.DisableIntegrationWebhookSubscription(t.Context(), created.Key, principal); !errors.Is(err, repository.upsertErr) {
		t.Fatalf("disable write error=%v", err)
	}
	repository.upsertErr = nil
	disabled, err := service.DisableIntegrationWebhookSubscription(t.Context(), created.Key, principal)
	if err != nil || disabled.Status != "disabled" || disabled.DisabledAt == "" || auditEvents[len(auditEvents)-1] != "integration_webhook_subscription_disabled" {
		t.Fatalf("disabled=%#v audits=%#v err=%v", disabled, auditEvents, err)
	}
}

func TestDeleteIntegrationWebhookSubscriptionIsBackendGovernedAndAudited(t *testing.T) {
	principal := integrationRotationPrincipal(PermissionConnectionManage)
	repository := newIntegrationWebhookLifecycleRepository()
	subscription := integrationmodel.IntegrationWebhookSubscription{Key: "orders", WorkspaceID: "workspace", ConnectorKey: "webhook", ConnectionKey: "connection", Status: "disabled"}
	repository.subscriptions = []integrationmodel.IntegrationWebhookSubscription{subscription}
	var auditEvent string
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, before, after map[string]any, _ map[string]any) {
			auditEvent = event
			if before["key"] != subscription.Key || after != nil {
				t.Fatalf("delete audit before=%#v after=%#v", before, after)
			}
		},
	})
	if err := service.DeleteIntegrationWebhookSubscription(t.Context(), subscription.Key, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace guard=%v", err)
	}
	if err := service.DeleteIntegrationWebhookSubscription(t.Context(), subscription.Key, integrationRotationPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission guard=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.DeleteIntegrationWebhookSubscription(cancelled, subscription.Key, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled delete=%v", err)
	}
	repository.subscriptionErr = errors.New("subscription list failed")
	if err := service.DeleteIntegrationWebhookSubscription(t.Context(), subscription.Key, principal); !errors.Is(err, repository.subscriptionErr) {
		t.Fatalf("list error=%v", err)
	}
	repository.subscriptionErr = nil
	if err := service.DeleteIntegrationWebhookSubscription(t.Context(), "missing", principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("missing=%v", err)
	}
	repository.deleteErr = errors.New("delete failed")
	if err := service.DeleteIntegrationWebhookSubscription(t.Context(), subscription.Key, principal); !errors.Is(err, repository.deleteErr) {
		t.Fatalf("delete error=%v", err)
	}
	repository.deleteErr = nil
	if err := service.DeleteIntegrationWebhookSubscription(t.Context(), subscription.Key, principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("backend rejected delete=%v", err)
	}
	nonDeleteRepository := &independentConfigRepository{subscriptions: []integrationmodel.IntegrationWebhookSubscription{subscription}}
	nonDeleteService := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: nonDeleteRepository})
	if err := nonDeleteService.DeleteIntegrationWebhookSubscription(t.Context(), subscription.Key, principal); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("missing delete repository=%v", err)
	}
	ctx, cancelDuringDelete := context.WithCancel(t.Context())
	repository.cancelDelete = cancelDuringDelete
	if err := service.DeleteIntegrationWebhookSubscription(ctx, subscription.Key, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete cancellation=%v", err)
	}
	repository.deleteResult = true
	if err := service.DeleteIntegrationWebhookSubscription(t.Context(), subscription.Key, principal); err != nil {
		t.Fatalf("delete=%v", err)
	}
	if len(repository.subscriptions) != 0 || auditEvent != "integration_webhook_subscription_deleted" {
		t.Fatalf("subscriptions=%#v audit=%q", repository.subscriptions, auditEvent)
	}
}

func TestPublishIntegrationWebhookEventFailureAndPayloadEdges(t *testing.T) {
	publisher := integrationRotationPrincipal(PermissionInvoke)
	publisher.RequestID = "request"
	repository := newIntegrationWebhookLifecycleRepository()
	repository.subscriptions = []integrationmodel.IntegrationWebhookSubscription{{Key: "subscription", ConnectorKey: "webhook", ConnectionKey: "connection", Status: "active"}}
	delivery := &integrationWebhookDeliveryRepository{}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery})
	if _, err := service.PublishIntegrationWebhookEvent(t.Context(), integrationmodel.IntegrationWebhookPublishRequest{EventType: "created"}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("publish workspace=%v", err)
	}
	if _, err := service.PublishIntegrationWebhookEvent(t.Context(), integrationmodel.IntegrationWebhookPublishRequest{EventType: "created"}, integrationRotationPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("publish permission=%v", err)
	}
	if _, err := service.PublishIntegrationWebhookEvent(t.Context(), integrationmodel.IntegrationWebhookPublishRequest{}, publisher); apperror.CodeOf(err) != "backend.integration.webhook_subscription.missing_event_type" {
		t.Fatalf("publish event type=%v", err)
	}
	repository.subscriptionErr = errors.New("subscription list failed")
	if _, err := service.PublishIntegrationWebhookEvent(t.Context(), integrationmodel.IntegrationWebhookPublishRequest{EventType: "created"}, publisher); !errors.Is(err, repository.subscriptionErr) {
		t.Fatalf("publish list error=%v", err)
	}
	repository.subscriptionErr = nil
	delivery.insertErr = errors.New("outbox write failed")
	if _, err := service.PublishIntegrationWebhookEvent(t.Context(), integrationmodel.IntegrationWebhookPublishRequest{EventType: "created"}, publisher); !errors.Is(err, delivery.insertErr) {
		t.Fatalf("publish outbox error=%v", err)
	}
	delivery.insertErr = nil
	request := integrationmodel.IntegrationWebhookPublishRequest{EventType: " created ", ObjectKey: " object ", RecordID: " record ", WorkflowExecutionID: " workflow ", Payload: map[string]any{"password": "secret"}}
	result, err := service.PublishIntegrationWebhookEvent(t.Context(), request, publisher)
	if err != nil || result.Enqueued != 1 || len(delivery.inserted) != 1 {
		t.Fatalf("result=%#v inserted=%#v err=%v", result, delivery.inserted, err)
	}
	payload, ok := delivery.inserted[0].Payload["payload"].(map[string]any)
	if !ok {
		t.Fatalf("operation payload=%#v", delivery.inserted[0].Payload)
	}
	if payload["password"] != "[REDACTED]" || payload["request_id"] != "request" || payload["object_key"] != "object" || payload["record_id"] != "record" || payload["workflow_execution_id"] != "workflow" {
		t.Fatalf("payload=%#v", payload)
	}
}
