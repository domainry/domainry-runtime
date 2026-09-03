package runtime

import (
	"context"
	"sync"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	auditsdk "github.com/domainry/domainry-audit-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"

	connector "github.com/domainry/domainry-connector-sdk"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/ratelimit"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	notificationfacade "github.com/domainry/domainry-runtime/runtime/application/notificationfacade"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	notificationpublication "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

// Runtime owns the process-level composition and lifecycle.
type Runtime struct {
	cfg                  config.Config
	templateID           string
	store                *persistence.RuntimeStore
	borrowedStore        bool
	records              *composition.RuntimeServices
	authorizationActions func() *actioncontract.Registry
	moduleBindings       runtimeModuleBindingInventory
	identityBinding      identitysdk.Binding
	identityDirectory    identitysdk.Directory
	identityPrincipals   identitysdk.PrincipalResolver
	principalCache       identityprincipal.Cache
	integrationMode      integrationsdk.DeploymentMode
	integrationBinding   integrationsdk.Binding
	integrationWorkers   integrationsdk.LocalWorkers
	dataExchangeBinding  dataexchangesdk.Binding
	lifecycleBinding     lifecyclesdk.Binding
	manifest             manifestmodel.ManifestSchema
	recordRepo           recordrepository.RecordRepository
	rateLimiter          ratelimit.Limiter
	notificationHTTP     *notificationfacade.NotificationApplicationService
	notificationBinding  notificationsdk.Binding
	monitoringBinding    monitoringsdk.Binding
	schedulerBinding     schedulersdk.Binding
	agentBinding         agentsdk.Binding
	auditBinding         auditsdk.Binding
	metadataBinding      metadatasdk.Binding
	reportBinding        reportsdk.Binding
	notificationWorkers  notificationsdk.LocalWorkers
	notificationRelay    *notificationpublication.Relay
	worker               workerplatform.Dependencies
	api                  *runtimehttp.HTTPRouter
	businessHandlers     *runtimeext.BusinessHandlerRegistry
	connectorProviders   *connector.Registry
	releaseIdentity      runtimehttp.RuntimeReleaseIdentity
	releaseCohort        *deploymentapplication.DeploymentRuntimeReleaseCohortApplicationService
	releaseLease         deploymentmodel.RuntimeReleaseCohortLease
	releaseAdmission     *deploymentapplication.RuntimeReleaseAdmission
	releaseIntegrity     *deploymentapplication.RuntimeReleaseIntegrity
	releaseMu            sync.Mutex

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
