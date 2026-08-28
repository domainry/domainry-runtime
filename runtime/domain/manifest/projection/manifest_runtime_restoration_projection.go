package projection

import (
	"strings"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func MergeInstalledEnvelope(persisted, installed manifestmodel.ManifestSchema, publishedTemplates []notificationmodel.NotificationTemplate) manifestmodel.ManifestSchema {
	persisted.ManifestHash = installed.ManifestHash
	persisted.SourceBlueprintID = installed.SourceBlueprintID
	persisted.TargetAPIContractVersion = installed.TargetAPIContractVersion
	persisted.TargetAPIContractHash = installed.TargetAPIContractHash
	persisted.AuthoringContractVersion = installed.AuthoringContractVersion
	persisted.AuthoringContractHash = installed.AuthoringContractHash
	persisted.SourceIntentCoverage = installed.SourceIntentCoverage
	persisted.Description = installed.Description
	persisted.I18n = installed.I18n
	persisted.Roles = append([]manifestmodel.RoleSchema(nil), installed.Roles...)
	persisted.SeedRecords = append([]businessseedmodel.SeedRecordSchema(nil), installed.SeedRecords...)
	persisted.AutomationExecutionSeeds = append([]automationmodel.AutomationRuleExecution(nil), installed.AutomationExecutionSeeds...)
	persisted.BusinessLoops = append([]map[string]any(nil), installed.BusinessLoops...)
	persisted.StateMachines = append([]map[string]any(nil), installed.StateMachines...)
	persisted.ValidationPlan = append([]map[string]any(nil), installed.ValidationPlan...)
	persisted.Dictionaries = MergeDictionaries(persisted.Dictionaries, installed.Dictionaries)
	persisted.AutomationRules = MergeAutomationRules(persisted.AutomationRules, installed.AutomationRules)
	persisted.Workflows = MergeWorkflows(persisted.Workflows, installed.Workflows)
	persisted.NotificationTemplates = append([]notificationmodel.NotificationTemplate(nil), publishedTemplates...)
	persisted.NotificationEventTypes = append([]notificationmodel.NotificationEventType(nil), installed.NotificationEventTypes...)
	persisted.NotificationRules = append([]notificationmodel.NotificationRule(nil), installed.NotificationRules...)
	persisted = MergeConnectorValidationCatalog(persisted, installed.Integrations.Connectors)
	persisted.Integrations.Connections = append([]integrationmodel.ConnectionSchema(nil), installed.Integrations.Connections...)
	return persisted
}

func MergeDictionaries(existing, installed []metadatamodel.DictionarySchema) []metadatamodel.DictionarySchema {
	result := append([]metadatamodel.DictionarySchema(nil), existing...)
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

func MergeConnectorValidationCatalog(manifest manifestmodel.ManifestSchema, validationCatalog []integrationmodel.ConnectorSchema) manifestmodel.ManifestSchema {
	seen := make(map[string]bool, len(manifest.Integrations.Connectors))
	connectors := append([]integrationmodel.ConnectorSchema(nil), manifest.Integrations.Connectors...)
	for _, connector := range connectors {
		seen[connector.Key] = true
	}
	for _, connector := range validationCatalog {
		if !seen[connector.Key] {
			seen[connector.Key] = true
			connectors = append(connectors, connector)
		}
	}
	manifest.Integrations.Connectors = connectors
	return manifest
}
