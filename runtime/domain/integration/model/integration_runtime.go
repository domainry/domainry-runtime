package integrationmodel

import "encoding/json"
import agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"

type IntegrationConnection struct {
	Key          string            `json:"key"`
	WorkspaceID  string            `json:"workspace_id,omitempty"`
	ConnectorKey string            `json:"connector_key"`
	ProviderKey  string            `json:"provider_key"`
	Name         string            `json:"name,omitempty"`
	Status       string            `json:"status"`
	Config       map[string]any    `json:"config,omitempty"`
	SecretRefs   map[string]string `json:"secret_refs,omitempty"`
	CreatedBy    string            `json:"created_by,omitempty"`
	CreatedAt    string            `json:"created_at,omitempty"`
	UpdatedAt    string            `json:"updated_at,omitempty"`
}

type IntegrationConnectionUpsertRequest struct {
	Key          string            `json:"key,omitempty"`
	ConnectorKey string            `json:"connector_key"`
	ProviderKey  string            `json:"provider_key"`
	Name         string            `json:"name,omitempty"`
	Status       string            `json:"status,omitempty"`
	Config       map[string]any    `json:"config,omitempty"`
	SecretRefs   map[string]string `json:"secret_refs,omitempty"`
}

// ConnectorProviderState is Runtime-owned durable execution state whose JSON
// payload and state version are owned by the selected Provider.
type ConnectorProviderState struct {
	WorkspaceID    string          `json:"workspace_id"`
	ConnectorKey   string          `json:"connector_key"`
	ProviderKey    string          `json:"provider_key"`
	ConnectionKey  string          `json:"connection_key"`
	TaskKey        string          `json:"task_key"`
	StateVersion   int             `json:"state_version"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
	DueAt          string          `json:"due_at,omitempty"`
	LastErrorCode  string          `json:"last_error_code,omitempty"`
	AttemptCount   int             `json:"attempt_count"`
	LeaseOwner     string          `json:"lease_owner,omitempty"`
	LeaseExpiresAt string          `json:"lease_expires_at,omitempty"`
	FencingToken   int64           `json:"fencing_token"`
	UpdatedAt      string          `json:"updated_at"`
}

type ConnectorProviderStateCandidate struct {
	Connection    IntegrationConnection             `json:"connection"`
	State         ConnectorProviderState            `json:"state"`
	RelatedStates map[string]ConnectorProviderState `json:"related_states,omitempty"`
}

// IntegrationGoogleOAuthStartRequest begins a user-authorized Google Workspace
// connection. Client credentials stay in Integration secrets; callers provide
// only the registered callback URL.
type IntegrationGoogleOAuthStartRequest struct {
	RedirectURI  string `json:"redirect_uri"`
	EventActorID string `json:"event_actor_id,omitempty"`
	EventRoleKey string `json:"event_role_key,omitempty"`
}

type IntegrationGoogleOAuthStartResult struct {
	AuthorizationURL string `json:"authorization_url"`
	ExpiresAt        string `json:"expires_at"`
}

type IntegrationGoogleOAuthCallbackResult struct {
	ConnectionKey string `json:"connection_key"`
	WorkspaceID   string `json:"workspace_id"`
	Status        string `json:"status"`
}

type IntegrationSecret struct {
	Key            string `json:"key"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	Description    string `json:"description,omitempty"`
	ValueRef       string `json:"value_ref,omitempty"`
	Fingerprint    string `json:"fingerprint,omitempty"`
	CreatedBy      string `json:"created_by,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	DisabledAt     string `json:"disabled_at,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	RotatedAt      string `json:"rotated_at,omitempty"`
	RevokedAt      string `json:"revoked_at,omitempty"`
	LastTestedAt   string `json:"last_tested_at,omitempty"`
	LastTestStatus string `json:"last_test_status,omitempty"`
	LastTestError  string `json:"last_test_error,omitempty"`
}

type IntegrationSecretUpsertRequest struct {
	Key         string `json:"key,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Status      string `json:"status,omitempty"`
	Description string `json:"description,omitempty"`
	ValueRef    string `json:"value_ref,omitempty"`
	Value       string `json:"value,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
}

type IntegrationWebhookSignatureVerificationRequest struct {
	ConnectorKey   string `json:"connector_key,omitempty"`
	ConnectionKey  string `json:"connection_key,omitempty"`
	SecretRefName  string `json:"secret_ref_name,omitempty"`
	SecretRef      string `json:"secret_ref,omitempty"`
	Algorithm      string `json:"algorithm"`
	Signature      string `json:"signature"`
	Timestamp      string `json:"timestamp,omitempty"`
	Nonce          string `json:"nonce,omitempty"`
	Body           []byte `json:"-"`
	MaxSkewSeconds int64  `json:"max_skew_seconds,omitempty"`
}

