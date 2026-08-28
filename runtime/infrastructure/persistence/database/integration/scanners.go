package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"strings"
	"time"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type integrationConnectionScanner interface {
	Scan(dest ...any) error
}

func scanIntegrationSecret(row integrationConnectionScanner) (integrationmodel.IntegrationSecret, error) {
	var secret integrationmodel.IntegrationSecret
	if err := row.Scan(&secret.Key, &secret.WorkspaceID, &secret.Kind, &secret.Status, &secret.Description, &secret.ValueRef, &secret.Fingerprint, &secret.CreatedBy, &secret.CreatedAt, &secret.UpdatedAt, &secret.DisabledAt, &secret.ExpiresAt, &secret.RotatedAt, &secret.RevokedAt, &secret.LastTestedAt, &secret.LastTestStatus, &secret.LastTestError); err != nil {
		return integrationmodel.IntegrationSecret{}, fmt.Errorf("scan integration secret: %w", err)
	}
	return secret, nil
}

func scanIntegrationConnection(row integrationConnectionScanner) (integrationmodel.IntegrationConnection, error) {
	var connection integrationmodel.IntegrationConnection
	var configJSON string
	var secretRefsJSON string
	if err := row.Scan(&connection.Key, &connection.WorkspaceID, &connection.ConnectorKey, &connection.ProviderKey, &connection.Name, &connection.Status, &configJSON, &secretRefsJSON, &connection.CreatedBy, &connection.CreatedAt, &connection.UpdatedAt); err != nil {
		return integrationmodel.IntegrationConnection{}, fmt.Errorf("scan integration connection: %w", err)
	}
	_ = json.Unmarshal([]byte(configJSON), &connection.Config)
	_ = json.Unmarshal([]byte(secretRefsJSON), &connection.SecretRefs)
	connection.Config = nonNilMap(connection.Config)
	connection.SecretRefs = nonNilStringMap(connection.SecretRefs)
	return connection, nil
}

func scanIntegrationExternalIdentity(row integrationConnectionScanner) (integrationmodel.IntegrationExternalIdentity, error) {
	var identity integrationmodel.IntegrationExternalIdentity
	if err := row.Scan(&identity.Key, &identity.WorkspaceID, &identity.Provider, &identity.ExternalSubject, &identity.ExternalSubjectType, &identity.ExternalName, &identity.ExternalOrganization, &identity.ExternalDepartment, &identity.ExternalGroup, &identity.ExternalBotID, &identity.ActorID, &identity.RoleKey, &identity.Status, &identity.LastResolvedAt, &identity.CreatedBy, &identity.CreatedAt, &identity.UpdatedAt, &identity.DisabledAt); err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, fmt.Errorf("scan integration external identity: %w", err)
	}
	return identity, nil
}

func scanIntegrationEvent(row integrationConnectionScanner) (integrationmodel.IntegrationEvent, error) {
	var event integrationmodel.IntegrationEvent
	var payloadJSON string
	if err := row.Scan(&event.ID, &event.WorkspaceID, &event.Provider, &event.EventType, &event.ExternalID, &event.Status, &payloadJSON, &event.Error, &event.AttemptCount, &event.NextRetryAt, &event.LastAttemptAt, &event.LeaseOwner, &event.LeaseExpiresAt, &event.FencingToken, &event.ReceivedAt, &event.UpdatedAt); err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	_ = json.Unmarshal([]byte(payloadJSON), &event.Payload)
	event.Payload = nonNilMap(event.Payload)
	return event, nil
}

func scanIntegrationInvocation(row integrationConnectionScanner) (integrationmodel.IntegrationInvocation, error) {
	var invocation integrationmodel.IntegrationInvocation
	var metadataJSON string
	if err := row.Scan(&invocation.ID, &invocation.WorkspaceID, &invocation.ConnectorKey, &invocation.ProviderKey, &invocation.ConnectionKey, &invocation.Operation, &invocation.Status, &invocation.DurationMS, &invocation.RequestRef, &invocation.ResponseRef, &invocation.Error, &invocation.EventID, &invocation.ObjectKey, &invocation.RecordID, &invocation.WorkflowExecutionID, &metadataJSON, &invocation.CreatedAt, &invocation.UpdatedAt); err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	_ = json.Unmarshal([]byte(metadataJSON), &invocation.Metadata)
	invocation.Metadata = nonNilMap(invocation.Metadata)
	return invocation, nil
}

func scanIntegrationOutboxMessage(row integrationConnectionScanner) (integrationmodel.IntegrationOutboxMessage, error) {
	var message integrationmodel.IntegrationOutboxMessage
	var payloadJSON string
	if err := row.Scan(&message.ID, &message.WorkspaceID, &message.ConnectorKey, &message.ConnectionKey, &message.Operation, &message.Status, &payloadJSON, &message.EventID, &message.RequestRef, &message.DedupKey, &message.RequestFingerprint, &message.ResponseRef, &message.Error, &message.AttemptCount, &message.NextAttemptAt, &message.AckDeadlineAt, &message.LastAttemptAt, &message.LeaseOwner, &message.LeaseExpiresAt, &message.FencingToken, &message.CreatedBy, &message.CreatedAt, &message.UpdatedAt); err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	_ = json.Unmarshal([]byte(payloadJSON), &message.Payload)
	message.Payload = nonNilMap(message.Payload)
	return message, nil
}

