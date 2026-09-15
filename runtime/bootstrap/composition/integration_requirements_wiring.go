package composition

import (
	"encoding/json"
	"fmt"
	"strings"

	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func IntegrationConnectionRequirements(connections []connectormodel.ConnectionSchema) []integrationsdk.ConnectionRequirement {
	result := make([]integrationsdk.ConnectionRequirement, 0, len(connections))
	for _, connection := range connections {
		if connection.Key == "" || connection.ConnectorKey == "" {
			continue
		}
		config, err := json.Marshal(connection.Config)
		if err != nil {
			panic(fmt.Errorf("encode manifest Integration connection %q: %w", connection.Key, err))
		}
		result = append(result, integrationsdk.ConnectionRequirement{Key: connection.Key, WorkspaceID: principalmodel.InstallationWorkspaceID, ConnectorKey: connection.ConnectorKey, ProviderKey: connection.ProviderKey, Name: connection.Name, Status: connection.Status, Config: config})
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
			ObjectKey: mapping.ObjectKey, ObjectKeyPath: mapping.ObjectKeyPath,
			RecordID: mapping.RecordID, RecordIDPath: mapping.RecordIDPath,
			ActionKey: mapping.ActionKey, ActionKeyPath: mapping.ActionKeyPath,
			ActionInput: cloneIntegrationPathBindings(mapping.ActionInput), WorkflowInput: cloneIntegrationPathBindings(mapping.WorkflowInput),
			AgentID: mapping.AgentID, ConversationID: mapping.ConversationID, AgentTaskMode: mapping.AgentTaskMode,
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
