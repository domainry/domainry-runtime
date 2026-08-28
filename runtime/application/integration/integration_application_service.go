package integration

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"errors"
	"strings"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"

	"time"

	resilience "github.com/domainry/domainry-runtime/runtime/platform/resilience"
)

type AuditFunc func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
type IntegrationConnectionHistoryReader interface {
	Events(context.Context, auditmodel.AuditEventQuery, principalmodel.Principal) ([]auditmodel.AuditEvent, error)
}
type ConnectionNormalizer func(context.Context, integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error)
type InvocationProviderResolver func(context.Context, string, string, string, string) (string, error)
type ConnectorExists func(string) bool
type EventMappingExecutor func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, bool, error)
type AutomationOutboxExecutor func(context.Context, integrationmodel.IntegrationOutboxMessage) error
type AdapterOutboxSender func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error)
type OutboxPayloadPreparer func(context.Context, integrationmodel.IntegrationOutboxMessage, map[string]any) (map[string]any, error)
type OutboxAdapterResult struct {
	Response          map[string]any
	ResponseRef       string
	SecretUpdates     map[string]string
	ProviderErrorCode string
	ResourceHealth    *integrationmodel.IntegrationProviderResourceHealth
}
type PreparedOutboxCall struct {
	Operation string
	Execute   func(context.Context, map[string]string) (OutboxAdapterResult, error)
}
type OperationInputValidator func(string, integrationmodel.ConnectorOperationSchema, map[string]any) error
type OperationOutputValidator func(string, integrationmodel.ConnectorOperationSchema, map[string]any) error
type SyncCallRequest struct {
	ConnectorKey       string
	ConnectionKey      string
	Operation          string
	ContractSHA256     string
	OperationMode      string
	OperationEffect    string
	ActionExecution    bool
	Method             string
	Request            map[string]any
	ObjectKey          string
	RecordID           string
	ActionKey          string
	InvocationKey      string
	InvocationMode     string
	SideEffect         string
	RequestRef         string
	Compensation       map[string]any
	CompensationPolicy any
	ResponseSchema     *definitionmodel.ObjectSchema
	ResponseStrict     bool
	CircuitThreshold   int
	CircuitCooldown    time.Duration
	RateLimitCount     int
	RateLimitWindow    time.Duration
	Timeout            time.Duration
}
type SyncCallResult struct {
	ActionInvocation integrationmodel.IntegrationInvocation
	Response         map[string]any
}
type SyncAdapterResult struct {
	Response          map[string]any
	ResponseRef       string
	SecretUpdates     map[string]string
	ProviderErrorCode string
	ResourceHealth    *integrationmodel.IntegrationProviderResourceHealth
}
type PreparedSyncCall struct {
	Execute func(context.Context, SyncCallRequest, map[string]any, string, principalmodel.Principal, map[string]string) (SyncAdapterResult, error)
}
type PrincipalResolver func(context.Context, string, string, string) principalmodel.Principal
type UnmappedReadOnlyPrincipalResolver func(context.Context, string, string) principalmodel.Principal
type ConnectionReference struct {
	Kind string
	Key  string
	Path string
}
type ConnectionReferenceCheck func(context.Context, string, principalmodel.Principal) ([]ConnectionReference, error)
type ConnectionDraftValidator func(context.Context, string, integrationmodel.IntegrationConnectionUpsertRequest, principalmodel.Principal) error
type ConnectionConfigPreparer func(integrationmodel.ConnectorSchema, string, string, map[string]any) (map[string]any, error)
type WebhookSignatureAlgorithmNormalizer func(string) string
type WebhookSignatureVerifier func(integrationmodel.IntegrationWebhookSignatureCheck) integrationmodel.IntegrationWebhookSignatureCheckResult
type IntegrationNotificationCompiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
type IntegrationNotificationPublisher func(context.Context, notificationmodel.NotificationIntent, principalmodel.SystemScope) (notificationmodel.NotificationEvent, bool, error)

