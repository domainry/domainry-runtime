// Package runtimehost owns the public process startup boundary for a
// statically composed Domainry project Runtime.
package runtimehost

import (
	"context"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	auditsdk "github.com/domainry/domainry-audit-sdk"
	"github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/codingruntime"
	"github.com/domainry/domainry-runtime/pkg/runtimeengine"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	toolsdk "github.com/domainry/domainry-tools-sdk"
)

// Options is the complete project-owned input to Runtime process composition.
// Project code supplies build identities and extensions, never Runtime
// internal services or infrastructure objects.
type Options struct {
	Identity BuildIdentity
	// ModelFile is the only handwritten Domainry model input. Runtime decodes
	// it strictly and fails startup when it is absent or invalid.
	ModelFile string
	// IdentityFactory is selected by the project composition root.
	// A module build injects domainry-identity/module.Factory; a SaaS build
	// injects domainry-identity-sdk/remote.Factory. Runtime never selects or
	// switches the deployment topology.
	IdentityFactory identitysdk.Factory
	// AuditFactory is selected by the project composition root. Runtime never
	// imports or constructs the source Audit implementation.
	AuditFactory auditsdk.Factory
	// MetadataFactory is selected by the project composition root. The embedded
	// implementation receives Runtime's host database and migration registrar;
	// Runtime never selects the implementation itself.
	MetadataFactory metadatasdk.Factory
	// LifecycleFactory is selected by the project composition root. It is opened
	// only when the project model enables Lifecycle capabilities.
	LifecycleFactory lifecyclesdk.Factory
	// IntegrationFactory optionally selects the source-owned in-process Module
	// or SaaS Binding. Runtime owns only the durable outbound handoff. Nil leaves
	// Integration storage, HTTP and workers uninstalled and is rejected when
	// project definitions declare connector/integration behavior.
	IntegrationFactory integrationsdk.Factory
	// ReportFactory optionally selects the Report Module or SaaS Binding.
	// Report-owned definitions and snapshot state are never assembled inside
	// Runtime. Nil leaves Report module storage, HTTP and application ports
	// uninstalled and is rejected when the project declares Report references.
	ReportFactory reportsdk.Factory
	// AnalysisTableSourceFactory is assembled by project code from a Knowledge
	// or data-service public adapter. Runtime supplies the resolved instance ID
	// and consumes only the Report SDK owner port.
	AnalysisTableSourceFactory func(string) (reportmodulehost.AnalysisTableSource, error)
	// NotificationFactory is optionally selected by project composition. Module
	// builds inject domainry-notification/module; SaaS builds inject the SDK
	// Remote Factory. Nil leaves Notification storage, HTTP and workers
	// uninstalled and is rejected when the project declares Notification data.
	NotificationFactory notificationsdk.Factory
	// MonitoringFactory optionally selects the Monitoring Module or SaaS
	// Binding. Nil leaves the module uninstalled.
	MonitoringFactory monitoringsdk.Factory
	// SchedulerFactory optionally selects the in-process clock Module or SaaS
	// Binding. Nil leaves Scheduler storage, HTTP and workers uninstalled.
	SchedulerFactory schedulersdk.Factory
	// DataExchangeFactory optionally selects the in-process large-file engine or
	// its SaaS Remote binding. Nil leaves import/export jobs, their routes,
	// workers, lifecycle owner, and storage uninstalled.
	DataExchangeFactory dataexchangesdk.Factory
	// AgentFactory optionally selects the in-process domainry-agent Module or
	// its SaaS Remote Binding. Nil is valid when the project declares no Agent
	// definitions or conversation capability.
	AgentFactory agentsdk.Factory
	// ConversationToolsFactory is selected by the project composition root.
	// Runtime supplies only SDK ports and never imports Tools adapters.
	ConversationToolsFactory toolsdk.ConversationToolFactory
	ProjectExtensions        ProjectExtensionFactory
	// ProjectHTTP mounts a project-owned router below /api/. Runtime injects a
	// governed in-process Engine and keeps authentication and workspace policy
	// outside the project transport implementation.
	ProjectHTTP runtimeengine.HTTPFactory
	// DevelopmentData enables one-shot generated sample records after the
	// project model has materialized. Runtime accepts it only in development or
	// demo environments and only while every eligible business object is empty.
	// No sample row is described in model.json or SQL.
	DevelopmentData *DevelopmentDataOptions
	// DevelopmentIdentity enables explicitly authored evaluation users while
	// creating the first embedded Workspace. Runtime accepts it only in
	// development, dev, demo, or test environments and provisions the users in
	// the same transaction as the Workspace. Production startup rejects it.
	DevelopmentIdentity *DevelopmentIdentityOptions
	Connectors          connector.ProviderSetFactory
	// ConnectorProcesses is an explicit host policy for optional Provider
	// subprocesses. The zero value denies every executable.
	ConnectorProcesses ConnectorProcessPolicy
	// AgentCodingWorkspace explicitly enables the local coding execution world.
	// Nil leaves filesystem, terminal, process and LSP tools unpublished.
	AgentCodingWorkspace *codingruntime.Options
	// BlobStore and FileScanner are deployment-owned infrastructure adapters.
	// They do not participate in project extension identity or receive business
	// repositories. Nil selects Runtime's local filesystem and built-in scanner.
	BlobStore   runtimefile.BlobStore
	FileScanner runtimefile.FileScanner
	// InitialWorkspaceCredentialDelivery is the process-local, one-shot sink
	// used only while creating the first Workspace. It never becomes a Runtime
	// HTTP, project model, audit, receipt, or logging surface.
	InitialWorkspaceCredentialDelivery InitialWorkspaceCredentialDelivery
	// InstallationAdministratorCredentialDelivery is a startup-only seam for
	// private acceptance environments. Production normally selects the
	// create-only file sink through explicit process configuration.
	InstallationAdministratorCredentialDelivery InstallationAdministratorCredentialDelivery
	// ProjectConfigFile and ProjectI18nDir point to Git-owned extensions.
	ProjectConfigFile string
	ProjectI18nDir    string
}

