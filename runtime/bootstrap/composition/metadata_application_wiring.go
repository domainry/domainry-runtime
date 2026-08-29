package composition

import (
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type applicationSchemaLifecycleRuntime interface {
	applyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ViewSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []metadatamodel.DictionarySchema, integrationmodel.IntegrationSchema, []reportmodel.ReportSchema, []definitionmodel.EntryPointSchema, []agentmodel.SkillSchema, []agentmodel.AgentSchema, []profilebindingmodel.Binding)
	applyManifestAgentMetadata([]agentmodel.AgentTaskDefinition, []agentmodel.AgentEntrypointAssignment, []agentmodel.AgentServicePrincipalBinding)
	Schema() metadatamodel.ApplicationSchemaSnapshot
}

type applicationSchemaLifecycleRuntimeAdapter struct {
	runtime applicationSchemaLifecycleRuntime
}

var _ metadataapplication.LifecycleRuntime = applicationSchemaLifecycleRuntimeAdapter{}

func (a applicationSchemaLifecycleRuntimeAdapter) ApplyManifestMetadata(templateID, templateVersion, name string, objects []definitionmodel.ObjectSchema, views []definitionmodel.ViewSchema, actions []definitionmodel.ActionSchema, workflows []definitionmodel.WorkflowSchema, automationRules []automationmodel.AutomationRuleSchema, dictionaries []metadatamodel.DictionarySchema, integrations integrationmodel.IntegrationSchema, reports []reportmodel.ReportSchema, entrypoints []definitionmodel.EntryPointSchema, skills []agentmodel.SkillSchema, agents []agentmodel.AgentSchema, profileBindings []profilebindingmodel.Binding) {
	a.runtime.applyManifestMetadata(templateID, templateVersion, name, objects, views, actions, workflows, automationRules, dictionaries, integrations, reports, entrypoints, skills, agents, profileBindings)
}

func (a applicationSchemaLifecycleRuntimeAdapter) ApplyManifestAgentMetadata(tasks []agentmodel.AgentTaskDefinition, entrypoints []agentmodel.AgentEntrypointAssignment, principals []agentmodel.AgentServicePrincipalBinding) {
	a.runtime.applyManifestAgentMetadata(tasks, entrypoints, principals)
}

func (a applicationSchemaLifecycleRuntimeAdapter) Schema() metadatamodel.ApplicationSchemaSnapshot {
	return a.runtime.Schema()
}

func assembleApplicationSchema(records *runtimeAssembly) *metadataapplication.ApplicationSchemaService {
	if records == nil {
		return metadataapplication.NewApplicationSchemaService(metadataapplication.ApplicationSchemaDependencies{})
	}
	if records.applicationSchemaService != nil {
		return records.applicationSchemaService
	}
	return metadataapplication.NewApplicationSchemaService(metadataapplication.ApplicationSchemaDependencies{
		Repository: records.metadataRepo, Runtime: applicationSchemaLifecycleRuntimeAdapter{runtime: records},
		Workflows: records.Applications().Workflows, Dictionary: records.dictionaryRuntime,
		Audit: records.auditApplicationService, AuditAppender: records.auditApplicationService.AppendWithMetadata,
		TemplateID: records.templateID, Version: records.templateVersion, Name: records.name,
		Records: records.recordRepo, Integrations: records.integrationConfigRepo,
		References: records.businessReferences, ChangePlans: records.businessChangePlanRepo,
	})
}
