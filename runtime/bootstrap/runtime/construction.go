package runtime

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	auditsdk "github.com/domainry/domainry-audit-sdk"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	partysdk "github.com/domainry/domainry-party-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	notificationfacade "github.com/domainry/domainry-runtime/runtime/application/notificationfacade"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	notificationpublication "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-foundation/ratelimit"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type runtimeConstructionInput struct {
	config              config.Config
	templateID          string
	store               *persistence.RuntimeStore
	applicationServices *composition.RuntimeServices
	identityBinding     identitysdk.Binding
	identityDirectory   identitysdk.Directory
	identityPrincipals  identitysdk.PrincipalResolver
	integrationMode     integrationsdk.DeploymentMode
	integrationBinding  integrationsdk.Binding
	integrationWorkers  integrationsdk.LocalWorkers
	partyBinding        partysdk.Binding
	dataExchangeBinding dataexchangesdk.Binding
	lifecycleBinding    lifecyclesdk.Binding
	manifest            manifestmodel.ManifestSchema
	recordRepository    recordrepository.RecordRepository
	rateLimiter         ratelimit.Limiter
	notificationHTTP    *notificationfacade.NotificationApplicationService
	notificationBinding notificationsdk.Binding
	monitoringBinding   monitoringsdk.Binding
	schedulerBinding    schedulersdk.Binding
	agentBinding        agentsdk.Binding
	auditBinding        auditsdk.Binding
	metadataBinding     metadatasdk.Binding
	reportBinding       reportsdk.Binding
	notificationWorkers notificationsdk.LocalWorkers
	notificationRelay   *notificationpublication.Relay
	worker              workerplatform.Dependencies
	businessHandlers    *runtimeext.BusinessHandlerRegistry
	connectorProviders  *connector.Registry
	releaseIdentity     runtimehttp.RuntimeReleaseIdentity
	releaseCohort       *deploymentapplication.DeploymentRuntimeReleaseCohortApplicationService
	releaseLease        deploymentmodel.RuntimeReleaseCohortLease
	releaseAdmission    *deploymentapplication.RuntimeReleaseAdmission
	releaseIntegrity    *deploymentapplication.RuntimeReleaseIntegrity
}

func constructRuntime(input runtimeConstructionInput) *Runtime {
	return &Runtime{
		cfg:                 input.config,
		templateID:          input.templateID,
		store:               input.store,
		records:             input.applicationServices,
		identityBinding:     input.identityBinding,
		identityDirectory:   input.identityDirectory,
		identityPrincipals:  input.identityPrincipals,
		integrationMode:     input.integrationMode,
		integrationBinding:  input.integrationBinding,
		integrationWorkers:  input.integrationWorkers,
		partyBinding:        input.partyBinding,
		dataExchangeBinding: input.dataExchangeBinding,
		lifecycleBinding:    input.lifecycleBinding,
		manifest:            input.manifest,
		recordRepo:          input.recordRepository,
		rateLimiter:         input.rateLimiter,
		notificationHTTP:    input.notificationHTTP,
		notificationBinding: input.notificationBinding,
		monitoringBinding:   input.monitoringBinding,
		schedulerBinding:    input.schedulerBinding,
		agentBinding:        input.agentBinding,
		auditBinding:        input.auditBinding,
		metadataBinding:     input.metadataBinding,
		reportBinding:       input.reportBinding,
		notificationWorkers: input.notificationWorkers,
		notificationRelay:   input.notificationRelay,
		worker:              workerplatform.NormalizeDependencies(input.worker),
		businessHandlers:    input.businessHandlers,
		connectorProviders:  input.connectorProviders,
		releaseIdentity:     input.releaseIdentity,
		releaseCohort:       input.releaseCohort,
		releaseLease:        input.releaseLease,
		releaseAdmission:    input.releaseAdmission,
		releaseIntegrity:    input.releaseIntegrity,
	}
}
