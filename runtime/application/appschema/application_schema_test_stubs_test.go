package appschema

import (
	agentsdk "github.com/domainry/domainry-agent-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

type localizedLifecycleRuntimeStub struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (s localizedLifecycleRuntimeStub) Schema() appschemamodel.ApplicationSchemaSnapshot {
	return s.snapshot
}

func (localizedLifecycleRuntimeStub) ApplyManifestMetadata(string, string, string, string, []definitionmodel.ObjectSchema, []definitionmodel.ActionSchema, []definitionmodel.WorkflowSchema, []businesscalendarmodel.BusinessCalendarSchema, []automationmodel.AutomationRuleSchema, []appschemamodel.DictionarySchema, appschemamodel.IntegrationSchema, []reportmodel.ReportSchema, []agentsdk.SkillSchema, []agentsdk.AgentSchema, []profilebindingmodel.Binding) {
}
