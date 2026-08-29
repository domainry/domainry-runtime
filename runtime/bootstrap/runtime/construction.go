package runtime

import (
	connector "github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	partysdk "github.com/domainry/domainry-party-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	notificationpublication "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	notificationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/notifications"
)

type runtimeConstructionInput struct {
	config              config.Config
	templateID          string
	store               *persistence.RuntimeStore
	applicationServices *composition.RuntimeServices
	identityBinding     identitysdk.Binding
	identityDirectory   identitysdk.Directory
	identityPrincipals  identitysdk.PrincipalResolver
	partyBinding        partysdk.Binding
	manifest            manifestmodel.ManifestSchema
	recordRepository    recordrepository.RecordRepository
	rateLimiter         ratelimit.Limiter
	notificationHTTP    notificationhttp.NotificationApplication
	notificationBinding notificationsdk.Binding
	monitoringBinding   monitoringsdk.Binding
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
		partyBinding:        input.partyBinding,
		manifest:            input.manifest,
		recordRepo:          input.recordRepository,
		rateLimiter:         input.rateLimiter,
		notificationHTTP:    input.notificationHTTP,
		notificationBinding: input.notificationBinding,
		monitoringBinding:   input.monitoringBinding,
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
