package repository

import (
	"context"
	"encoding/json"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ConnectorProviderTask struct {
	Key          string
	StateVersion int
	InitialState json.RawMessage
}

type ConnectorProviderStateRepository interface {
	MigrateLegacyConnectorProviderStates(context.Context, principalmodel.SystemScope) error
	ListConnectorProviderConnections(context.Context, principalmodel.SystemScope) ([]integrationmodel.IntegrationConnection, error)
	SyncConnectorProviderTasks(context.Context, integrationmodel.IntegrationConnection, []ConnectorProviderTask, string) error
	ListDueConnectorProviderStates(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.ConnectorProviderStateCandidate, error)
	ClaimConnectorProviderState(context.Context, string, string, string, string, string, string) (integrationmodel.ConnectorProviderState, bool, error)
	CompleteConnectorProviderState(context.Context, integrationmodel.ConnectorProviderState, json.RawMessage, string, string) (integrationmodel.ConnectorProviderState, error)
	FailConnectorProviderState(context.Context, integrationmodel.ConnectorProviderState, string, string, string) (integrationmodel.ConnectorProviderState, error)
	WakeConnectorProviderState(context.Context, string, string, string, string) error
	DeleteConnectorProviderStates(context.Context, string, string) error
}

// IntegrationConfigRepository owns connector configuration, credentials, API keys and
// external identity mappings. Event and delivery persistence use separate contracts
// so integration workers do not depend on the Record aggregate.
type IntegrationConfigRepository interface {
	ListSecrets(context.Context, string) ([]integrationmodel.IntegrationSecret, error)
	UpsertSecret(context.Context, string, integrationmodel.IntegrationSecret) (integrationmodel.IntegrationSecret, error)
	ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error)
	UpsertConnection(context.Context, string, integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error)
	ListExternalIdentities(context.Context, string) ([]integrationmodel.IntegrationExternalIdentity, error)
	UpsertExternalIdentity(context.Context, string, integrationmodel.IntegrationExternalIdentity) (integrationmodel.IntegrationExternalIdentity, error)
	ListWebhookSubscriptions(context.Context, string, string, string, string, int) ([]integrationmodel.IntegrationWebhookSubscription, error)
	UpsertWebhookSubscription(context.Context, string, integrationmodel.IntegrationWebhookSubscription) (integrationmodel.IntegrationWebhookSubscription, error)
	ListAPIKeys(context.Context, string) ([]integrationmodel.IntegrationAPIKey, error)
	UpsertAPIKey(context.Context, string, integrationmodel.IntegrationAPIKey) (integrationmodel.IntegrationAPIKey, error)
	FindAPIKeyByTokenHash(context.Context, string, string) (integrationmodel.IntegrationAPIKey, bool, error)
	UpdateAPIKeyLastUsed(context.Context, string, string, string) (integrationmodel.IntegrationAPIKey, error)
}

// IntegrationConnectionRepository is the connection aggregate persistence
// contract required by manifest installation.
type IntegrationConnectionRepository interface {
	ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error)
	UpsertConnection(context.Context, string, integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error)
}

type IntegrationSecretMaterialRepository interface {
	PutSecretMaterial(context.Context, string, string, string) error
	ResolveSecretMaterial(context.Context, string, string) (string, error)
}

type IntegrationCredentialLeaseRepository interface {
	TryAcquireCredentialRefreshLease(ctx context.Context, workspaceID, connectionKey, owner, now, expiresAt string) (bool, error)
	ReleaseCredentialRefreshLease(ctx context.Context, workspaceID, connectionKey, owner string) error
}

type IntegrationCredentialLeaseRepositoryProvider interface {
	CredentialLeaseRepository() IntegrationCredentialLeaseRepository
}

type IntegrationConnectionDeleteRepository interface {
	DeleteConnection(ctx context.Context, workspaceID, connectionKey string) (bool, error)
}

type IntegrationWebhookSubscriptionDeleteRepository interface {
	DeleteWebhookSubscription(ctx context.Context, workspaceID, subscriptionKey string) (bool, error)
}

type IntegrationEventRepository interface {
	ListEvents(context.Context, string, string, string, int) ([]integrationmodel.IntegrationEvent, error)
	GetEvent(context.Context, string, string) (integrationmodel.IntegrationEvent, bool, error)
	UpsertEvent(context.Context, string, integrationmodel.IntegrationEvent) (integrationmodel.IntegrationEvent, bool, error)
	AcceptEvent(context.Context, string, integrationmodel.IntegrationEvent, integrationmodel.IntegrationEventMappingIntent) (integrationmodel.IntegrationEvent, bool, error)
	UpdateEventStatus(context.Context, string, string, string, string) (integrationmodel.IntegrationEvent, error)
	ScheduleEventRetry(context.Context, string, string, int, string) (integrationmodel.IntegrationEvent, error)
	RecordWebhookNonce(context.Context, string, string, string, string, string) (bool, error)
}