// IntegrationCredentialNotificationCommitter keeps the producer-owned state
// transition and its durable Notification Event in one database transaction.
// Notification still owns compilation, rendering, delivery and inbox state.
type IntegrationCredentialNotificationCommitter interface {
	CommitIntegrationConnectionNotification(context.Context, integrationmodel.IntegrationConnection, notificationmodel.NotificationEvent) (integrationmodel.IntegrationConnection, error)
	CommitIntegrationSecretNotification(context.Context, integrationmodel.IntegrationSecret, string, notificationmodel.NotificationEvent) (integrationmodel.IntegrationSecret, error)
}

type IntegrationCredentialExpirySource interface {
	ListIntegrationCredentialExpiryCandidates(context.Context, principalmodel.SystemScope, string, int) ([]integrationmodel.IntegrationSecret, error)
}

type ApplicationDependencies struct {
	ConfigRepository           integrationrepository.IntegrationConfigRepository
	EventRepository            integrationrepository.IntegrationEventRepository
	DeliveryRepository         integrationrepository.IntegrationDeliveryRepository
	WorkerRepository           integrationrepository.IntegrationWorkerRepository
	Registry                   Registry
	Audit                      AuditFunc
	ConnectionHistory          IntegrationConnectionHistoryReader
	PolicyStore                resilience.Store
	APILimiter                 ratelimit.Limiter
	ConnectionNormalizer       ConnectionNormalizer
	InvocationProviderResolver InvocationProviderResolver
	ConnectorExists            ConnectorExists
	EventMappingExecutor       EventMappingExecutor
	AutomationOutboxExecutor   AutomationOutboxExecutor
	AdapterOutboxSender        AdapterOutboxSender
	OutboxPayloadPreparer      OutboxPayloadPreparer
	OperationInputValidator    OperationInputValidator
	OperationOutputValidator   OperationOutputValidator
	PrincipalResolver          PrincipalResolver
	UnmappedPrincipalResolver  UnmappedReadOnlyPrincipalResolver
	ConnectionReferenceCheck   ConnectionReferenceCheck
	ConnectionDraftValidator   ConnectionDraftValidator
	ConnectionConfigPreparer   ConnectionConfigPreparer
	WebhookAlgorithmNormalizer WebhookSignatureAlgorithmNormalizer
	WebhookSignatureVerifier   WebhookSignatureVerifier
	ConnectorCapacity          *capacityplatform.Controller
	NotificationCompiler       IntegrationNotificationCompiler
	NotificationPublisher      IntegrationNotificationPublisher
	CredentialNotifications    IntegrationCredentialNotificationCommitter
	CredentialExpirySource     IntegrationCredentialExpirySource
	Schema                     IntegrationSchemaProvider
	SchemaObjectMap            IntegrationSchemaObjectMapProvider
	InvokeAction               IntegrationActionInvoker
	Records                    IntegrationAgentRecordApplication
	EventRecords               IntegrationEventRecordApplication
	Workflows                  IntegrationWorkflowApplication
	Automation                 IntegrationAutomationApplication
	EventWorkflowExecutor      IntegrationEventWorkflowExecutor
	EventActionExecutor        IntegrationEventActionExecutor
	EventIdentityResolver      IntegrationEventIdentityResolver
	Worker                     workerplatform.Dependencies
	WorkerWakeups              *workerplatform.WakeupBroker
}

