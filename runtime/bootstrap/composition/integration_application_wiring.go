package composition

import (
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	"github.com/domainry/domainry-runtime/runtime/platform/resilience"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type IntegrationRuntimeWiringDependencies struct {
	ConfigRepository        integrationrepository.IntegrationConfigRepository
	EventRepository         integrationrepository.IntegrationEventRepository
	DeliveryRepository      integrationrepository.IntegrationDeliveryRepository
	WorkerRepository        integrationrepository.IntegrationWorkerRepository
	WorkerWakeups           *workerplatform.WakeupBroker
	PolicyStore             resilience.Store
	APILimiter              ratelimit.Limiter
	ConnectorRegistry       *integrationapplication.ConnectorRegistry
	Automation              integrationapplication.IntegrationAutomationApplication
	Audit                   integrationapplication.AuditFunc
	ConnectionHistory       integrationapplication.IntegrationConnectionHistoryReader
	Principal               integrationapplication.PrincipalResolver
	Schema                  integrationapplication.IntegrationSchemaProvider
	SchemaObjectMap         integrationapplication.IntegrationSchemaObjectMapProvider
	InvokeAction            integrationapplication.IntegrationActionInvoker
	Records                 integrationapplication.IntegrationAgentRecordApplication
	EventRecords            integrationapplication.IntegrationEventRecordApplication
	Workflows               integrationapplication.IntegrationWorkflowApplication
	EventWorkflowExecutor   integrationapplication.IntegrationEventWorkflowExecutor
	EventActionExecutor     integrationapplication.IntegrationEventActionExecutor
	EventIdentityResolver   integrationapplication.IntegrationEventIdentityResolver
	Worker                  workerplatform.Dependencies
	NotificationCompiler    integrationapplication.IntegrationNotificationCompiler
	NotificationPublisher   integrationapplication.IntegrationNotificationPublisher
	CredentialNotifications integrationapplication.IntegrationCredentialNotificationCommitter
	CredentialExpirySource  integrationapplication.IntegrationCredentialExpirySource
	OutboxPayloadPreparer   integrationapplication.OutboxPayloadPreparer
}
