package service

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	reportmodel "github.com/domainry/domainry-report-sdk/model"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"sort"
)

type SchemaSnapshotState struct {
	ProjectKey, SchemaVersion, Name, TimeZone string
	Objects                                   []definitionmodel.ObjectSchema
	Actions                                   []definitionmodel.ActionSchema
	Workflows                                 []definitionmodel.WorkflowSchema
	BusinessCalendars                         []businesscalendarmodel.BusinessCalendarSchema
	AutomationRules                           []automationmodel.AutomationRuleSchema
	Dictionaries                              []appschemamodel.DictionarySchema
	Integrations                              connectormodel.IntegrationSchema
	Reports                                   []reportmodel.ReportSchema
	Skills                                    []agentsdk.SkillSchema
	Agents                                    []agentsdk.AgentSchema
	AgentTasks                                []agentsdk.AgentTaskDefinition
	AgentEntrypoints                          []agentsdk.AgentEntrypointAssignment
	AgentServicePrincipals                    []agentsdk.AgentServicePrincipalBinding
	IdentityProfileExtensions                 []profilebindingmodel.Binding
}

// ProjectSchemaObjects normalizes the current owner definitions without
// assembling unrelated application domains or computing a snapshot hash.
func ProjectSchemaObjects(ownerObjects []definitionmodel.ObjectSchema) []definitionmodel.ObjectSchema {
	objects := append([]definitionmodel.ObjectSchema(nil), ownerObjects...)
	for index := range objects {
		capabilities := definitionmodel.EffectiveObjectCapabilities(objects[index])
		objects[index].Capabilities = &capabilities
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Key < objects[j].Key })
	return objects
}

func BuildSchemaSnapshot(state SchemaSnapshotState) appschemamodel.ApplicationSchemaSnapshot {
	objects := ProjectSchemaObjects(state.Objects)
	actions := append([]definitionmodel.ActionSchema(nil), state.Actions...)
	workflows := append([]definitionmodel.WorkflowSchema(nil), state.Workflows...)
	automationRules := append([]automationmodel.AutomationRuleSchema(nil), state.AutomationRules...)
	sort.Slice(actions, func(i, j int) bool { return actions[i].Key < actions[j].Key })
	sort.Slice(workflows, func(i, j int) bool { return workflows[i].Key < workflows[j].Key })
	sort.Slice(automationRules, func(i, j int) bool { return automationRules[i].Key < automationRules[j].Key })
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		ProjectKey: state.ProjectKey, SchemaVersion: state.SchemaVersion, Name: state.Name, TimeZone: state.TimeZone,
		Objects: objects, Actions: actions,
		GuardedWrites: GuardedWriteContracts(actions), Workflows: workflows, AutomationRules: automationRules,
		BusinessCalendars: append([]businesscalendarmodel.BusinessCalendarSchema(nil), state.BusinessCalendars...),
		Dictionaries:      append([]appschemamodel.DictionarySchema(nil), state.Dictionaries...), Integrations: CloneIntegrationSchema(state.Integrations),
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