type IntegrationWebhookSignatureVerificationResult struct {
	Valid         bool           `json:"valid"`
	ConnectorKey  string         `json:"connector_key,omitempty"`
	ConnectionKey string         `json:"connection_key,omitempty"`
	SecretRefName string         `json:"secret_ref_name,omitempty"`
	Algorithm     string         `json:"algorithm"`
	Strategy      map[string]any `json:"strategy,omitempty"`
}

type IntegrationExternalIdentity struct {
	Key                  string `json:"key"`
	WorkspaceID          string `json:"workspace_id,omitempty"`
	Provider             string `json:"provider"`
	ExternalSubject      string `json:"external_subject"`
	ExternalSubjectType  string `json:"external_subject_type,omitempty"`
	ExternalName         string `json:"external_name,omitempty"`
	ExternalOrganization string `json:"external_organization,omitempty"`
	ExternalDepartment   string `json:"external_department,omitempty"`
	ExternalGroup        string `json:"external_group,omitempty"`
	ExternalBotID        string `json:"external_bot_id,omitempty"`
	ActorID              string `json:"actor_id"`
	RoleKey              string `json:"role_key"`
	Status               string `json:"status"`
	LastResolvedAt       string `json:"last_resolved_at,omitempty"`
	CreatedBy            string `json:"created_by,omitempty"`
	CreatedAt            string `json:"created_at,omitempty"`
	UpdatedAt            string `json:"updated_at,omitempty"`
	DisabledAt           string `json:"disabled_at,omitempty"`
}

type IntegrationExternalIdentityUpsertRequest struct {
	Key                  string `json:"key,omitempty"`
	Provider             string `json:"provider"`
	ExternalSubject      string `json:"external_subject"`
	ExternalSubjectType  string `json:"external_subject_type,omitempty"`
	ExternalName         string `json:"external_name,omitempty"`
	ExternalOrganization string `json:"external_organization,omitempty"`
	ExternalDepartment   string `json:"external_department,omitempty"`
	ExternalGroup        string `json:"external_group,omitempty"`
	ExternalBotID        string `json:"external_bot_id,omitempty"`
	ActorID              string `json:"actor_id"`
	RoleKey              string `json:"role_key"`
	Status               string `json:"status,omitempty"`
}

type IntegrationExternalIdentityResolveRequest struct {
	Provider             string `json:"provider"`
	ExternalSubject      string `json:"external_subject"`
	ExternalSubjectType  string `json:"external_subject_type,omitempty"`
	ExternalName         string `json:"external_name,omitempty"`
	ExternalOrganization string `json:"external_organization,omitempty"`
	ExternalDepartment   string `json:"external_department,omitempty"`
	ExternalGroup        string `json:"external_group,omitempty"`
	ExternalBotID        string `json:"external_bot_id,omitempty"`
	OnUnmapped           string `json:"on_unmapped,omitempty"`
}

type IntegrationExternalIdentityResolveResult struct {
	Mapped               bool   `json:"mapped"`
	MappingKey           string `json:"mapping_key,omitempty"`
	Provider             string `json:"provider"`
	ExternalSubject      string `json:"external_subject"`
	ExternalSubjectType  string `json:"external_subject_type,omitempty"`
	ExternalName         string `json:"external_name,omitempty"`
	ExternalOrganization string `json:"external_organization,omitempty"`
	ExternalDepartment   string `json:"external_department,omitempty"`
	ExternalGroup        string `json:"external_group,omitempty"`
	ExternalBotID        string `json:"external_bot_id,omitempty"`
	ActorID              string `json:"actor_id,omitempty"`
	RoleKey              string `json:"role_key,omitempty"`
	ExternalPrincipal    string `json:"external_principal"`
	Status               string `json:"status"`
}

type IntegrationEvent struct {
	ID             string         `json:"id"`
	WorkspaceID    string         `json:"workspace_id,omitempty"`
	Provider       string         `json:"provider"`
	EventType      string         `json:"event_type"`
	ExternalID     string         `json:"external_id"`
	Status         string         `json:"status"`
	Payload        map[string]any `json:"payload,omitempty"`
	Error          string         `json:"error,omitempty"`
	AttemptCount   int            `json:"attempt_count"`
	NextRetryAt    string         `json:"next_retry_at,omitempty"`
	LastAttemptAt  string         `json:"last_attempt_at,omitempty"`
	LeaseOwner     string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt string         `json:"lease_expires_at,omitempty"`
	FencingToken   int64          `json:"fencing_token"`
	ReceivedAt     string         `json:"received_at,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty"`
}

