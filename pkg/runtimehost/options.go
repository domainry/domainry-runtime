// Package runtimehost owns the public process startup boundary for a
// statically composed Domainry project Runtime.
package runtimehost

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

// Options is the complete project-owned input to Runtime process composition.
// Project code supplies generated identities and extensions, never Runtime
// internal services or infrastructure objects.
type Options struct {
	Identity BuildIdentity
	// IdentityFactory is selected by the generated project composition root.
	// A module build injects domainry-identity/module.Factory; a SaaS build
	// injects domainry-identity-sdk/remote.Factory. Runtime never selects or
	// switches the deployment topology.
	IdentityFactory identitysdk.Factory
	// IntegrationFactory selects the source-owned in-process Module or SaaS
	// Binding. Runtime owns only the durable outbound handoff.
	IntegrationFactory integrationsdk.Factory
	// ReportFactory selects the Report Module or SaaS Binding. Report-owned
	// definitions and snapshot state are never assembled inside Runtime.
	ReportFactory reportsdk.Factory
	// NotificationFactory is selected by generated composition. Module builds
	// inject domainry-notification/module; SaaS builds inject the SDK Remote
	// Factory. Runtime never switches topology from environment at startup.
	NotificationFactory notificationsdk.Factory
	// MonitoringFactory selects the Monitoring Module or SaaS Binding.
	MonitoringFactory monitoringsdk.Factory
	// SchedulerFactory selects the in-process clock Module or SaaS Binding.
	SchedulerFactory schedulersdk.Factory
	// DataExchangeFactory selects the in-process large-file engine or its SaaS
	// Remote binding. Generated composition owns this topology decision.
	DataExchangeFactory dataexchangesdk.Factory
	// AgentFactory selects the in-process domainry-agent Module or its SaaS
	// Remote Binding. Runtime receives the topology only through this factory.
	AgentFactory     agentsdk.Factory
	BusinessHandlers BusinessHandlerFactory
	Connectors       connector.ProviderSetFactory
	// ConnectorProcesses is an explicit host policy for optional Provider
	// subprocesses. The zero value denies every executable.
	ConnectorProcesses ConnectorProcessPolicy
	// ProjectConfigFile and ProjectI18nDir point to optional Git-owned
	// extension files relative to the backend working directory.
	ProjectConfigFile string
	ProjectI18nDir    string
}

type ConnectorProcessPolicy struct {
	AllowedExecutables             []string
	AllowedWorkingDirectoryRoots   []string
	AllowInheritedWorkingDirectory bool
}