// RuntimePublicationRepository is the Runtime-owned durable handoff ledger.
// It intentionally contains no Provider invocation evidence.
type RuntimePublicationRepository interface {
	ListOutbox(context.Context, string, string, string, int) ([]integrationmodel.IntegrationOutboxMessage, error)
	InsertOutbox(context.Context, string, integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error)
	UpdateOutboxStatus(context.Context, string, string, string, string, string) (integrationmodel.IntegrationOutboxMessage, error)
	UpdateOutboxStatusByResponseRef(context.Context, string, string, string, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error)
	ScheduleOutboxRetry(context.Context, string, string, int, string) (integrationmodel.IntegrationOutboxMessage, error)
}

// IntegrationInvocationRepository is owner-side execution evidence. Runtime
// composition must not provide a local implementation; it remains temporarily
// separate for compatibility while callers move to the Integration Binding.
type IntegrationInvocationRepository interface {
	ListInvocations(context.Context, string, string, string, string, string, int) ([]integrationmodel.IntegrationInvocation, error)
	InsertInvocation(context.Context, string, integrationmodel.IntegrationInvocation) (integrationmodel.IntegrationInvocation, error)
	UpdateInvocationStatus(context.Context, string, string, string, int64, string, string) (integrationmodel.IntegrationInvocation, error)
}

// IntegrationDeliveryRepository is the legacy aggregate contract. New Runtime
// code must depend on one of the narrow owner-specific interfaces above.
type IntegrationDeliveryRepository interface {
	RuntimePublicationRepository
	IntegrationInvocationRepository
}

type IntegrationOutboxReader interface {
	GetOutbox(context.Context, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error)
}

// IntegrationInvocationOutcomeRepository finalizes a durable prepared
// invocation with the provider outcome and reconciliation metadata.
type IntegrationInvocationOutcomeRepository interface {
	CompleteInvocation(context.Context, string, string, string, int64, string, string, map[string]any) (integrationmodel.IntegrationInvocation, error)
}

// IntegrationInvocationReconciliationRepository finds durable invocation facts
// whose external outcome was never persisted, then claims them exactly once for
// reconciliation. Implementations must use a prepared-state compare-and-swap
// when marking a fact so concurrent reconciliation workers cannot report it
// more than once.
type IntegrationInvocationReconciliationRepository interface {
	ListPreparedInvocationsForReconciliation(ctx context.Context, scope principalmodel.SystemScope, limit int, staleBefore string) ([]integrationmodel.IntegrationInvocation, error)
	MarkInvocationReconciliationRequired(ctx context.Context, workspaceID, invocationID, detectedAt string) (integrationmodel.IntegrationInvocation, bool, error)
}

type IntegrationEventWorkerRepository interface {
	ListDueEvents(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]integrationmodel.IntegrationEvent, error)
	ClaimEvent(ctx context.Context, workspaceID, eventID, owner, now string) (integrationmodel.IntegrationEvent, bool, error)
	HeartbeatEvent(ctx context.Context, workspaceID, eventID, expectedLeaseOwner string, expectedFencingToken int64, now string) (integrationmodel.IntegrationEvent, error)
	UpdateEventStatus(ctx context.Context, workspaceID, eventID, expectedLeaseOwner string, expectedFencingToken int64, status, errorText, now string) (integrationmodel.IntegrationEvent, error)
	ScheduleEventRetry(ctx context.Context, workspaceID, eventID, expectedLeaseOwner string, expectedFencingToken int64, delaySeconds int, errorText, now string) (integrationmodel.IntegrationEvent, error)
}

type RuntimePublicationWorkerRepository interface {
	ListDueOutbox(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]integrationmodel.IntegrationOutboxMessage, error)
	ClaimOutbox(ctx context.Context, workspaceID, messageID, owner, now string) (integrationmodel.IntegrationOutboxMessage, bool, error)
	HeartbeatOutbox(ctx context.Context, workspaceID, messageID, expectedLeaseOwner string, expectedFencingToken int64, now string) (integrationmodel.IntegrationOutboxMessage, error)
	UpdateOutboxStatus(ctx context.Context, workspaceID, messageID, expectedLeaseOwner string, expectedFencingToken int64, status, responseRef, errorText, ackDeadlineAt, now string) (integrationmodel.IntegrationOutboxMessage, error)
	ScheduleOutboxRetry(ctx context.Context, workspaceID, messageID, expectedLeaseOwner string, expectedFencingToken int64, delaySeconds int, errorText, now string) (integrationmodel.IntegrationOutboxMessage, error)
}

type IntegrationWorkerRepository interface {
	IntegrationEventWorkerRepository
	RuntimePublicationWorkerRepository
}

// IntegrationAcknowledgementReconciliationRepository owns acknowledgements
// that were expected after a provider accepted an Outbox command.
type IntegrationAcknowledgementReconciliationRepository interface {
	ListOverdueOutboxAcknowledgements(ctx context.Context, scope principalmodel.SystemScope, limit int, now string) ([]integrationmodel.IntegrationOutboxMessage, error)
	MarkOutboxAcknowledgementReconciliationRequired(ctx context.Context, workspaceID, messageID, detectedAt string) (integrationmodel.IntegrationOutboxMessage, bool, error)
}
