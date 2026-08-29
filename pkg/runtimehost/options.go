// Package runtimehost owns the public process startup boundary for a
// statically composed Domainry project Runtime.
package runtimehost

import (
	"github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	partysdk "github.com/domainry/domainry-party-sdk"
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
	// NotificationFactory is selected by generated composition. Module builds
	// inject domainry-notification/module; SaaS builds inject the SDK Remote
	// Factory. Runtime never switches topology from environment at startup.
	NotificationFactory notificationsdk.Factory
	// PartyFactory selects the in-process Module or SaaS Remote Binding.
	PartyFactory     partysdk.Factory
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
