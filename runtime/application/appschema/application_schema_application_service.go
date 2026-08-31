package appschema

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	reportmodel "github.com/domainry/domainry-report-sdk/model"

	"context"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"

	"sync"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

// ApplicationSchemaApplicationService owns Runtime-specific metadata
// validation and lifecycle orchestration. Definition, localization and
// dictionary reads are owned by the Metadata SDK Binding.
type ApplicationSchemaApplicationService struct {
	repository        appschemarepository.ApplicationSchemaRepository
	runtime           LifecycleRuntime
	workflows         WorkflowDefinitionInitializer
	audit             auditcontract.AuditEventFactory
	templateID        string
	version           string
	name              string
	records           recordrepository.RecordRepository
	integrations      integrationsdk.Management
	references        ApplicationSchemaReferenceGraphProvider
	auditAppender     ApplicationSchemaAuditAppender
	reloadObserversMu sync.RWMutex
	reloadObservers   []func(appschemamodel.ApplicationSchemaSnapshot)
}

func (s *ApplicationSchemaApplicationService) AddReloadObserver(observer func(appschemamodel.ApplicationSchemaSnapshot)) {
	if s == nil || observer == nil {
		return
	}
	s.reloadObserversMu.Lock()
	defer s.reloadObserversMu.Unlock()
	s.reloadObservers = append(s.reloadObservers, observer)
}

func (s *ApplicationSchemaApplicationService) notifyReloadObservers(snapshot appschemamodel.ApplicationSchemaSnapshot) {
	s.reloadObserversMu.RLock()
	observers := append([]func(appschemamodel.ApplicationSchemaSnapshot){}, s.reloadObservers...)
	s.reloadObserversMu.RUnlock()
	for _, observer := range observers {
		observer(snapshot)
	}
}

type ApplicationSchemaReferenceGraphProvider interface {
	Graph(context.Context, principalmodel.Principal) (changeplanmodel.ReferenceGraph, error)
}

type ApplicationSchemaAuditAppender func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)

type LifecycleRuntime interface {
	ApplyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []appschemamodel.DictionarySchema, connectormodel.IntegrationSchema, []reportmodel.ReportSchema, []agentsdk.SkillSchema, []agentsdk.AgentSchema, []profilebindingmodel.Binding)
	Schema() appschemamodel.ApplicationSchemaSnapshot
}

type agentLifecycleRuntime interface {
	ApplyManifestAgentMetadata([]agentsdk.AgentTaskDefinition, []agentsdk.AgentEntrypointAssignment, []agentsdk.AgentServicePrincipalBinding)
}

func applyManifestAgentMetadata(runtime LifecycleRuntime, manifest manifestmodel.ManifestSchema) {
	if target, ok := runtime.(agentLifecycleRuntime); ok {
		target.ApplyManifestAgentMetadata(manifest.AgentTasks, manifest.AgentEntrypoints, manifest.AgentServicePrincipals)
	}
}

type WorkflowDefinitionInitializer interface {
	InitializePublishedWorkflowDefinitions(context.Context, []definitionmodel.WorkflowSchema, principalmodel.SystemScope) error
}

type ApplicationSchemaDependencies struct {
	Repository    appschemarepository.ApplicationSchemaRepository
	Runtime       LifecycleRuntime
	Workflows     WorkflowDefinitionInitializer
	Audit         auditcontract.AuditEventFactory
	TemplateID    string
	Version       string
	Name          string
	Records       recordrepository.RecordRepository
	Integrations  integrationsdk.Management
	References    ApplicationSchemaReferenceGraphProvider
	AuditAppender ApplicationSchemaAuditAppender
}

func NewApplicationSchemaApplicationService(dependencies ApplicationSchemaDependencies) *ApplicationSchemaApplicationService {
	return &ApplicationSchemaApplicationService{
		repository: dependencies.Repository, runtime: dependencies.Runtime, workflows: dependencies.Workflows,
		audit: dependencies.Audit, templateID: dependencies.TemplateID,
		version: dependencies.Version, name: dependencies.Name, records: dependencies.Records,
		integrations: dependencies.Integrations, references: dependencies.References,
		auditAppender: dependencies.AuditAppender,
	}
}