func scanIntegrationWebhookSubscription(row integrationConnectionScanner) (integrationmodel.IntegrationWebhookSubscription, error) {
	var subscription integrationmodel.IntegrationWebhookSubscription
	var eventTypesJSON string
	if err := row.Scan(&subscription.Key, &subscription.WorkspaceID, &subscription.Name, &subscription.ConnectorKey, &subscription.ConnectionKey, &eventTypesJSON, &subscription.Status, &subscription.Description, &subscription.CreatedBy, &subscription.CreatedAt, &subscription.UpdatedAt, &subscription.DisabledAt); err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	_ = json.Unmarshal([]byte(eventTypesJSON), &subscription.EventTypes)
	subscription.EventTypes = nonNilStringSlice(subscription.EventTypes)
	return subscription, nil
}

func scanIntegrationAPIKey(row integrationConnectionScanner) (integrationmodel.IntegrationAPIKey, error) {
	var apiKey integrationmodel.IntegrationAPIKey
	var scopesJSON string
	if err := row.Scan(&apiKey.Key, &apiKey.WorkspaceID, &apiKey.Name, &apiKey.TokenPrefix, &apiKey.TokenHash, &apiKey.ActorID, &apiKey.RoleKey, &scopesJSON, &apiKey.Status, &apiKey.ExpiresAt, &apiKey.LastUsedAt, &apiKey.CreatedBy, &apiKey.CreatedAt, &apiKey.UpdatedAt, &apiKey.DisabledAt); err != nil {
		return integrationmodel.IntegrationAPIKey{}, err
	}
	_ = json.Unmarshal([]byte(scopesJSON), &apiKey.Scopes)
	apiKey.Scopes = nonNilStringSlice(apiKey.Scopes)
	return apiKey, nil
}

func integrationWebhookSubscriptionMatchesEvent(subscription integrationmodel.IntegrationWebhookSubscription, eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return true
	}
	for _, candidate := range subscription.EventTypes {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == eventType {
			return true
		}
		if strings.HasSuffix(candidate, ".*") && strings.HasPrefix(eventType, strings.TrimSuffix(candidate, ".*")+".") {
			return true
		}
	}
	return false
}

func integrationWorkspaceID(value string) string {
	return strings.TrimSpace(value)
}

func requireIntegrationWorkspaceID(value string) (string, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(value)
	if err != nil {
		return "", fmt.Errorf("integration workspace: %w", err)
	}
	return workspaceID.String(), nil
}

func requireIntegrationMutationWorkspaceID(workspaceID, embeddedWorkspaceID string) (string, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return "", err
	}
	embeddedWorkspaceID = strings.TrimSpace(embeddedWorkspaceID)
	if len(embeddedWorkspaceID) > 0 && strings.Compare(embeddedWorkspaceID, workspaceID) != 0 {
		return "", fmt.Errorf("integration workspace mismatch: explicit %q does not match payload %q", workspaceID, embeddedWorkspaceID)
	}
	return workspaceID, nil
}

func integrationEventID(workspaceID string, provider string, externalID string) string {
	return "integration_event:" + integrationWorkspaceID(workspaceID) + ":" + strings.TrimSpace(provider) + ":" + strings.TrimSpace(externalID)
}

func integrationInvocationID(workspaceID string, connectorKey string, operation string) string {
	return "integration_invocation:" + integrationWorkspaceID(workspaceID) + ":" + strings.TrimSpace(connectorKey) + ":" + strings.TrimSpace(operation) + ":" + fmt.Sprint(time.Now().UTC().UnixNano())
}

func integrationOutboxID(workspaceID string, connectorKey string, operation string) string {
	return "integration_outbox:" + integrationWorkspaceID(workspaceID) + ":" + strings.TrimSpace(connectorKey) + ":" + strings.TrimSpace(operation) + ":" + fmt.Sprint(time.Now().UTC().UnixNano())
}

func WorkspaceID(value string) string { return integrationWorkspaceID(value) }
func OutboxID(workspaceID, connectorKey, operation string) string {
	return integrationOutboxID(workspaceID, connectorKey, operation)
}

func OutboxDedupID(workspaceID, connectorKey, connectionKey, operation, dedupKey string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{integrationWorkspaceID(workspaceID), strings.TrimSpace(connectorKey), strings.TrimSpace(connectionKey), strings.TrimSpace(operation), strings.TrimSpace(dedupKey)}, "\x00")))
	return "integration_outbox:" + hex.EncodeToString(sum[:])[:24]
}

func integrationInvocationColumnsSQL(s *database.RuntimeStore) string {
	return stringsJoinIdentifiers(s,
		"id", "workspace_id", "connector_key", "provider_key", "connection_key", "operation", "status", "duration_ms", "request_ref", "response_ref", "error", "event_id", "object_key", "record_id", "workflow_execution_id", "metadata_json", "created_at", "updated_at",
	)
}

func integrationOutboxColumnsSQL(s *database.RuntimeStore) string {
	return stringsJoinIdentifiers(s,
		"id", "workspace_id", "connector_key", "connection_key", "operation", "status", "payload_json", "event_id", "request_ref", "dedup_key", "request_fingerprint", "response_ref", "error", "attempt_count", "next_attempt_at", "ack_deadline_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "created_by", "created_at", "updated_at",
	)
}

func integrationWebhookSubscriptionColumnsSQL(s *database.RuntimeStore) string {
	return stringsJoinIdentifiers(s,
		"subscription_key", "workspace_id", "name", "connector_key", "connection_key", "event_types_json", "status", "description", "created_by", "created_at", "updated_at", "disabled_at",
	)
}

func integrationAPIKeyColumnsSQL(s *database.RuntimeStore) string {
	return stringsJoinIdentifiers(s,
		"api_key", "workspace_id", "name", "token_prefix", "token_hash", "actor_id", "role_key", "scopes_json", "status", "expires_at", "last_used_at", "created_by", "created_at", "updated_at", "disabled_at",
	)
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func nonNilStringMap(value map[string]string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	return value
}

func nonNilStringSlice(value []string) []string {
	if value == nil {
		return []string{}
	}
	return value
}