// IntegrationApplicationService owns integration lifecycle behavior.
type IntegrationApplicationService struct {
	configRepo                integrationrepository.IntegrationConfigRepository
	eventRepo                 integrationrepository.IntegrationEventRepository
	deliveryRepo              integrationrepository.IntegrationDeliveryRepository
	workerRepo                integrationrepository.IntegrationWorkerRepository
	providerStateRepo         integrationrepository.ConnectorProviderStateRepository
	registry                  Registry
	audit                     AuditFunc
	connectionHistory         IntegrationConnectionHistoryReader
	policyStore               resilience.Store
	apiLimiter                ratelimit.Limiter
	normalizeConnection       ConnectionNormalizer
	resolveInvocationProvider InvocationProviderResolver
	connectorExists           ConnectorExists
	executeEventMapping       EventMappingExecutor
	executeAutomationOutbox   AutomationOutboxExecutor
	sendAdapterOutbox         AdapterOutboxSender
	prepareOutboxPayload      OutboxPayloadPreparer
	validateOperationInput    OperationInputValidator
	validateOperationOutput   OperationOutputValidator
	resolvePrincipal          PrincipalResolver
	resolveUnmappedReadOnly   UnmappedReadOnlyPrincipalResolver
	connectionReferences      ConnectionReferenceCheck
	validateConnectionDraft   ConnectionDraftValidator
	prepareConnectionConfig   ConnectionConfigPreparer
	normalizeWebhookAlgorithm WebhookSignatureAlgorithmNormalizer
	verifyWebhookSignature    WebhookSignatureVerifier
	principal                 PrincipalResolver
	schema                    IntegrationSchemaProvider
	schemaMap                 IntegrationSchemaObjectMapProvider
	invokeAction              IntegrationActionInvoker
	recordsApp                IntegrationAgentRecordApplication
	eventRecords              IntegrationEventRecordApplication
	workflows                 IntegrationWorkflowApplication
	automation                IntegrationAutomationApplication
	executeEventWorkflow      IntegrationEventWorkflowExecutor
	executeEventAction        IntegrationEventActionExecutor
	resolveEventIdentity      IntegrationEventIdentityResolver
	operationalMetrics        *integrationOperationalMetrics
	connectorCapacity         *capacityplatform.Controller
	compileNotification       IntegrationNotificationCompiler
	publishNotification       IntegrationNotificationPublisher
	credentialNotifications   IntegrationCredentialNotificationCommitter
	credentialExpirySource    IntegrationCredentialExpirySource
	worker                    workerplatform.Dependencies
	eventWakeups              chan IntegrationEventLocator
	outboxWakeups             <-chan workerplatform.DurableTaskLocator
	publishOutboxWakeup       func(workerplatform.DurableTaskLocator)
	providerStateWakeups      <-chan workerplatform.DurableTaskLocator
}

