package runtimeext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	notificationcontract "github.com/domainry/domainry-notification-sdk/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	businesscalendarpolicy "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/policy"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

var projectDefinitionKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
var publicResourceKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var (
	ErrProjectDefinitionInvalid   = fmt.Errorf("project definition is invalid")
	ErrProjectDefinitionDuplicate = fmt.Errorf("project definition is already registered")
)

const (
	ProjectDefinitionReport                = "report"
	ProjectDefinitionPublicResource        = "public_resource"
	ProjectDefinitionWorkflow              = "workflow"
	ProjectDefinitionBusinessCalendar      = "business_calendar"
	ProjectDefinitionSchedule              = "schedule"
	ProjectDefinitionAutomation            = "automation"
	ProjectDefinitionNotificationTemplate  = "notification_template"
	ProjectDefinitionNotificationEventType = "notification_event_type"
	ProjectDefinitionNotificationRule      = "notification_rule"
	ProjectDefinitionIntegrationMapping    = "integration_mapping"
	ProjectDefinitionAgentSkill            = "agent_skill"
	ProjectDefinitionAgent                 = "agent"
	ProjectDefinitionAgentTask             = "agent_task"
	ProjectDefinitionAgentEntrypoint       = "agent_entrypoint"
	ProjectDefinitionAgentPrincipal        = "agent_service_principal"
)

type frozenProjectDefinitions struct {
	reports                map[string]ReportDefinition
	publicResources        map[string]PublicResourceDefinition
	workflows              map[string]definitionmodel.WorkflowSchema
	businessCalendars      map[string]businesscalendarmodel.BusinessCalendarSchema
	schedules              map[string]schedulersdk.Definition
	automationRules        map[string]automationmodel.AutomationRuleSchema
	notificationTemplates  map[string]notificationcontract.NotificationTemplate
	notificationEventTypes map[string]notificationcontract.NotificationEventType
	notificationRules      map[string]notificationcontract.NotificationRule
	integrationMappings    map[string]integrationsdk.EventMappingRequirement
	agentSkills            map[string]agentsdk.SkillSchema
	agents                 map[string]agentsdk.AgentSchema
	agentTasks             map[string]agentsdk.AgentTaskDefinition
	agentEntrypoints       map[string]agentsdk.AgentEntrypointAssignment
	agentPrincipals        map[string]agentsdk.AgentServicePrincipalBinding
	identities             map[string]ProjectDefinitionIdentity
}

func newFrozenProjectDefinitions() frozenProjectDefinitions {
	return frozenProjectDefinitions{
		reports: map[string]ReportDefinition{}, publicResources: map[string]PublicResourceDefinition{}, workflows: map[string]definitionmodel.WorkflowSchema{},
		businessCalendars: map[string]businesscalendarmodel.BusinessCalendarSchema{}, schedules: map[string]schedulersdk.Definition{},
		automationRules: map[string]automationmodel.AutomationRuleSchema{}, notificationTemplates: map[string]notificationcontract.NotificationTemplate{},
		notificationEventTypes: map[string]notificationcontract.NotificationEventType{}, notificationRules: map[string]notificationcontract.NotificationRule{},
		integrationMappings: map[string]integrationsdk.EventMappingRequirement{}, agentSkills: map[string]agentsdk.SkillSchema{}, agents: map[string]agentsdk.AgentSchema{},
		agentTasks: map[string]agentsdk.AgentTaskDefinition{}, agentEntrypoints: map[string]agentsdk.AgentEntrypointAssignment{},
		agentPrincipals: map[string]agentsdk.AgentServicePrincipalBinding{}, identities: map[string]ProjectDefinitionIdentity{},
	}
}

