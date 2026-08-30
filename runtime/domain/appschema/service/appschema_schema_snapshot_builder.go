package service

import (
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"sort"
)

type SchemaSnapshotState struct {
	TemplateID, TemplateVersion, Name string
	Objects                           []definitionmodel.ObjectSchema
	Views                             []definitionmodel.ViewSchema
	Actions                           []definitionmodel.ActionSchema
	Workflows                         []definitionmodel.WorkflowSchema
	AutomationRules                   []automationmodel.AutomationRuleSchema
	Dictionaries                      []appschemamodel.DictionarySchema
	Integrations                      integrationmodel.IntegrationSchema
	Reports                           []reportmodel.ReportSchema
	EntryPoints                       []definitionmodel.EntryPointSchema
	Skills                            []agentmodel.SkillSchema
	Agents                            []agentmodel.AgentSchema
	AgentTasks                        []agentmodel.AgentTaskDefinition
	AgentEntrypoints                  []agentmodel.AgentEntrypointAssignment
	AgentServicePrincipals            []agentmodel.AgentServicePrincipalBinding
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
		Objects: objects, Views: append([]definitionmodel.ViewSchema(nil), state.Views...), Actions: actions,
		GuardedWrites: GuardedWriteContracts(actions), Workflows: workflows, AutomationRules: automationRules,
		Dictionaries: append([]appschemamodel.DictionarySchema(nil), state.Dictionaries...), Integrations: CloneIntegrationSchema(state.Integrations),
		Reports: append([]reportmodel.ReportSchema(nil), state.Reports...), EntryPoints: append([]definitionmodel.EntryPointSchema(nil), state.EntryPoints...),
		Skills: append([]agentmodel.SkillSchema(nil), state.Skills...), Agents: append([]agentmodel.AgentSchema(nil), state.Agents...),
		AgentTasks: append([]agentmodel.AgentTaskDefinition(nil), state.AgentTasks...), AgentEntrypoints: append([]agentmodel.AgentEntrypointAssignment(nil), state.AgentEntrypoints...),
		AgentServicePrincipals:    append([]agentmodel.AgentServicePrincipalBinding(nil), state.AgentServicePrincipals...),
		IdentityProfileExtensions: append([]profilebindingmodel.Binding(nil), state.IdentityProfileExtensions...),
	}
	snapshot.SchemaHash = SchemaSnapshotHash(snapshot)
	snapshot.SnapshotVersion = snapshot.SchemaHash
	return snapshot
}
