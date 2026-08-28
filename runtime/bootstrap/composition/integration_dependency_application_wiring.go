package composition

import (
	"context"

	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"
	integrationvalidation "github.com/domainry/domainry-runtime/runtime/domain/integration/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func newIntegrationApplicationServiceWithDependencies(dependencies IntegrationRuntimeWiringDependencies) *integrationapplication.IntegrationApplicationService {
	registry := dependencies.ConnectorRegistry
	if registry == nil {
		registry = integrationapplication.NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	}
	automation := dependencies.Automation
	if automation == nil {
		automation = automationapplication.NewAutomationApplicationService(automationapplication.AutomationApplicationDependencies{Connectors: registry})
	}
	var service *integrationapplication.IntegrationApplicationService
	service = integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{
		ConfigRepository: dependencies.ConfigRepository, EventRepository: dependencies.EventRepository,
		DeliveryRepository: dependencies.DeliveryRepository, WorkerRepository: dependencies.WorkerRepository,
		Registry: registry, Audit: dependencies.Audit, ConnectionHistory: dependencies.ConnectionHistory, PolicyStore: dependencies.PolicyStore, APILimiter: dependencies.APILimiter,
		Worker:                  dependencies.Worker,
		WorkerWakeups:           dependencies.WorkerWakeups,
		NotificationCompiler:    dependencies.NotificationCompiler,
		NotificationPublisher:   dependencies.NotificationPublisher,
		CredentialNotifications: dependencies.CredentialNotifications,
		CredentialExpirySource:  dependencies.CredentialExpirySource,
		OutboxPayloadPreparer:   dependencies.OutboxPayloadPreparer,
		Schema:                  dependencies.Schema, SchemaObjectMap: dependencies.SchemaObjectMap, InvokeAction: dependencies.InvokeAction,
		Records: dependencies.Records, EventRecords: dependencies.EventRecords, Workflows: dependencies.Workflows, Automation: automation,
		EventWorkflowExecutor: dependencies.EventWorkflowExecutor, EventActionExecutor: dependencies.EventActionExecutor, EventIdentityResolver: dependencies.EventIdentityResolver,
		ConnectionNormalizer: func(ctx context.Context, connection integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
			return service.MigratePersistedConnectionProvider(ctx, connection)
		},
		WebhookAlgorithmNormalizer: integrationvalidation.IntegrationNormalizeWebhookSignatureAlgorithm,
		WebhookSignatureVerifier:   integrationvalidation.IntegrationVerifyWebhookSignature,
		ConnectionDraftValidator: func(ctx context.Context, key string, request integrationmodel.IntegrationConnectionUpsertRequest, principal principalmodel.Principal) error {
			return service.ValidateIntegrationConnectionDraft(ctx, key, request, principal)
		},
		ConnectionReferenceCheck: serviceReferenceResolver(&service),
		EventMappingExecutor: func(ctx context.Context, event integrationmodel.IntegrationEvent, principal principalmodel.Principal) (integrationapplication.EventProcessDecision, bool, error) {
			return service.ExecuteIntegrationEventMapping(ctx, event, principal)
		},
		OperationInputValidator: func(_ string, operation integrationmodel.ConnectorOperationSchema, payload map[string]any) error {
			return automationvalidation.AutomationValidateOperationInput(operation, payload, definitionmodel.ObjectSchema{}, nil)
		},
		OperationOutputValidator: func(connectorKey string, operation integrationmodel.ConnectorOperationSchema, payload map[string]any) error {
			return automation.ValidateIntegrationOutput(automationmodel.AutomationInstructionSchema{ConnectorKey: connectorKey, Operation: operation.Key}, payload)
		},
		AutomationOutboxExecutor: automation.ExecuteOutboxMessage,
		AdapterOutboxSender: func(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, principal principalmodel.Principal) (integrationapplication.OutboxSendResult, error) {
			return service.SendAdapterOutboxMessage(ctx, message, principal)
		},
		InvocationProviderResolver: func(ctx context.Context, connectorKey, connectionKey, requestedProvider, workspaceID string) (string, error) {
			return service.ResolveIntegrationDeliveryProvider(ctx, connectorKey, connectionKey, requestedProvider, workspaceID)
		},
		ConnectorExists:   func(key string) bool { return service.ConnectorExists(key) },
		PrincipalResolver: dependencies.Principal,
		UnmappedPrincipalResolver: func(ctx context.Context, workspaceID, externalPrincipal string) principalmodel.Principal {
			if dependencies.SchemaObjectMap == nil {
				return principalmodel.Principal{}
			}
			return integrationprojection.IntegrationUnmappedReadOnlyPrincipal(workspaceID, externalPrincipal, dependencies.SchemaObjectMap(ctx))
		},
	})
	return service
}

func serviceReferenceResolver(service **integrationapplication.IntegrationApplicationService) integrationapplication.ConnectionReferenceCheck {
	return func(ctx context.Context, connectionKey string, principal principalmodel.Principal) ([]integrationapplication.ConnectionReference, error) {
		return (*service).IntegrationConnectionReferences(ctx, connectionKey, principal)
	}
}
