package composition

import (
	"strings"

	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func IntegrationConnectionRequirements(values []integrationsdk.ConnectionRequirement) []integrationsdk.ConnectionRequirement {
	result := make([]integrationsdk.ConnectionRequirement, len(values))
	for index, value := range values {
		value.WorkspaceID = principalmodel.InstallationWorkspaceID
		value.Config = append([]byte(nil), value.Config...)
		result[index] = value
	}
	return result
}

func IntegrationEventMappingRequirements(mappings []connectormodel.IntegrationEventMappingSchema) []integrationsdk.EventMappingRequirement {
	result := make([]integrationsdk.EventMappingRequirement, 0, len(mappings))
	for _, mapping := range mappings {
		if strings.TrimSpace(mapping.Key) == "" || strings.TrimSpace(mapping.Provider) == "" {
			continue
		}
		eventFields := make([]integrationsdk.EventFieldRequirement, 0, len(mapping.EventFields))
		for _, field := range mapping.EventFields {
			eventFields = append(eventFields, integrationsdk.EventFieldRequirement{
				Path: field.Path, Type: field.Type, Options: append([]string(nil), field.Options...), Required: field.Required,
			})
		}
		connectionKey := ""
		if value, ok := mapping.Payload["connection_key"].(string); ok {
			connectionKey = strings.TrimSpace(value)
		}
		payload := make(map[string]any, len(mapping.Payload))
		for key, value := range mapping.Payload {
			if key != "connection_key" {
				payload[key] = value
			}
		}
		result = append(result, integrationsdk.EventMappingRequirement{
			Key: mapping.Key, WorkspaceID: principalmodel.InstallationWorkspaceID, Provider: mapping.Provider,
			ConnectionKey: connectionKey, EventType: mapping.EventType, CommandPrefix: mapping.CommandPrefix,
			TargetType: mapping.TargetType, WorkflowKey: mapping.WorkflowKey,
			AutomationRuleKey: mapping.AutomationRuleKey,
			ObjectKey:         mapping.ObjectKey, ObjectKeyPath: mapping.ObjectKeyPath,
			RecordID: mapping.RecordID, RecordIDPath: mapping.RecordIDPath,
			ActionKey: mapping.ActionKey, ActionKeyPath: mapping.ActionKeyPath,
			ActionInput: cloneIntegrationPathBindings(mapping.ActionInput), WorkflowInput: cloneIntegrationPathBindings(mapping.WorkflowInput),
			AutomationInput: cloneIntegrationPathBindings(mapping.AutomationInput),
			AgentID:         mapping.AgentID, ConversationID: mapping.ConversationID, AgentTaskMode: mapping.AgentTaskMode,
			RelatedTaskID: mapping.RelatedTaskID, RelatedTaskIDPath: mapping.RelatedTaskIDPath,
			AgentInput:  cloneIntegrationPathBindings(mapping.AgentInput),
			EventFields: eventFields,
			ExternalIdentity: integrationsdk.ExternalIdentityMappingRequirement{
				Provider: mapping.ExternalIdentity.Provider, SubjectPath: mapping.ExternalIdentity.SubjectPath,
				SubjectType: mapping.ExternalIdentity.SubjectType, NamePath: mapping.ExternalIdentity.NamePath,
				OnUnmapped: mapping.ExternalIdentity.OnUnmapped,
			},
			Payload: payload, Enabled: mapping.Enabled,
		})
	}
	return result
}

func cloneIntegrationPathBindings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
