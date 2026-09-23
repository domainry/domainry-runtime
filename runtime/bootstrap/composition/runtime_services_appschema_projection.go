package composition

import (
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
)

// applyProjectMetadata builds Runtime's read-only application-schema indexes
// from the storage/security model plus frozen code registries. It is an
// in-memory projection only; no combined manifest is serialized or persisted.
func (s *runtimeAssembly) applyProjectMetadata(model projectmodel.RuntimeModel, actions []definitionmodel.ActionSchema, definitions runtimeext.ProjectDefinitions, integrations appschemamodel.IntegrationSchema) {
	reports := make([]reportmodel.ReportSchema, 0, len(definitions.Reports))
	for _, definition := range definitions.Reports {
		reports = append(reports, definition.Report)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.schemaGeneration++
	s.templateID = strings.TrimSpace(model.ProjectKey)
	s.templateVersion = strings.TrimSpace(model.ContentHash)
	s.name = strings.TrimSpace(model.ProjectName)
	s.timeZone = model.EffectiveTimeZone()
	s.schema = make(map[string]definitionmodel.ObjectSchema, len(model.Objects))
	s.actions = make(map[string]definitionmodel.ActionSchema, len(actions))
	s.workflows = make(map[string]definitionmodel.WorkflowSchema, len(definitions.Workflows))
	s.businessCalendars = append([]businesscalendarmodel.BusinessCalendarSchema(nil), definitions.BusinessCalendars...)
	s.automationRules = make(map[string]automationmodel.AutomationRuleSchema, len(definitions.AutomationRules))
	s.dictionaries = nil
	s.integrations = appschemaservice.CloneIntegrationSchema(integrations)
	if s.connectorRegistry == nil {
		s.connectorRegistry = newRuntimeConnectorCatalog(integrations)
	} else {
		s.connectorRegistry.ReplaceSchema(integrations)
	}
	s.reports = reports
	s.skills = append(s.skills[:0], definitions.AgentSkills...)
	s.agents = append(s.agents[:0], definitions.Agents...)
	s.agentTasks = append(s.agentTasks[:0], definitions.AgentTasks...)
	s.agentEntrypoints = append(s.agentEntrypoints[:0], definitions.AgentEntrypoints...)
	s.agentServicePrincipals = append(s.agentServicePrincipals[:0], definitions.AgentServicePrincipals...)
	s.identityProfileExtensions = append(s.identityProfileExtensions[:0], model.IdentityProfiles...)
	for _, object := range model.Objects {
		if key := strings.TrimSpace(object.Key); key != "" {
			s.schema[key] = object
		}
	}
	for _, action := range actions {
		if key := strings.TrimSpace(action.Key); key != "" {
			s.actions[key] = action
		}
	}
	for _, workflow := range definitions.Workflows {
		if key := strings.TrimSpace(workflow.Key); key != "" {
			s.workflows[key] = workflow
		}
	}
	for _, rule := range definitions.AutomationRules {
		if key := strings.TrimSpace(rule.Key); key != "" {
			s.automationRules[key] = rule
		}
	}
}

func (s *runtimeAssembly) businessCalendarSnapshot() []businesscalendarmodel.BusinessCalendarSchema {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]businesscalendarmodel.BusinessCalendarSchema(nil), s.businessCalendars...)
}
