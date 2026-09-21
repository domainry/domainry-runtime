package projection

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"strings"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func MergeInstalledEnvelope(persisted, installed manifestmodel.ManifestSchema, publishedTemplates []notificationmodel.NotificationTemplate) manifestmodel.ManifestSchema {
	persisted.ManifestHash = installed.ManifestHash
	persisted.SourceBlueprintID = installed.SourceBlueprintID
	persisted.TargetAPIContractVersion = installed.TargetAPIContractVersion
	persisted.TargetAPIContractHash = installed.TargetAPIContractHash
	persisted.AuthoringContractVersion = installed.AuthoringContractVersion
	persisted.AuthoringContractHash = installed.AuthoringContractHash
	persisted.GeneratedDomainSDK = installed.GeneratedDomainSDK
	persisted.SourceIntentCoverage = installed.SourceIntentCoverage
	persisted.Description = installed.Description
	persisted.I18n = installed.I18n
	persisted.Roles = append([]manifestmodel.RoleSchema(nil), installed.Roles...)
	persisted.InitialWorkspaceAdministratorRole = installed.InitialWorkspaceAdministratorRole
	persisted.WorkspaceProvisioning = append([]manifestmodel.WorkspaceProvisionProjection(nil), installed.WorkspaceProvisioning...)
	persisted.SeedRecords = append([]businessseedmodel.SeedRecordSchema(nil), installed.SeedRecords...)
	persisted.AutomationExecutionSeeds = append([]automationmodel.AutomationRuleExecution(nil), installed.AutomationExecutionSeeds...)
	persisted.BusinessLoops = append([]map[string]any(nil), installed.BusinessLoops...)
	persisted.StateMachines = append([]map[string]any(nil), installed.StateMachines...)
	persisted.ValidationPlan = append([]map[string]any(nil), installed.ValidationPlan...)
	persisted.Dictionaries = MergeDictionaries(persisted.Dictionaries, installed.Dictionaries)
	persisted.AutomationRules = MergeAutomationRules(persisted.AutomationRules, installed.AutomationRules)
	persisted.Workflows = MergeWorkflows(persisted.Workflows, installed.Workflows)
	persisted.BusinessCalendars = MergeBusinessCalendars(persisted.BusinessCalendars, installed.BusinessCalendars)
	// These catalogs are source-owned outside Metadata's current projection.
	// Preserve the installed envelope used to synchronize their modules; an
	// empty Metadata load must never erase them before Runtime composition.
	persisted.SchedulerDefinitions = append([]map[string]any(nil), installed.SchedulerDefinitions...)
	persisted.Reports = append(persisted.Reports[:0:0], installed.Reports...)
	persisted.OperationStateExamples = append(persisted.OperationStateExamples[:0:0], installed.OperationStateExamples...)
	persisted.SensitiveFieldPolicies = append(persisted.SensitiveFieldPolicies[:0:0], installed.SensitiveFieldPolicies...)
	persisted.ReportExportControls = append(persisted.ReportExportControls[:0:0], installed.ReportExportControls...)
	persisted.Skills = append(persisted.Skills[:0:0], installed.Skills...)
	persisted.Agents = append(persisted.Agents[:0:0], installed.Agents...)
	persisted.AgentTasks = append(persisted.AgentTasks[:0:0], installed.AgentTasks...)
	persisted.AgentEntrypoints = append(persisted.AgentEntrypoints[:0:0], installed.AgentEntrypoints...)
	persisted.AgentServicePrincipals = append(persisted.AgentServicePrincipals[:0:0], installed.AgentServicePrincipals...)
	persisted.NotificationTemplates = append([]notificationmodel.NotificationTemplate(nil), publishedTemplates...)
	persisted.NotificationEventTypes = append([]notificationmodel.NotificationEventType(nil), installed.NotificationEventTypes...)
	persisted.NotificationRules = append([]notificationmodel.NotificationRule(nil), installed.NotificationRules...)
	// Connector definitions are projected transiently from Integration's
	// Connectors-owned catalog after restoration; Metadata never persists them.
	persisted.Integrations.Connectors = append([]connectormodel.ConnectorSchema(nil), installed.Integrations.Connectors...)
	persisted.Integrations.Connections = append([]connectormodel.ConnectionSchema(nil), installed.Integrations.Connections...)
	return persisted
}

func MergeBusinessCalendars(existing, installed []businesscalendarmodel.BusinessCalendarSchema) []businesscalendarmodel.BusinessCalendarSchema {
	result := append([]businesscalendarmodel.BusinessCalendarSchema(nil), existing...)
	seen := map[string]bool{}
	for _, value := range result {
		seen[strings.TrimSpace(value.Key)] = true
	}
	for _, value := range installed {
		if key := strings.TrimSpace(value.Key); key != "" && !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

func MergeDictionaries(existing, installed []appschemamodel.DictionarySchema) []appschemamodel.DictionarySchema {
	result := append([]appschemamodel.DictionarySchema(nil), existing...)
	seen := map[string]bool{}
	for _, value := range result {
		seen[strings.TrimSpace(value.Key)] = true
	}
	for _, value := range installed {
		if key := strings.TrimSpace(value.Key); key != "" && !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

func MergeAutomationRules(existing, installed []automationmodel.AutomationRuleSchema) []automationmodel.AutomationRuleSchema {
	result := append([]automationmodel.AutomationRuleSchema(nil), existing...)
	seen := map[string]bool{}
	for _, value := range result {
		seen[strings.TrimSpace(value.Key)] = true
	}
	for _, value := range installed {
		if key := strings.TrimSpace(value.Key); key != "" && !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

func MergeWorkflows(existing, installed []definitionmodel.WorkflowSchema) []definitionmodel.WorkflowSchema {
	result := append([]definitionmodel.WorkflowSchema(nil), existing...)
	seen := map[string]bool{}
	for _, value := range result {
		seen[strings.TrimSpace(value.Key)] = true
	}
	for _, value := range installed {
		if key := strings.TrimSpace(value.Key); key != "" && !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}
