package runtime

import (
	connector "github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	notificationapplication "github.com/domainry/domainry-runtime/runtime/application/notification"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
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
	manifest            manifestmodel.ManifestSchema
	recordRepository    recordrepository.RecordRepository
	rateLimiter         ratelimit.Limiter
	notifications       *notificationapplication.NotificationApplicationService
	notificationHTTP    notificationhttp.NotificationApplication
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
	notificationHTTP := input.notificationHTTP
	if notificationHTTP == nil {
		notificationHTTP = input.notifications
	}
	return &Runtime{
		cfg:                input.config,
		templateID:         input.templateID,
		store:              input.store,
		records:            input.applicationServices,
		identityBinding:    input.identityBinding,
		identityDirectory:  input.identityDirectory,
		identityPrincipals: input.identityPrincipals,
		manifest:           input.manifest,
		recordRepo:         input.recordRepository,
		rateLimiter:        input.rateLimiter,
		notifications:      input.notifications,
		notificationHTTP:   notificationHTTP,
		worker:             workerplatform.NormalizeDependencies(input.worker),
		businessHandlers:   input.businessHandlers,
		connectorProviders: input.connectorProviders,
		releaseIdentity:    input.releaseIdentity,
		releaseCohort:      input.releaseCohort,
		releaseLease:       input.releaseLease,
		releaseAdmission:   input.releaseAdmission,
		releaseIntegrity:   input.releaseIntegrity,
	}
}
