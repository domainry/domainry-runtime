package appschema

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type upsertMetadataRuntime struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (*upsertMetadataRuntime) ApplyManifestMetadata(string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []automationmodel.AutomationRuleSchema, []appschemamodel.DictionarySchema, integrationmodel.IntegrationSchema, []reportmodel.ReportSchema, []agentsdk.SkillSchema, []agentsdk.AgentSchema, []profilebindingmodel.Binding) {
}

func (r *upsertMetadataRuntime) Schema() appschemamodel.ApplicationSchemaSnapshot { return r.snapshot }
