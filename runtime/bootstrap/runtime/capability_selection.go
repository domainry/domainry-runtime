package runtime

import (
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

func ProjectSchemaCapabilities(model projectmodel.RuntimeModel, extensions *runtimeext.ProjectExtensionRegistry) persistence.RuntimeSchemaCapabilities {
	if extensions == nil {
		return persistence.RuntimeSchemaCapabilities{}
	}
	return projectSchemaCapabilities(model, extensions.ProjectDefinitions(), extensions.BusinessHandlerDescriptors())
}

func selectedRuntimeSchemaCapabilities(selected []persistence.RuntimeSchemaCapabilities) persistence.RuntimeSchemaCapabilities {
	if len(selected) == 0 {
		return persistence.FullRuntimeSchemaCapabilities()
	}
	return selected[0]
}

func (runtime *Runtime) effectiveSchemaCapabilities() persistence.RuntimeSchemaCapabilities {
	if runtime == nil || !runtime.schemaCapabilitiesSelected {
		return persistence.FullRuntimeSchemaCapabilities()
	}
	return runtime.schemaCapabilities
}

func projectSchemaCapabilities(model projectmodel.RuntimeModel, definitions runtimeext.ProjectDefinitions, handlers []runtimeext.HandlerDescriptor) persistence.RuntimeSchemaCapabilities {
	uploads := projectUsesUploads(model, definitions, handlers)
	return persistence.RuntimeSchemaCapabilities{
		Workflow:   len(definitions.Workflows) != 0,
		Automation: len(definitions.AutomationRules) != 0,
		Uploads:    uploads,
		Lifecycle:  uploads || projectUsesLifecycle(handlers),
	}
}

func projectUsesLifecycle(handlers []runtimeext.HandlerDescriptor) bool {
	for _, handler := range handlers {
		if handler.AccountErasure != nil {
			return true
		}
	}
	return false
}

func projectUsesUploads(model projectmodel.RuntimeModel, definitions runtimeext.ProjectDefinitions, handlers []runtimeext.HandlerDescriptor) bool {
	if projectDefinitionsUseAgent(definitions) {
		return true
	}
	for _, handler := range handlers {
		if len(handler.FileCapabilities) != 0 {
			return true
		}
	}
	for _, object := range model.Objects {
		for _, field := range object.Fields {
			switch strings.TrimSpace(field.Type) {
			case "file", "file_list":
				return true
			}
		}
		for _, resource := range object.PublicResources {
			if len(resource.Files) != 0 {
				return true
			}
		}
	}
	return false
}

func validateSelectedCapabilityFactories(definitions runtimeext.ProjectDefinitions, handlers []runtimeext.HandlerDescriptor, notificationFactory notificationsdk.Factory, reportFactory reportsdk.Factory, integrationFactory integrationsdk.Factory, hasConnectorProviders bool, schedulerFactory schedulersdk.Factory, agentFactory agentsdk.Factory) error {
	if projectDefinitionsUseNotification(definitions, handlers) && notificationFactory == nil {
		return fmt.Errorf("project Notification definitions require a Notification SDK Factory")
	}
	if projectDefinitionsUseReport(definitions) && reportFactory == nil {
		return fmt.Errorf("project Report definitions or targets require a Report SDK Factory")
	}
	if projectDefinitionsUseIntegration(definitions, handlers, hasConnectorProviders) && integrationFactory == nil {
		return fmt.Errorf("project connector or Integration definitions require an Integration SDK Factory")
	}
	if len(definitions.Schedules) != 0 && schedulerFactory == nil {
		return fmt.Errorf("project schedules require a Scheduler SDK Factory")
	}
	if projectDefinitionsUseAgent(definitions) && agentFactory == nil {
		return fmt.Errorf("project Agent definitions require an Agent SDK Factory")
	}
	return nil
}

func projectDefinitionsUseIntegration(definitions runtimeext.ProjectDefinitions, handlers []runtimeext.HandlerDescriptor, hasConnectorProviders bool) bool {
	if hasConnectorProviders || len(definitions.IntegrationMappings) != 0 {
		return true
	}
	for _, handler := range handlers {
		if len(handler.ConnectorCapabilities) != 0 {
			return true
		}
	}
	for _, schedule := range definitions.Schedules {
		if strings.TrimSpace(schedule.Target.Type) == "http" || strings.TrimSpace(schedule.Target.ConnectionKey) != "" {
			return true
		}
	}
	for _, rule := range definitions.AutomationRules {
		if strings.TrimSpace(rule.Trigger.Phase) == "after" {
			return true
		}
		for _, instruction := range rule.Instructions {
			if strings.TrimSpace(instruction.ConnectorKey) != "" || strings.TrimSpace(instruction.ConnectionKey) != "" {
				return true
			}
		}
	}
	for _, rule := range definitions.NotificationRules {
		for _, channel := range rule.Channels {
			if strings.TrimSpace(channel.ConnectorKey) != "" || strings.TrimSpace(channel.ConnectionKey) != "" {
				return true
			}
		}
	}
	return false
}

func projectDefinitionsUseReport(definitions runtimeext.ProjectDefinitions) bool {
	if len(definitions.Reports) != 0 {
		return true
	}
	for _, schedule := range definitions.Schedules {
		if strings.TrimSpace(schedule.Target.Owner) == "report_snapshot_refresh" {
			return true
		}
	}
	return false
}

func projectDefinitionsUseNotification(definitions runtimeext.ProjectDefinitions, handlers []runtimeext.HandlerDescriptor) bool {
	if len(definitions.NotificationTemplates) != 0 || len(definitions.NotificationEventTypes) != 0 || len(definitions.NotificationRules) != 0 {
		return true
	}
	for _, handler := range handlers {
		if len(handler.NotificationEventTypes) != 0 {
			return true
		}
	}
	for _, workflow := range definitions.Workflows {
		if workflow.Graph == nil {
			continue
		}
		for _, node := range workflow.Graph.Nodes {
			if node.Contract != nil && node.Contract.CC != nil {
				return true
			}
		}
	}
	for _, rule := range definitions.AutomationRules {
		mode := strings.TrimSpace(rule.Execution.ResultNotification)
		if mode != "" && mode != "none" {
			return true
		}
	}
	return false
}

func projectDefinitionsUseAgent(definitions runtimeext.ProjectDefinitions) bool {
	if len(definitions.AgentSkills) != 0 ||
		len(definitions.Agents) != 0 ||
		len(definitions.AgentTasks) != 0 ||
		len(definitions.AgentEntrypoints) != 0 ||
		len(definitions.AgentServicePrincipals) != 0 {
		return true
	}
	for _, workflow := range definitions.Workflows {
		if workflow.Graph == nil {
			continue
		}
		for _, node := range workflow.Graph.Nodes {
			if node.Contract != nil && node.Contract.AgentTask != nil {
				return true
			}
		}
	}
	return false
}
