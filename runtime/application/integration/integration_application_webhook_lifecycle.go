package integration

import (
	"context"
	"fmt"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"sort"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
)

func (s *IntegrationApplicationService) UpsertIntegrationWebhookSubscription(ctx context.Context, subscriptionKey string, req integrationmodel.IntegrationWebhookSubscriptionUpsertRequest, principal principalmodel.Principal) (integrationmodel.IntegrationWebhookSubscription, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	if !HasPermission(principal, PermissionConnectionManage) {
		return integrationmodel.IntegrationWebhookSubscription{}, forbidden("auth.permission_denied")
	}
	key := strings.TrimSpace(subscriptionKey)
	if key == "" {
		key = strings.TrimSpace(req.Key)
	}
	if key == "" {
		key = sanitizeKey(valueOrDefault(req.Name, "webhook_subscription") + "_" + fmt.Sprint(time.Now().UTC().UnixNano()))
	}
	connectorKey, connectionKey := strings.TrimSpace(req.ConnectorKey), strings.TrimSpace(req.ConnectionKey)
	if connectorKey == "" || connectionKey == "" {
		return integrationmodel.IntegrationWebhookSubscription{}, badRequest("backend.integration.webhook_subscription.missing_connection")
	}
	if !s.connectorExists(connectorKey) {
		return integrationmodel.IntegrationWebhookSubscription{}, notFound("backend.integration.connector.not_found")
	}
	workspaceID := principalWorkspaceID(principal)
	connection, exists, err := s.findConnection(ctx, connectionKey, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	if !exists {
		return integrationmodel.IntegrationWebhookSubscription{}, notFound("backend.integration.connection.not_found")
	}
	if connection.ConnectorKey != connectorKey {
		return integrationmodel.IntegrationWebhookSubscription{}, badRequest("backend.integration.webhook_subscription.connector_mismatch")
	}
	if connection.Status == "disabled" {
		return integrationmodel.IntegrationWebhookSubscription{}, badRequest("backend.integration.connection.disabled")
	}
	eventTypes, err := normalizeWebhookEventTypes(req.EventTypes)
	if err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	status, err := normalizeWebhookSubscriptionStatus(req.Status)
	if err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	existing, existed, err := s.findWebhookSubscription(ctx, key, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	subscription := integrationmodel.IntegrationWebhookSubscription{
		Key: key, WorkspaceID: workspaceID, Name: strings.TrimSpace(req.Name), ConnectorKey: connection.ConnectorKey,
		ConnectionKey: connection.Key, EventTypes: eventTypes, Status: status,
		Description: strings.TrimSpace(req.Description), CreatedBy: strings.TrimSpace(principal.UserID),
	}
	saved, err := s.configRepo.UpsertWebhookSubscription(ctx, subscription.WorkspaceID, subscription)
	if err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	var before map[string]any
	if existed {
		before = webhookSubscriptionAuditShape(existing)
	}
	s.audit(ctx, "integration_webhook_subscription_upserted", "integration_webhook_subscription", saved.Key, principal, "Upserted integration webhook subscription "+saved.Key, before, webhookSubscriptionAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "connector_key": saved.ConnectorKey, "connection_key": saved.ConnectionKey,
		"subscription_key": saved.Key, "event_types": saved.EventTypes, "status": saved.Status,
	})
	return saved, nil
}

func normalizeWebhookEventTypes(eventTypes []string) ([]string, error) {
	if len(eventTypes) == 0 {
		return []string{"*"}, nil
	}
	seen, out := map[string]bool{}, []string{}
	for _, eventType := range eventTypes {
		eventType = strings.TrimSpace(eventType)
		if eventType == "" {
			return nil, badRequest("backend.integration.webhook_subscription.invalid_event_type")
		}
		if !seen[eventType] {
			seen[eventType] = true
			out = append(out, eventType)
		}
	}
	sort.Strings(out)
	return out, nil
}

func normalizeWebhookSubscriptionStatus(value string) (string, error) {
	switch value = strings.TrimSpace(value); value {
	case "":
		return "active", nil
	case "active", "disabled":
		return value, nil
	default:
		return "", badRequest("backend.integration.webhook_subscription.invalid_status")
	}
}

func (s *IntegrationApplicationService) PublishIntegrationWebhookEvent(ctx context.Context, req integrationmodel.IntegrationWebhookPublishRequest, principal principalmodel.Principal) (integrationmodel.IntegrationWebhookPublishResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationWebhookPublishResult{}, err
	}
	if !HasPermission(principal, PermissionInvoke) {
		return integrationmodel.IntegrationWebhookPublishResult{}, forbidden("auth.permission_denied")
	}
	eventType := strings.TrimSpace(req.EventType)
	if eventType == "" {
		return integrationmodel.IntegrationWebhookPublishResult{}, badRequest("backend.integration.webhook_subscription.missing_event_type")
	}
	workspaceID := principalWorkspaceID(principal)
	subscriptions, err := s.configRepo.ListWebhookSubscriptions(ctx, workspaceID, "", eventType, "active", 500)
	if err != nil {
		return integrationmodel.IntegrationWebhookPublishResult{}, err
	}
	result := integrationmodel.IntegrationWebhookPublishResult{EventType: eventType, Subscriptions: []integrationmodel.IntegrationWebhookSubscription{}, Messages: []integrationmodel.IntegrationOutboxMessage{}}
	for _, subscription := range subscriptions {
		eventPayload := webhookSubscriptionPayload(req, subscription)
		if principal.RequestID != "" {
			eventPayload["request_id"] = principal.RequestID
		}
		// The webhook Connector's send operation owns a single required JSON
		// input named "payload". Keep the subscription event envelope inside
		// that field so persisted outbox messages satisfy the same catalog
		// contract as every other Connector invocation.
		operationInput := map[string]any{"payload": eventPayload}
		message := integrationmodel.IntegrationOutboxMessage{
			WorkspaceID: workspaceID, ConnectorKey: subscription.ConnectorKey, ConnectionKey: subscription.ConnectionKey,
			Operation: "webhook.deliver." + eventType, Status: "queued", Payload: integrationpolicy.RedactSensitiveMap(operationInput),
			RequestRef: valueOrDefault(strings.TrimSpace(req.RequestRef), "webhook_subscription:"+subscription.Key+":"+eventType),
			CreatedBy:  principal.UserID,
		}
		saved, err := s.publicationRepo.InsertOutbox(ctx, message.WorkspaceID, message)
		if err != nil {
			return integrationmodel.IntegrationWebhookPublishResult{}, err
		}
		result.Subscriptions = append(result.Subscriptions, subscription)
		result.Messages = append(result.Messages, saved)
		result.Enqueued = len(result.Messages)
		s.audit(ctx, "integration_webhook_subscription_enqueued", "integration_webhook_subscription", subscription.Key, principal, "Enqueued outbound webhook subscription "+subscription.Key, nil, integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{
			"workspace_id": workspaceID, "connector_key": subscription.ConnectorKey, "connection_key": subscription.ConnectionKey,
			"subscription_key": subscription.Key, "event_type": eventType, "outbox_id": saved.ID,
		})
		s.wakeIntegrationOutbox(saved)
	}
	return result, nil
}

