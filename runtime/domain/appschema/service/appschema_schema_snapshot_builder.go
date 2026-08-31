package service

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	reportmodel "github.com/domainry/domainry-report-sdk/model"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"sort"
)

type SchemaSnapshotState struct {
	TemplateID, TemplateVersion, Name string
	Objects                           []definitionmodel.ObjectSchema
	Actions                           []definitionmodel.ActionSchema
	Workflows                         []definitionmodel.WorkflowSchema
	AutomationRules                   []automationmodel.AutomationRuleSchema
	Dictionaries                      []appschemamodel.DictionarySchema
	Integrations                      integrationmodel.IntegrationSchema
	Reports                           []reportmodel.ReportSchema
	Skills                            []agentsdk.SkillSchema
	Agents                            []agentsdk.AgentSchema
	AgentTasks                        []agentsdk.AgentTaskDefinition
	AgentEntrypoints                  []agentsdk.AgentEntrypointAssignment
	AgentServicePrincipals            []agentsdk.AgentServicePrincipalBinding
	IdentityProfileExtensions         []profilebindingmodel.Binding
}

func BuildSchemaSnapshot(state SchemaSnapshotState) appschemamodel.ApplicationSchemaSnapshot {
	objects := append([]definitionmodel.ObjectSchema(nil), state.Objects...)
	actions := append([]definitionmodel.ActionSchema(nil), state.Actions...)
	workflows := append([]definitionmodel.WorkflowSchema(nil), state.Workflows...)
	automationRules := append([]automationmodel.AutomationRuleSchema(nil), state.AutomationRules...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	sort.Slice(actions, func(i, j int) bool { return actions[i].Key < actions[j].Key })
	sort.Slice(workflows, func(i, j int) bool { return workflows[i].Key < workflows[j].Key })
	sort.Slice(automationRules, func(i, j int) bool { return automationRules[i].Key < automationRules[j].Key })
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		TemplateID: state.TemplateID, TemplateVersion: state.TemplateVersion, Name: state.Name,
		Objects: objects, Actions: actions,
		GuardedWrites: GuardedWriteContracts(actions), Workflows: workflows, AutomationRules: automationRules,
		Dictionaries: append([]appschemamodel.DictionarySchema(nil), state.Dictionaries...), Integrations: CloneIntegrationSchema(state.Integrations),
		Reports: append([]reportmodel.ReportSchema(nil), state.Reports...),
		Skills:  append([]agentsdk.SkillSchema(nil), state.Skills...), Agents: append([]agentsdk.AgentSchema(nil), state.Agents...),
		AgentTasks: append([]agentsdk.AgentTaskDefinition(nil), state.AgentTasks...), AgentEntrypoints: append([]agentsdk.AgentEntrypointAssignment(nil), state.AgentEntrypoints...),
		AgentServicePrincipals:    append([]agentsdk.AgentServicePrincipalBinding(nil), state.AgentServicePrincipals...),
		IdentityProfileExtensions: append([]profilebindingmodel.Binding(nil), state.IdentityProfileExtensions...),
	}
	snapshot.SchemaHash = SchemaSnapshotHash(snapshot)
	snapshot.SnapshotVersion = snapshot.SchemaHash
	return snapshot
}