func NewIntegrationApplicationService(dependencies ApplicationDependencies) *IntegrationApplicationService {
	dependencies.Worker = workerplatform.NormalizeDependencies(dependencies.Worker)
	outboxWakeups, publishOutboxWakeup := newIntegrationOutboxWakeupBinding(dependencies.WorkerWakeups)
	audit := dependencies.Audit
	if audit == nil {
		audit = func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		}
	}
	service := &IntegrationApplicationService{
		configRepo:              dependencies.ConfigRepository,
		eventRepo:               dependencies.EventRepository,
		deliveryRepo:            dependencies.DeliveryRepository,
		workerRepo:              dependencies.WorkerRepository,
		registry:                dependencies.Registry,
		audit:                   audit,
		connectionHistory:       dependencies.ConnectionHistory,
		policyStore:             dependencies.PolicyStore,
		apiLimiter:              dependencies.APILimiter,
		principal:               dependencies.PrincipalResolver,
		schema:                  dependencies.Schema,
		schemaMap:               dependencies.SchemaObjectMap,
		invokeAction:            dependencies.InvokeAction,
		recordsApp:              dependencies.Records,
		eventRecords:            dependencies.EventRecords,
		workflows:               dependencies.Workflows,
		automation:              dependencies.Automation,
		operationalMetrics:      newIntegrationOperationalMetrics(),
		connectorCapacity:       capacityplatform.NewController(capacityplatform.Limits{GlobalInFlight: 64, WorkspaceInFlight: 16, UseCaseInFlight: 8, RetryInFlight: 8, GlobalRate: 1200, WorkspaceRate: 300, UseCaseRate: 600, RateWindow: time.Minute, MaxWorkspaceStates: 10_000, MaxUseCaseStates: 256}, nil),
		worker:                  dependencies.Worker,
		eventWakeups:            make(chan IntegrationEventLocator, 256),
		outboxWakeups:           outboxWakeups,
		publishOutboxWakeup:     publishOutboxWakeup,
		compileNotification:     dependencies.NotificationCompiler,
		publishNotification:     dependencies.NotificationPublisher,
		credentialNotifications: dependencies.CredentialNotifications,
		credentialExpirySource:  dependencies.CredentialExpirySource,
		normalizeConnection: func(_ context.Context, connection integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
			return connection, nil
		},
		resolvePrincipal: func(context.Context, string, string, string) principalmodel.Principal {
			return principalmodel.Principal{}
		},
		resolveUnmappedReadOnly: func(context.Context, string, string) principalmodel.Principal { return principalmodel.Principal{} },
		connectionReferences: func(context.Context, string, principalmodel.Principal) ([]ConnectionReference, error) {
			return nil, nil
		},
		validateConnectionDraft: func(context.Context, string, integrationmodel.IntegrationConnectionUpsertRequest, principalmodel.Principal) error {
			return nil
		},
		prepareConnectionConfig: func(connector integrationmodel.ConnectorSchema, providerKey, status string, config map[string]any) (map[string]any, error) {
			prepared := integrationcontract.IntegrationApplyProviderConfigDefaults(connector, providerKey, config)
			if err := ValidateConnectionConfig(connector, providerKey, status, prepared); err != nil {
				return nil, err
			}
			return prepared, nil
		},
		verifyWebhookSignature: func(integrationmodel.IntegrationWebhookSignatureCheck) integrationmodel.IntegrationWebhookSignatureCheckResult {
			return integrationmodel.IntegrationWebhookSignatureCheckResult{Failure: "invalid_algorithm"}
		},
		normalizeWebhookAlgorithm: func(string) string { return "" },
		executeAutomationOutbox: func(context.Context, integrationmodel.IntegrationOutboxMessage) error {
			return errors.New("backend.integration.outbox.automation_executor_unavailable")
		},
		sendAdapterOutbox: func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
			return OutboxSendResult{}, errors.New("backend.integration.outbox.sender_not_found")
		},
		prepareOutboxPayload: func(_ context.Context, _ integrationmodel.IntegrationOutboxMessage, payload map[string]any) (map[string]any, error) {
			return payload, nil
		},
		validateOperationInput:  func(string, integrationmodel.ConnectorOperationSchema, map[string]any) error { return nil },
		validateOperationOutput: func(string, integrationmodel.ConnectorOperationSchema, map[string]any) error { return nil },
		executeEventMapping: func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, bool, error) {
			return EventProcessDecision{}, false, nil
		},
	}
	if repository, ok := dependencies.ConfigRepository.(integrationrepository.ConnectorProviderStateRepository); ok {
		service.providerStateRepo = repository
	}
	if dependencies.ConnectorCapacity != nil {
		service.connectorCapacity = dependencies.ConnectorCapacity
	}
	if dependencies.OutboxPayloadPreparer != nil {
		service.prepareOutboxPayload = dependencies.OutboxPayloadPreparer
	}
	service.connectorExists = service.ConnectorExists
	if dependencies.ConnectionNormalizer != nil {
		service.normalizeConnection = dependencies.ConnectionNormalizer
	}
	if dependencies.InvocationProviderResolver != nil {
		service.resolveInvocationProvider = dependencies.InvocationProviderResolver
	}
	if dependencies.ConnectorExists != nil {
		service.connectorExists = dependencies.ConnectorExists
	}
	if dependencies.EventMappingExecutor != nil {
		service.executeEventMapping = dependencies.EventMappingExecutor
	}
	if dependencies.AutomationOutboxExecutor != nil {
		service.executeAutomationOutbox = dependencies.AutomationOutboxExecutor
	}
	if dependencies.AdapterOutboxSender != nil {
		service.sendAdapterOutbox = dependencies.AdapterOutboxSender
	}
	if dependencies.OperationInputValidator != nil {
		service.validateOperationInput = dependencies.OperationInputValidator
	}
	if dependencies.OperationOutputValidator != nil {
		service.validateOperationOutput = dependencies.OperationOutputValidator
	}
	if dependencies.PrincipalResolver != nil {
		service.resolvePrincipal = dependencies.PrincipalResolver
	}
	if dependencies.UnmappedPrincipalResolver != nil {
		service.resolveUnmappedReadOnly = dependencies.UnmappedPrincipalResolver
	}
	if dependencies.ConnectionReferenceCheck != nil {
		service.connectionReferences = dependencies.ConnectionReferenceCheck
	}
	if dependencies.ConnectionDraftValidator != nil {
		service.validateConnectionDraft = dependencies.ConnectionDraftValidator
	}
	if dependencies.ConnectionConfigPreparer != nil {
		service.prepareConnectionConfig = dependencies.ConnectionConfigPreparer
	}
	if dependencies.WebhookAlgorithmNormalizer != nil {
		service.normalizeWebhookAlgorithm = dependencies.WebhookAlgorithmNormalizer
	}
	if dependencies.WebhookSignatureVerifier != nil {
		service.verifyWebhookSignature = dependencies.WebhookSignatureVerifier
	}
	service.executeEventWorkflow = dependencies.EventWorkflowExecutor
	if service.executeEventWorkflow == nil {
		service.executeEventWorkflow = service.RunIntegrationWorkflow
	}
	service.executeEventAction = dependencies.EventActionExecutor
	if service.executeEventAction == nil {
		service.executeEventAction = service.ExecuteIntegrationAction
	}
	service.resolveEventIdentity = dependencies.EventIdentityResolver
	if service.resolveEventIdentity == nil {
		service.resolveEventIdentity = service.ResolveIntegrationExternalIdentity
	}
	return service
}