func webhookSubscriptionPayload(req integrationmodel.IntegrationWebhookPublishRequest, subscription integrationmodel.IntegrationWebhookSubscription) map[string]any {
	payload := cloneMap(req.Payload)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["event_type"] = strings.TrimSpace(req.EventType)
	payload["subscription_key"] = strings.TrimSpace(subscription.Key)
	payload["connector_key"] = strings.TrimSpace(subscription.ConnectorKey)
	payload["connection_key"] = strings.TrimSpace(subscription.ConnectionKey)
	if value := strings.TrimSpace(req.ObjectKey); value != "" {
		payload["object_key"] = value
	}
	if value := strings.TrimSpace(req.RecordID); value != "" {
		payload["record_id"] = value
	}
	if value := strings.TrimSpace(req.WorkflowExecutionID); value != "" {
		payload["workflow_execution_id"] = value
	}
	return payload
}

func (s *IntegrationApplicationService) DisableIntegrationWebhookSubscription(ctx context.Context, subscriptionKey string, principal principalmodel.Principal) (integrationmodel.IntegrationWebhookSubscription, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	if !HasPermission(principal, PermissionConnectionManage) {
		return integrationmodel.IntegrationWebhookSubscription{}, forbidden("auth.permission_denied")
	}
	workspaceID := principalWorkspaceID(principal)
	subscription, ok, err := s.findWebhookSubscription(ctx, subscriptionKey, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	if !ok {
		return integrationmodel.IntegrationWebhookSubscription{}, notFound("backend.integration.webhook_subscription.not_found")
	}
	before := webhookSubscriptionAuditShape(subscription)
	subscription.Status, subscription.DisabledAt = "disabled", time.Now().UTC().Format(time.RFC3339)
	saved, err := s.configRepo.UpsertWebhookSubscription(ctx, subscription.WorkspaceID, subscription)
	if err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	s.audit(ctx, "integration_webhook_subscription_disabled", "integration_webhook_subscription", saved.Key, principal, "Disabled integration webhook subscription "+saved.Key, before, webhookSubscriptionAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "connector_key": saved.ConnectorKey, "connection_key": saved.ConnectionKey, "subscription_key": saved.Key,
	})
	return saved, nil
}

