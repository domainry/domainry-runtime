package composition

import (
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type applicationSchemaLifecycleRuntime interface {
	applyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ViewSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []appschemamodel.DictionarySchema, integrationmodel.IntegrationSchema, []reportmodel.ReportSchema, []definitionmodel.EntryPointSchema, []agentmodel.SkillSchema, []agentmodel.AgentSchema, []profilebindingmodel.Binding)
	applyManifestAgentMetadata([]agentmodel.AgentTaskDefinition, []agentmodel.AgentEntrypointAssignment, []agentmodel.AgentServicePrincipalBinding)
	Schema() appschemamodel.ApplicationSchemaSnapshot
}

type applicationSchemaLifecycleRuntimeAdapter struct {
	runtime applicationSchemaLifecycleRuntime
}

var _ appschemaapplication.LifecycleRuntime = applicationSchemaLifecycleRuntimeAdapter{}

func (a applicationSchemaLifecycleRuntimeAdapter) ApplyManifestMetadata(templateID, templateVersion, name string, objects []definitionmodel.ObjectSchema, views []definitionmodel.ViewSchema, actions []definitionmodel.ActionSchema, workflows []definitionmodel.WorkflowSchema, automationRules []automationmodel.AutomationRuleSchema, dictionaries []appschemamodel.DictionarySchema, integrations integrationmodel.IntegrationSchema, reports []reportmodel.ReportSchema, entrypoints []definitionmodel.EntryPointSchema, skills []agentmodel.SkillSchema, agents []agentmodel.AgentSchema, profileBindings []profilebindingmodel.Binding) {
	a.runtime.applyManifestMetadata(templateID, templateVersion, name, objects, views, actions, workflows, automationRules, dictionaries, integrations, reports, entrypoints, skills, agents, profileBindings)
}

func (a applicationSchemaLifecycleRuntimeAdapter) ApplyManifestAgentMetadata(tasks []agentmodel.AgentTaskDefinition, entrypoints []agentmodel.AgentEntrypointAssignment, principals []agentmodel.AgentServicePrincipalBinding) {
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
		Workflows: records.Applications().Workflows, Dictionary: records.dictionaryRuntime,
		Audit: records.auditApplicationService, AuditAppender: records.auditApplicationService.AppendWithMetadata,
		TemplateID: records.templateID, Version: records.templateVersion, Name: records.name,
		Records: records.recordRepo, Integrations: records.integrationConfigRepo,
		References: records.businessReferences, ChangePlans: records.businessChangePlanRepo,
	})
}