// DevelopmentDataOptions is process policy, not project metadata. Seed makes
// generated values reproducible in tests; zero selects a fresh random
// default. RecordsPerObject defaults to three and is capped by Runtime.
type DevelopmentDataOptions struct {
	Seed             int64
	RecordsPerObject int
}

// DevelopmentIdentityOptions is startup policy for disposable evaluation
// environments. Passwords remain on the in-process bootstrap boundary and
// are never serialized through Runtime HTTP or project metadata.
type DevelopmentIdentityOptions struct {
	Organizations []DevelopmentIdentityOrganization
	Actors        []DevelopmentIdentityActor
}

type DevelopmentIdentityOrganization struct {
	ID   string
	Code string
	Name string
}

type DevelopmentIdentityActor struct {
	ID              string
	LoginID         string
	Name            string
	RoleKey         string
	OrganizationID  string
	ManagerUserID   string
	InitialPassword string
}

type InitialWorkspaceCredential struct {
	WorkspaceID        string `json:"-"`
	CanonicalCode      string `json:"-"`
	LoginID            string `json:"-"`
	InitialPassword    string `json:"-"`
	MustChangePassword bool   `json:"-"`
}

type InitialWorkspaceCredentialDeliveryAcknowledgment struct{ Accepted bool }

type InitialWorkspaceCredentialDelivery interface {
	DeliverInitialWorkspaceCredential(context.Context, InitialWorkspaceCredential) (InitialWorkspaceCredentialDeliveryAcknowledgment, error)
}

type InstallationAdministratorCredential struct {
	CanonicalWorkspaceCode string `json:"-"`
	LoginID                string `json:"-"`
	InitialPassword        string `json:"-"`
	MustChangePassword     bool   `json:"-"`
}

type InstallationAdministratorCredentialDeliveryAcknowledgment struct{ Accepted bool }

type InstallationAdministratorCredentialDelivery interface {
	DeliverInstallationAdministratorCredential(context.Context, InstallationAdministratorCredential) (InstallationAdministratorCredentialDeliveryAcknowledgment, error)
}

type ConnectorProcessPolicy struct {
	AllowedExecutables             []string
	AllowedWorkingDirectoryRoots   []string
	AllowInheritedWorkingDirectory bool
}