func validateProjectDefinitions(input ProjectDefinitions) (frozenProjectDefinitions, error) {
	result := newFrozenProjectDefinitions()
	register := func(kind, key string, value any) error {
		key = strings.TrimSpace(key)
		if !projectDefinitionKeyPattern.MatchString(key) {
			return fmt.Errorf("%w: %s key %q", ErrProjectDefinitionInvalid, kind, key)
		}
		identityKey := kind + "\x00" + key
		if _, exists := result.identities[identityKey]; exists {
			return fmt.Errorf("%w: %s %s", ErrProjectDefinitionDuplicate, kind, key)
		}
		hash, err := definitionHash(value)
		if err != nil {
			return fmt.Errorf("%w: hash %s %s: %v", ErrProjectDefinitionInvalid, kind, key, err)
		}
		result.identities[identityKey] = ProjectDefinitionIdentity{Kind: kind, Key: key, SHA256: hash}
		return nil
	}
	for _, value := range input.Reports {
		key := strings.TrimSpace(value.Report.Key)
		if value.Report.ObjectSQLV1 == nil {
			return result, fmt.Errorf("%w: report %s requires object_sql_v1", ErrProjectDefinitionInvalid, key)
		}
		if err := register(ProjectDefinitionReport, key, value); err != nil {
			return result, err
		}
		result.reports[key] = cloneDefinition(value)
	}
	for _, value := range input.PublicResources {
		value.ObjectKey = strings.TrimSpace(value.ObjectKey)
		value.Resource.Key = strings.TrimSpace(value.Resource.Key)
		if value.ObjectKey == "" || !publicResourceKeyPattern.MatchString(value.Resource.Key) || strings.TrimSpace(value.Resource.AccessKeyField) == "" || strings.TrimSpace(value.Resource.StateField) == "" || strings.TrimSpace(value.Resource.ActiveState) == "" || len(value.Resource.Fields) == 0 {
			return result, fmt.Errorf("%w: public resource %s requires object, access key, state gate and fields", ErrProjectDefinitionInvalid, value.Resource.Key)
		}
		if err := register(ProjectDefinitionPublicResource, value.Resource.Key, value); err != nil {
			return result, err
		}
		result.publicResources[value.Resource.Key] = cloneDefinition(value)
	}
	for _, value := range input.Workflows {
		key := strings.TrimSpace(value.Key)
		value.Key = key
		if strings.TrimSpace(value.Name) == "" {
			return result, fmt.Errorf("%w: workflow %s requires name", ErrProjectDefinitionInvalid, key)
		}
		if err := register(ProjectDefinitionWorkflow, key, value); err != nil {
			return result, err
		}
		result.workflows[key] = cloneDefinition(value)
	}
	for _, value := range input.BusinessCalendars {
		key := strings.TrimSpace(value.Key)
		value.Key, value.Revision = key, ""
		revision, err := definitionHash(value)
		if err != nil {
			return result, err
		}
		value.Revision = revision
		if err := businesscalendarpolicy.Validate(value); err != nil {
			return result, fmt.Errorf("%w: business calendar %s: %v", ErrProjectDefinitionInvalid, key, err)
		}
		if err := register(ProjectDefinitionBusinessCalendar, key, value); err != nil {
			return result, err
		}
		result.businessCalendars[key] = cloneDefinition(value)
	}
	for _, value := range input.Schedules {
		value = value.Normalize()
		key := strings.TrimSpace(value.Key)
		value.Key, value.Revision = key, ""
		revision, err := definitionHash(value)
		if err != nil {
			return result, err
		}
		value.Revision = revision
		if err := value.Validate(); err != nil {
			return result, fmt.Errorf("%w: schedule %s: %v", ErrProjectDefinitionInvalid, key, err)
		}
		if err := register(ProjectDefinitionSchedule, key, value); err != nil {
			return result, err
		}
		result.schedules[key] = cloneDefinition(value)
	}
	for _, value := range input.AutomationRules {
		key := strings.TrimSpace(value.Key)
		value.Key = key
		if strings.TrimSpace(value.ObjectKey) == "" || len(value.Instructions) == 0 {
			return result, fmt.Errorf("%w: automation %s requires object and instructions", ErrProjectDefinitionInvalid, key)
		}
		if err := register(ProjectDefinitionAutomation, key, value); err != nil {
			return result, err
		}
		result.automationRules[key] = cloneDefinition(value)
	}
	for _, value := range input.NotificationTemplates {
		key := strings.TrimSpace(value.Key)
		value.Key, value.ContentHash = key, ""
		value.ContentHash = notificationcontract.NotificationTemplateContentHash(value)
		if err := register(ProjectDefinitionNotificationTemplate, key, value); err != nil {
			return result, err
		}
		result.notificationTemplates[key] = cloneDefinition(value)
	}
	if err := notificationcontract.ValidateEventTypes(input.NotificationEventTypes, input.NotificationRules); err != nil {
		return result, fmt.Errorf("%w: notification catalog: %v", ErrProjectDefinitionInvalid, err)
	}
	for _, value := range input.NotificationEventTypes {
		key := strings.TrimSpace(value.Key)
		value.Key = key
		if err := register(ProjectDefinitionNotificationEventType, key, value); err != nil {
			return result, err
		}
		result.notificationEventTypes[key] = cloneDefinition(value)
	}
	for _, value := range input.NotificationRules {
		key := strings.TrimSpace(value.EventTypeKey)
		value.EventTypeKey = key
		if err := register(ProjectDefinitionNotificationRule, key, value); err != nil {
			return result, err
		}
		result.notificationRules[key] = cloneDefinition(value)
	}
	for _, value := range input.IntegrationMappings {
		key := strings.TrimSpace(value.Key)
		value.Key = key
		if err := value.Validate(); err != nil {
			return result, fmt.Errorf("%w: integration mapping %s: %v", ErrProjectDefinitionInvalid, key, err)
		}
		if err := register(ProjectDefinitionIntegrationMapping, key, value); err != nil {
			return result, err
		}
		result.integrationMappings[key] = cloneDefinition(value)
	}
	for _, value := range input.AgentSkills {
		key := strings.TrimSpace(value.Key)
		value.Key = key
		if err := register(ProjectDefinitionAgentSkill, key, value); err != nil {
			return result, err
		}
		result.agentSkills[key] = cloneDefinition(value)
	}
	for _, value := range input.Agents {
		key := strings.TrimSpace(value.Key)
		value.Key = key
		if err := register(ProjectDefinitionAgent, key, value); err != nil {
			return result, err
		}
		result.agents[key] = cloneDefinition(value)
	}
	for _, value := range input.AgentTasks {
		key := strings.TrimSpace(value.Key)
		value.Key = key
		if err := register(ProjectDefinitionAgentTask, key, value); err != nil {
			return result, err
		}
		result.agentTasks[key] = cloneDefinition(value)
	}
	for _, value := range input.AgentEntrypoints {
		key := strings.TrimSpace(value.Key)
		value.Key = key
		if err := register(ProjectDefinitionAgentEntrypoint, key, value); err != nil {
			return result, err
		}
		result.agentEntrypoints[key] = cloneDefinition(value)
	}
	for _, value := range input.AgentServicePrincipals {
		key := strings.TrimSpace(value.Key)
		value.Key = key
		if err := register(ProjectDefinitionAgentPrincipal, key, value); err != nil {
			return result, err
		}
		result.agentPrincipals[key] = cloneDefinition(value)
	}
	return result, nil
}

