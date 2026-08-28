package runtime

import (
	"context"
	"sync"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	notificationpublication "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	notificationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/notifications"
)

// Runtime owns the process-level composition and lifecycle.
type Runtime struct {
	cfg                 config.Config
	templateID          string
	store               *persistence.RuntimeStore
	borrowedStore       bool
	records             *composition.RuntimeServices
	identityBinding     identitysdk.Binding
	identityDirectory   identitysdk.Directory
	identityPrincipals  identitysdk.PrincipalResolver
	manifest            manifestmodel.ManifestSchema
	recordRepo          recordrepository.RecordRepository
	rateLimiter         ratelimit.Limiter
	notificationHTTP    notificationhttp.NotificationApplication
	notificationBinding notificationsdk.Binding
	notificationWorkers notificationsdk.LocalWorkers
	notificationRelay   *notificationpublication.Relay
	worker              workerplatform.Dependencies
	api                 *runtimehttp.HTTPRouter
	businessHandlers    *runtimeext.BusinessHandlerRegistry
	connectorProviders  *connector.Registry
	releaseIdentity     runtimehttp.RuntimeReleaseIdentity
	releaseCohort       *deploymentapplication.DeploymentRuntimeReleaseCohortApplicationService
	releaseLease        deploymentmodel.RuntimeReleaseCohortLease
	releaseAdmission    *deploymentapplication.RuntimeReleaseAdmission
	releaseIntegrity    *deploymentapplication.RuntimeReleaseIntegrity
	releaseMu           sync.Mutex

	workersMu                     sync.Mutex
	workerCancels                 []context.CancelFunc
	workerDone                    []<-chan struct{}
	workersStarted                bool
	workersClosing                bool
	operationsControlPollInterval time.Duration
	operationsControlMu           sync.Mutex
	operationsControlRefreshedAt  time.Time
	operationsControlSnapshot     map[string]operationsmodel.OperationsControl
	operationsControlSnapshotErr  error
	operationsControlLoader       func(context.Context) ([]operationsmodel.OperationsControl, error)
}

func (a *Runtime) runtimeReleaseLease() deploymentmodel.RuntimeReleaseCohortLease {
	a.releaseMu.Lock()
	defer a.releaseMu.Unlock()
	return a.releaseLease
}

func (a *Runtime) replaceRuntimeReleaseLease(lease deploymentmodel.RuntimeReleaseCohortLease) {
	a.releaseMu.Lock()
	defer a.releaseMu.Unlock()
	a.releaseLease = lease
}

// ActionConnectorGateway returns the internal synchronous Connector composition
// port without expanding Runtime's process-level method surface.
func ActionConnectorGateway(runtime *Runtime) *integrationapplication.ActionConnectorGateway {
	if runtime == nil || runtime.records == nil {
		return integrationapplication.NewActionConnectorGateway(nil)
	}
	return integrationapplication.NewActionConnectorGateway(runtime.records.Applications().Integrations)
}
