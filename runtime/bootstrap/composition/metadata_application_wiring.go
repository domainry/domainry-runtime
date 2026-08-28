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

type metadataLifecycleRuntime interface {
	applyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ViewSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []metadatamodel.DictionarySchema, integrationmodel.IntegrationSchema, []reportmodel.ReportSchema, []definitionmodel.EntryPointSchema, []agentmodel.SkillSchema, []agentmodel.AgentSchema, []profilebindingmodel.Binding)
	applyManifestAgentMetadata([]agentmodel.AgentTaskDefinition, []agentmodel.AgentEntrypointAssignment, []agentmodel.AgentServicePrincipalBinding)
	Schema() metadatamodel.MetadataSchemaSnapshot
}

type metadataLifecycleRuntimeAdapter struct{ runtime metadataLifecycleRuntime }

var _ metadataapplication.LifecycleRuntime = metadataLifecycleRuntimeAdapter{}

func (a metadataLifecycleRuntimeAdapter) ApplyManifestMetadata(templateID, templateVersion, name string, objects []definitionmodel.ObjectSchema, views []definitionmodel.ViewSchema, actions []definitionmodel.ActionSchema, workflows []definitionmodel.WorkflowSchema, automationRules []automationmodel.AutomationRuleSchema, dictionaries []metadatamodel.DictionarySchema, integrations integrationmodel.IntegrationSchema, reports []reportmodel.ReportSchema, entrypoints []definitionmodel.EntryPointSchema, skills []agentmodel.SkillSchema, agents []agentmodel.AgentSchema, profileBindings []profilebindingmodel.Binding) {
	a.runtime.applyManifestMetadata(templateID, templateVersion, name, objects, views, actions, workflows, automationRules, dictionaries, integrations, reports, entrypoints, skills, agents, profileBindings)
}

func (a metadataLifecycleRuntimeAdapter) ApplyManifestAgentMetadata(tasks []agentmodel.AgentTaskDefinition, entrypoints []agentmodel.AgentEntrypointAssignment, principals []agentmodel.AgentServicePrincipalBinding) {
	a.runtime.applyManifestAgentMetadata(tasks, entrypoints, principals)
}

func (a metadataLifecycleRuntimeAdapter) Schema() metadatamodel.MetadataSchemaSnapshot {
	return a.runtime.Schema()
}

func assembleMetadataApplication(records *runtimeAssembly) *metadataapplication.MetadataApplicationService {
	if records == nil {
		return metadataapplication.NewMetadataApplicationService(metadataapplication.MetadataApplicationDependencies{})
	}
	if records.metadataApplicationService != nil {
		return records.metadataApplicationService
	}
	return metadataapplication.NewMetadataApplicationService(metadataapplication.MetadataApplicationDependencies{
		Repository: records.metadataRepo, Runtime: metadataLifecycleRuntimeAdapter{runtime: records},
		Workflows: records.Applications().Workflows, Dictionary: records.dictionaryRuntime,
		Audit: records.auditApplicationService, AuditAppender: records.auditApplicationService.AppendWithMetadata,
		TemplateID: records.templateID, Version: records.templateVersion, Name: records.name,
		Records: records.recordRepo, Integrations: records.integrationConfigRepo,
		References: records.businessReferences, ChangePlans: records.businessChangePlanRepo,
	})
}