// IntegrationEventMappingIntent is the durable dispatch fact created when an
// inbound event is accepted. It deliberately stores only Integration-owned
// routing metadata; execution remains with the target domain worker.
type IntegrationEventMappingIntent struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id,omitempty"`
	EventID     string         `json:"event_id"`
	MappingKey  string         `json:"mapping_key,omitempty"`
	TargetType  string         `json:"target_type"`
	Status      string         `json:"status"`
	Payload     map[string]any `json:"payload,omitempty"`
	CreatedAt   string         `json:"created_at,omitempty"`
	UpdatedAt   string         `json:"updated_at,omitempty"`
}

type IntegrationEventRecordRequest struct {
	Provider   string         `json:"provider"`
	EventType  string         `json:"event_type"`
	ExternalID string         `json:"external_id"`
	Status     string         `json:"status,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
}

type IntegrationOfflineEventRecoveryRequest struct {
	Events []IntegrationEventRecordRequest `json:"events"`
}

type IntegrationOfflineEventRecoveryResult struct {
	Accepted               int                `json:"accepted"`
	Duplicates             int                `json:"duplicates"`
	ReconciliationRequired int                `json:"reconciliation_required"`
	Events                 []IntegrationEvent `json:"events"`
	ConflictExternalIDs    []string           `json:"conflict_external_ids,omitempty"`
}

type IntegrationEventStatusRequest struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type IntegrationEventRetryRequest struct {
	DelaySeconds int    `json:"delay_seconds,omitempty"`
	Error        string `json:"error,omitempty"`
}

type IntegrationInvocation struct {
	ID                  string         `json:"id"`
	WorkspaceID         string         `json:"workspace_id,omitempty"`
	ConnectorKey        string         `json:"connector_key"`
	ProviderKey         string         `json:"provider_key,omitempty"`
	ConnectionKey       string         `json:"connection_key,omitempty"`
	Operation           string         `json:"operation"`
	Status              string         `json:"status"`
	DurationMS          int64          `json:"duration_ms,omitempty"`
	RequestRef          string         `json:"request_ref,omitempty"`
	ResponseRef         string         `json:"response_ref,omitempty"`
	Error               string         `json:"error,omitempty"`
	EventID             string         `json:"event_id,omitempty"`
	ObjectKey           string         `json:"object_key,omitempty"`
	RecordID            string         `json:"record_id,omitempty"`
	WorkflowExecutionID string         `json:"workflow_execution_id,omitempty"`
	Metadata            map[string]any `json:"metadata,omitempty"`
	CreatedAt           string         `json:"created_at,omitempty"`
	UpdatedAt           string         `json:"updated_at,omitempty"`
}

type IntegrationInvocationRecordRequest struct {
	ConnectorKey        string         `json:"connector_key"`
	ProviderKey         string         `json:"provider_key,omitempty"`
	ConnectionKey       string         `json:"connection_key,omitempty"`
	Operation           string         `json:"operation"`
	Status              string         `json:"status,omitempty"`
	DurationMS          int64          `json:"duration_ms,omitempty"`
	RequestRef          string         `json:"request_ref,omitempty"`
	ResponseRef         string         `json:"response_ref,omitempty"`
	Error               string         `json:"error,omitempty"`
	EventID             string         `json:"event_id,omitempty"`
	ObjectKey           string         `json:"object_key,omitempty"`
	RecordID            string         `json:"record_id,omitempty"`
	WorkflowExecutionID string         `json:"workflow_execution_id,omitempty"`
	Metadata            map[string]any `json:"metadata,omitempty"`
}

type IntegrationInvocationStatusRequest struct {
	Status      string `json:"status"`
	DurationMS  int64  `json:"duration_ms,omitempty"`
	ResponseRef string `json:"response_ref,omitempty"`
	Error       string `json:"error,omitempty"`
}

type IntegrationOutboxMessage struct {
	ID                 string         `json:"id"`
	WorkspaceID        string         `json:"workspace_id,omitempty"`
	ConnectorKey       string         `json:"connector_key"`
	ConnectionKey      string         `json:"connection_key,omitempty"`
	Operation          string         `json:"operation"`
	Status             string         `json:"status"`
	Payload            map[string]any `json:"payload,omitempty"`
	EventID            string         `json:"event_id,omitempty"`
	RequestRef         string         `json:"request_ref,omitempty"`
	DedupKey           string         `json:"dedup_key,omitempty"`
	RequestFingerprint string         `json:"request_fingerprint,omitempty"`
	ResponseRef        string         `json:"response_ref,omitempty"`
	Error              string         `json:"error,omitempty"`
	AttemptCount       int            `json:"attempt_count"`
	NextAttemptAt      string         `json:"next_attempt_at,omitempty"`
	AckDeadlineAt      string         `json:"ack_deadline_at,omitempty"`
	LastAttemptAt      string         `json:"last_attempt_at,omitempty"`
	LeaseOwner         string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt     string         `json:"lease_expires_at,omitempty"`
	FencingToken       int64          `json:"fencing_token,omitempty"`
	CreatedBy          string         `json:"created_by,omitempty"`
	CreatedAt          string         `json:"created_at,omitempty"`
	UpdatedAt          string         `json:"updated_at,omitempty"`
}

type IntegrationOutboxEnqueueRequest struct {
	ConnectorKey  string         `json:"connector_key"`
	ConnectionKey string         `json:"connection_key,omitempty"`
	Operation     string         `json:"operation"`
	Payload       map[string]any `json:"payload,omitempty"`
	EventID       string         `json:"event_id,omitempty"`
	RequestRef    string         `json:"request_ref,omitempty"`
	DedupKey      string         `json:"dedup_key,omitempty"`
}

type IntegrationOutboxStatusRequest struct {
	Status      string `json:"status"`
	ResponseRef string `json:"response_ref,omitempty"`
	Error       string `json:"error,omitempty"`
}

type IntegrationOutboxRetryRequest struct {
	DelaySeconds int    `json:"delay_seconds,omitempty"`
	Error        string `json:"error,omitempty"`
}

// IntegrationIntentResult is the business-safe projection of one durable
// Connector write. It intentionally omits the original request payload,
// credentials, leases and worker fencing data.
type IntegrationIntentResult struct {
	ID            string         `json:"id"`
	ConnectorKey  string         `json:"connector_key"`
	ProviderKey   string         `json:"provider_key,omitempty"`
	ConnectionKey string         `json:"connection_key,omitempty"`
	Operation     string         `json:"operation"`
	Status        string         `json:"status"`
	ResponseRef   string         `json:"response_ref,omitempty"`
	Response      map[string]any `json:"response,omitempty"`
	Error         string         `json:"error,omitempty"`
	AttemptCount  int            `json:"attempt_count"`
	CreatedAt     string         `json:"created_at,omitempty"`
	UpdatedAt     string         `json:"updated_at,omitempty"`
}

type IntegrationWebhookSubscription struct {
	Key           string   `json:"key"`
	WorkspaceID   string   `json:"workspace_id,omitempty"`
	Name          string   `json:"name,omitempty"`
	ConnectorKey  string   `json:"connector_key"`
	ConnectionKey string   `json:"connection_key"`
	EventTypes    []string `json:"event_types,omitempty"`
	Status        string   `json:"status"`
	Description   string   `json:"description,omitempty"`
	CreatedBy     string   `json:"created_by,omitempty"`
	CreatedAt     string   `json:"created_at,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
	DisabledAt    string   `json:"disabled_at,omitempty"`
}