func (s *IntegrationApplicationService) UseConnectorCapacity(_ context.Context, controller *capacityplatform.Controller) {
	if s != nil && controller != nil {
		s.connectorCapacity = controller
	}
}

// validateSyncOperationIdentity prevents generated callers compiled against a
// stale operation mode or contract from crossing the Runtime boundary.
func (s *IntegrationApplicationService) validateSyncOperationIdentity(connection integrationmodel.IntegrationConnection, req SyncCallRequest) error {
	contractSHA256, mode := strings.TrimSpace(req.ContractSHA256), strings.TrimSpace(req.OperationMode)
	if contractSHA256 == "" && mode == "" {
		return nil
	}
	if contractSHA256 == "" || mode == "" {
		return badRequest("backend.integration.sync_call.operation_identity_required")
	}
	adapter, ok := s.AdapterForConnection(connection)
	if !ok {
		return badRequest("backend.integration.sync_call.connector_unsupported", "connector", connection.ConnectorKey)
	}
	identities, ok := adapter.(integrationcontract.OperationIdentityProvider)
	if !ok {
		return badRequest("backend.integration.sync_call.operation_identity_unavailable", "connector", connection.ConnectorKey, "provider", connection.ProviderKey)
	}
	expected, ok := identities.OperationIdentity(req.Operation)
	if !ok {
		return badRequest("backend.integration.operation.provider_unsupported", "connector", connection.ConnectorKey, "provider", connection.ProviderKey, "operation", req.Operation)
	}
	if mode != expected.Mode {
		return badRequest("backend.integration.sync_call.operation_mode_mismatch", "expected", expected.Mode, "actual", mode)
	}
	if contractSHA256 != expected.ContractSHA256 {
		return badRequest("backend.integration.sync_call.operation_contract_mismatch", "expected", expected.ContractSHA256, "actual", contractSHA256)
	}
	if effect := strings.TrimSpace(req.OperationEffect); effect != "" && effect != expected.Effect {
		return badRequest("backend.integration.sync_call.operation_effect_mismatch", "expected", expected.Effect, "actual", effect)
	}
	return nil
}