func definitionHash(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func cloneDefinition[T any](value T) T {
	payload, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var result T
	if json.Unmarshal(payload, &result) != nil {
		return value
	}
	return result
}

func (d frozenProjectDefinitions) rejectDuplicates(candidate frozenProjectDefinitions) error {
	for key, identity := range candidate.identities {
		if _, exists := d.identities[key]; exists {
			return fmt.Errorf("%w: %s %s", ErrProjectDefinitionDuplicate, identity.Kind, identity.Key)
		}
	}
	return nil
}

func (d *frozenProjectDefinitions) merge(candidate frozenProjectDefinitions) {
	mergeMap(d.reports, candidate.reports)
	mergeMap(d.publicResources, candidate.publicResources)
	mergeMap(d.workflows, candidate.workflows)
	mergeMap(d.businessCalendars, candidate.businessCalendars)
	mergeMap(d.schedules, candidate.schedules)
	mergeMap(d.automationRules, candidate.automationRules)
	mergeMap(d.notificationTemplates, candidate.notificationTemplates)
	mergeMap(d.notificationEventTypes, candidate.notificationEventTypes)
	mergeMap(d.notificationRules, candidate.notificationRules)
	mergeMap(d.integrationMappings, candidate.integrationMappings)
	mergeMap(d.agentSkills, candidate.agentSkills)
	mergeMap(d.agents, candidate.agents)
	mergeMap(d.agentTasks, candidate.agentTasks)
	mergeMap(d.agentEntrypoints, candidate.agentEntrypoints)
	mergeMap(d.agentPrincipals, candidate.agentPrincipals)
	mergeMap(d.identities, candidate.identities)
}

func mergeMap[T any](target, source map[string]T) {
	for key, value := range source {
		target[key] = value
	}
}

func (d frozenProjectDefinitions) identityList() []ProjectDefinitionIdentity {
	result := make([]ProjectDefinitionIdentity, 0, len(d.identities))
	for _, identity := range d.identities {
		result = append(result, identity)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Kind+"\x00"+result[i].Key < result[j].Kind+"\x00"+result[j].Key })
	return result
}

func (d frozenProjectDefinitions) snapshot() ProjectDefinitions {
	return ProjectDefinitions{
		Reports: values(d.reports), PublicResources: values(d.publicResources), Workflows: values(d.workflows), BusinessCalendars: values(d.businessCalendars),
		Schedules: values(d.schedules), AutomationRules: values(d.automationRules), NotificationTemplates: values(d.notificationTemplates),
		NotificationEventTypes: values(d.notificationEventTypes), NotificationRules: values(d.notificationRules), IntegrationMappings: values(d.integrationMappings),
		AgentSkills: values(d.agentSkills), Agents: values(d.agents), AgentTasks: values(d.agentTasks), AgentEntrypoints: values(d.agentEntrypoints),
		AgentServicePrincipals: values(d.agentPrincipals),
	}
}

func values[T any](source map[string]T) []T {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]T, 0, len(keys))
	for _, key := range keys {
		result = append(result, cloneDefinition(source[key]))
	}
	return result
}

// ProjectDefinitions returns a detached, stable-key ordered snapshot.
func (r *ProjectExtensionRegistry) ProjectDefinitions() ProjectDefinitions {
	if r == nil {
		return ProjectDefinitions{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.definitions.snapshot()
}

func (r *ProjectExtensionRegistry) ProjectDefinitionIdentities() []ProjectDefinitionIdentity {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.definitions.identityList()
}

// ReportDefinition returns one immutable code-owned report contract.
func (r *ProjectExtensionRegistry) ReportDefinition(key string) (ReportDefinition, bool) {
	if r == nil {
		return ReportDefinition{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, found := r.definitions.reports[strings.TrimSpace(key)]
	return cloneDefinition(value), found
}