type IntegrationWebhookSubscriptionUpsertRequest struct {
	Key           string   `json:"key,omitempty"`
	Name          string   `json:"name,omitempty"`
	ConnectorKey  string   `json:"connector_key"`
	ConnectionKey string   `json:"connection_key"`
	EventTypes    []string `json:"event_types,omitempty"`
	Status        string   `json:"status,omitempty"`
	Description   string   `json:"description,omitempty"`
}

type IntegrationWebhookPublishRequest struct {
	EventType           string         `json:"event_type"`
	Payload             map[string]any `json:"payload,omitempty"`
	ObjectKey           string         `json:"object_key,omitempty"`
	RecordID            string         `json:"record_id,omitempty"`
	WorkflowExecutionID string         `json:"workflow_execution_id,omitempty"`
	RequestRef          string         `json:"request_ref,omitempty"`
}

type IntegrationWebhookPublishResult struct {
	EventType     string                           `json:"event_type"`
	Enqueued      int                              `json:"enqueued"`
	Subscriptions []IntegrationWebhookSubscription `json:"subscriptions,omitempty"`
	Messages      []IntegrationOutboxMessage       `json:"messages,omitempty"`
}

type IntegrationAgentToolInvocationRequest struct {
	ExternalIdentity IntegrationExternalIdentityResolveRequest `json:"external_identity,omitempty"`
	Input            map[string]any                            `json:"input,omitempty"`
	Approved         bool                                      `json:"approved,omitempty"`
	RequestRef       string                                    `json:"request_ref,omitempty"`
}

type IntegrationAgentToolInvocationResult struct {
	Agent            agentmodel.AgentSchema                   `json:"agent"`
	Tool             string                                   `json:"tool"`
	Status           string                                   `json:"status"`
	ExternalIdentity IntegrationExternalIdentityResolveResult `json:"external_identity"`
	ActionInvocation IntegrationInvocation                    `json:"invocation"`
	ApprovalPlan     map[string]any                           `json:"approval_plan,omitempty"`
}