func (s *IntegrationApplicationService) DeleteIntegrationWebhookSubscription(ctx context.Context, subscriptionKey string, principal principalmodel.Principal) error {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !HasPermission(principal, PermissionConnectionManage) {
		return forbidden("auth.permission_denied")
	}
	workspaceID, key := principalWorkspaceID(principal), strings.TrimSpace(subscriptionKey)
	subscription, ok, err := s.findWebhookSubscription(ctx, key, workspaceID)
	if err != nil {
		return err
	}
	if !ok {
		return notFound("backend.integration.webhook_subscription.not_found")
	}
	repository, ok := s.configRepo.(integrationrepository.IntegrationWebhookSubscriptionDeleteRepository)
	if !ok {
		return internalError("delete integration webhook subscription", fmt.Errorf("integration webhook subscription delete repository is not configured"))
	}
	deleted, err := repository.DeleteWebhookSubscription(ctx, workspaceID, key)
	if err != nil {
		return err
	}
	if !deleted {
		if err := ctx.Err(); err != nil {
			return err
		}
		return notFound("backend.integration.webhook_subscription.not_found")
	}
	s.audit(ctx, "integration_webhook_subscription_deleted", "integration_webhook_subscription", key, principal, "Deleted integration webhook subscription "+key, webhookSubscriptionAuditShape(subscription), nil, map[string]any{
		"workspace_id": workspaceID, "connector_key": subscription.ConnectorKey, "connection_key": subscription.ConnectionKey, "subscription_key": key,
	})
	return nil
}

func (s *IntegrationApplicationService) findWebhookSubscription(ctx context.Context, key, workspaceID string) (integrationmodel.IntegrationWebhookSubscription, bool, error) {
	subscriptions, err := s.configRepo.ListWebhookSubscriptions(ctx, workspaceID, "", "", "", 500)
	if err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, false, err
	}
	key = strings.TrimSpace(key)
	for _, subscription := range subscriptions {
		if subscription.Key == key {
			return subscription, true, nil
		}
	}
	return integrationmodel.IntegrationWebhookSubscription{}, false, nil
}

func webhookSubscriptionAuditShape(subscription integrationmodel.IntegrationWebhookSubscription) map[string]any {
	eventTypes := subscription.EventTypes
	if eventTypes == nil {
		eventTypes = []string{}
	}
	return map[string]any{
		"key": subscription.Key, "workspace_id": subscription.WorkspaceID, "name": subscription.Name,
		"connector_key": subscription.ConnectorKey, "connection_key": subscription.ConnectionKey, "event_types": eventTypes,
		"status": subscription.Status, "description": subscription.Description, "created_by": subscription.CreatedBy,
		"created_at": subscription.CreatedAt, "updated_at": subscription.UpdatedAt, "disabled_at": subscription.DisabledAt,
	}
}
