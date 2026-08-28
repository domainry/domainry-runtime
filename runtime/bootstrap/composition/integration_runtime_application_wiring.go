package composition

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func integrationApplication(records *runtimeAssembly) *integrationapplication.IntegrationApplicationService {
	if records != nil && records.integrationService != nil {
		return records.integrationService
	}
	if records == nil {
		return newIntegrationApplicationServiceWithDependencies(IntegrationRuntimeWiringDependencies{})
	}
	applications := records.Applications()
	return newIntegrationApplicationServiceWithDependencies(IntegrationRuntimeWiringDependencies{
		ConfigRepository: records.integrationConfigRepo, EventRepository: records.integrationEventRepo,
		DeliveryRepository: records.integrationDeliveryRepo, WorkerRepository: records.integrationWorkerRepo,
		WorkerWakeups: records.workerWakeups,
		PolicyStore:   records.integrationPolicyStore, APILimiter: records.apiKeyRateLimiter,
		Audit: records.auditApplicationService.AppendWithMetadata, ConnectionHistory: records.auditApplicationService, ConnectorRegistry: records.connectorRegistry,
		Principal: func(ctx context.Context, userID, roleKey, fallbackRoleKey string) principalmodel.Principal {
			if roleKey == "" {
				roleKey = fallbackRoleKey
			}
			principal, err := resolveIdentityPrincipal(ctx, records.agentPrincipals, userID, roleKey)
			if err != nil {
				return principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}
			}
			return principal
		}, Schema: records.SchemaForPrincipal,
		SchemaObjectMap: func(ctx context.Context) map[string]definitionmodel.ObjectSchema {
			return records.schemaService.ObjectMap(ctx)
		},
		InvokeAction: integrationRuntimeActionInvoker(applications),
		Records:      applications.Records, EventRecords: applications.Records,
		Workflows: applications.Workflows, Automation: applications.Automations,
		NotificationCompiler:    records.integrationNotificationCompiler,
		NotificationPublisher:   records.integrationNotificationPublisher,
		CredentialNotifications: records.integrationCredentialNotifications,
		CredentialExpirySource:  records.integrationCredentialExpirySource,
		OutboxPayloadPreparer:   records.prepareOutboxPayload,
		Worker:                  records.workerDependencies,
	})
}

func integrationRuntimeActionInvoker(applications RuntimeApplications) func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
	return func(ctx context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
		return applications.Actions.Invoke(ctx, actionmodel.ActionSourceIntegration, invocation)
	}
}
