package runtime

import (
	"context"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodule "github.com/domainry/domainry-report/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

// NewVerifiedProjectWithTopologyFactoriesAndDatabase selects Integration topology and retains Report Module by default.
func NewVerifiedProjectWithTopologyFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, release runtimehttp.RuntimeReleaseIdentity, evidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, store *persistence.RuntimeStore, agent ...agentsdk.Factory) *Runtime {
	return NewVerifiedProjectWithAllTopologyFactoriesAndDatabase(ctx, cfg, handlers, connectors, release, evidence, identity, notification, monitoring, scheduler, dataExchange, integration, reportmodule.NewFactory(), store, agent...)
}

// NewVerifiedProjectWithAllTopologyFactoriesAndDatabase lets the project host select Report Module or SaaS explicitly.
func NewVerifiedProjectWithAllTopologyFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, release runtimehttp.RuntimeReleaseIdentity, evidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, report reportsdk.Factory, store *persistence.RuntimeStore, agent ...agentsdk.Factory) *Runtime {
	var agentFactory agentsdk.Factory
	if len(agent) > 0 {
		agentFactory = agent[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, handlers, connectors, release, identity, notification, monitoring, scheduler, dataExchange, agentFactory, integration, report, store, evidence)
}
