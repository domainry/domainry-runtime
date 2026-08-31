package composition

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

type applicationSchemaLifecycleRuntime interface {
	applyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []appschemamodel.DictionarySchema, connectormodel.IntegrationSchema, []reportmodel.ReportSchema, []agentsdk.SkillSchema, []agentsdk.AgentSchema, []profilebindingmodel.Binding)
	applyManifestAgentMetadata([]agentsdk.AgentTaskDefinition, []agentsdk.AgentEntrypointAssignment, []agentsdk.AgentServicePrincipalBinding)
	Schema() appschemamodel.ApplicationSchemaSnapshot
}

type applicationSchemaLifecycleRuntimeAdapter struct {
	runtime applicationSchemaLifecycleRuntime
}

var _ appschemaapplication.LifecycleRuntime = applicationSchemaLifecycleRuntimeAdapter{}

func (a applicationSchemaLifecycleRuntimeAdapter) ApplyManifestMetadata(templateID, templateVersion, name string, objects []definitionmodel.ObjectSchema, actions []definitionmodel.ActionSchema, workflows []definitionmodel.WorkflowSchema, automationRules []automationmodel.AutomationRuleSchema, dictionaries []appschemamodel.DictionarySchema, integrations connectormodel.IntegrationSchema, reports []reportmodel.ReportSchema, skills []agentsdk.SkillSchema, agents []agentsdk.AgentSchema, profileBindings []profilebindingmodel.Binding) {
	a.runtime.applyManifestMetadata(templateID, templateVersion, name, objects, actions, workflows, automationRules, dictionaries, integrations, reports, skills, agents, profileBindings)
}

func (a applicationSchemaLifecycleRuntimeAdapter) ApplyManifestAgentMetadata(tasks []agentsdk.AgentTaskDefinition, entrypoints []agentsdk.AgentEntrypointAssignment, principals []agentsdk.AgentServicePrincipalBinding) {
	a.runtime.applyManifestAgentMetadata(tasks, entrypoints, principals)
}

func (a applicationSchemaLifecycleRuntimeAdapter) Schema() appschemamodel.ApplicationSchemaSnapshot {
	return a.runtime.Schema()
}

func assembleApplicationSchema(records *runtimeAssembly) *appschemaapplication.ApplicationSchemaApplicationService {
	if records == nil {
		return appschemaapplication.NewApplicationSchemaApplicationService(appschemaapplication.ApplicationSchemaDependencies{})
	}
	if records.applicationSchemaService != nil {
		return records.applicationSchemaService
	}
	return appschemaapplication.NewApplicationSchemaApplicationService(appschemaapplication.ApplicationSchemaDependencies{
		Repository: records.applicationSchemaRepo, Runtime: applicationSchemaLifecycleRuntimeAdapter{runtime: records},
		Workflows: records.Applications().Workflows,
		Audit:     records.auditApplicationService, AuditAppender: records.auditApplicationService.AppendWithMetadata,
		TemplateID: records.templateID, Version: records.templateVersion, Name: records.name,
		Records:      records.recordRepo,
		Integrations: records.integrationOwnerManagement,
		References:   records.businessReferences,
	})
}
