package composition

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"

	businessintegration "github.com/domainry/domainry-runtime/runtime/application/integration"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaprojection "github.com/domainry/domainry-runtime/runtime/domain/appschema/projection"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

// applyManifestMetadata replaces the RuntimeServices-owned schema indexes. It is
// mutable composition state, not a Metadata application use case.
func (s *runtimeAssembly) applyManifestMetadata(templateID, templateVersion, name string, objects []definitionmodel.ObjectSchema, views []definitionmodel.ViewSchema, actions []definitionmodel.ActionSchema, workflows []definitionmodel.WorkflowSchema, automationRules []automationmodel.AutomationRuleSchema, dictionaries []appschemamodel.DictionarySchema, integrations integrationmodel.IntegrationSchema, reports []reportmodel.ReportSchema, entrypoints []definitionmodel.EntryPointSchema, skills []agentmodel.SkillSchema, agents []agentmodel.AgentSchema, profileBindings []profilebindingmodel.Binding) {
	objects = appschemaprojection.ApplicationSchemaEnrichObjectsWithFieldValueDomains(objects, dictionaries)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.templateID = strings.TrimSpace(templateID)
	s.templateVersion = strings.TrimSpace(templateVersion)
	s.name = strings.TrimSpace(name)
	s.schema = make(map[string]definitionmodel.ObjectSchema, len(objects))
	s.views = append([]definitionmodel.ViewSchema(nil), views...)
	s.actions = make(map[string]definitionmodel.ActionSchema, len(actions))
	s.workflows = make(map[string]definitionmodel.WorkflowSchema, len(workflows))
	s.automationRules = make(map[string]automationmodel.AutomationRuleSchema, len(automationRules))
	s.dictionaries = append([]appschemamodel.DictionarySchema(nil), dictionaries...)
	s.integrations = appschemaservice.CloneIntegrationSchema(integrations)
	if s.connectorRegistry == nil {
		s.connectorRegistry = businessintegration.NewConnectorRegistry(integrations)
	} else {
		s.connectorRegistry.ReplaceSchema(integrations)
	}
	s.reports = append([]reportmodel.ReportSchema(nil), reports...)
	s.reportObjects = recordservice.RecordReportObjectKeySet(reports)
	s.entrypoints = append([]definitionmodel.EntryPointSchema(nil), entrypoints...)
	s.skills = append([]agentmodel.SkillSchema(nil), skills...)
	s.agents = append([]agentmodel.AgentSchema(nil), agents...)
	s.identityProfileExtensions = append([]profilebindingmodel.Binding(nil), profileBindings...)
	if s.dictionaryRuntime != nil {
		s.dictionaryRuntime.Replace(dictionaries)
	}
	for _, object := range objects {
		if strings.TrimSpace(object.Key) != "" {
			s.schema[object.Key] = object
		}
	}
	for _, action := range actions {
		if strings.TrimSpace(action.Key) != "" {
			s.actions[action.Key] = action
		}
	}
	for _, workflow := range workflows {
		if strings.TrimSpace(workflow.Key) != "" {
			s.workflows[workflow.Key] = workflow
		}
	}
	for _, rule := range automationRules {
		if strings.TrimSpace(rule.Key) != "" {
			s.automationRules[rule.Key] = rule
		}
	}
}

func (s *runtimeAssembly) applyManifestAgentMetadata(tasks []agentmodel.AgentTaskDefinition, entrypoints []agentmodel.AgentEntrypointAssignment, principals []agentmodel.AgentServicePrincipalBinding) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agentTasks = append([]agentmodel.AgentTaskDefinition(nil), tasks...)
	s.agentEntrypoints = append([]agentmodel.AgentEntrypointAssignment(nil), entrypoints...)
	s.agentServicePrincipals = append([]agentmodel.AgentServicePrincipalBinding(nil), principals...)
}
