package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

// projectIntegrationSchema combines Integration-owned connector contracts
// with the project's frozen code-owned event mappings for transient Runtime
// validation and application-schema discovery. It is never persisted as a
// project model or manifest.
func projectIntegrationSchema(ctx context.Context, catalog integrationsdk.Catalog, mappings []integrationsdk.EventMappingRequirement) (appschemamodel.IntegrationSchema, error) {
	if catalog == nil {
		if len(mappings) == 0 {
			return appschemamodel.IntegrationSchema{}, nil
		}
		return appschemamodel.IntegrationSchema{}, fmt.Errorf("Integration Catalog is required")
	}
	definitions, err := catalog.ListConnectorDefinitions(ctx)
	if err != nil {
		return appschemamodel.IntegrationSchema{}, fmt.Errorf("list Integration connector definitions: %w", err)
	}
	result := appschemamodel.IntegrationSchema{EventMappings: integrationMappingProjections(mappings)}
	for _, definition := range definitions {
		if len(definition.Definition) == 0 || string(definition.Definition) == "null" {
			continue
		}
		var connector appschemamodel.ConnectorSchema
		if err := json.Unmarshal(definition.Definition, &connector); err != nil {
			return appschemamodel.IntegrationSchema{}, fmt.Errorf("decode Integration connector definition %q: %w", definition.Key, err)
		}
		if connector.Key == "" {
			connector.Key = definition.Key
		}
		if connector.Key != definition.Key {
			return appschemamodel.IntegrationSchema{}, fmt.Errorf("Integration connector definition %q projects key %q", definition.Key, connector.Key)
		}
		result.Connectors = append(result.Connectors, connector)
	}
	return result, nil
}

func integrationMappingProjections(values []integrationsdk.EventMappingRequirement) []appschemamodel.IntegrationEventMappingSchema {
	result := make([]appschemamodel.IntegrationEventMappingSchema, 0, len(values))
	for _, value := range values {
		payload := cloneProjectIntegrationPayload(value.Payload)
		if value.ConnectionKey != "" {
			if payload == nil {
				payload = map[string]any{}
			}
			payload["connection_key"] = value.ConnectionKey
		}
		result = append(result, appschemamodel.IntegrationEventMappingSchema{
			Key: value.Key, Provider: value.Provider, EventType: value.EventType, CommandPrefix: value.CommandPrefix, TargetType: value.TargetType,
			WorkflowKey: value.WorkflowKey, ObjectKey: value.ObjectKey, ObjectKeyPath: value.ObjectKeyPath, RecordID: value.RecordID, RecordIDPath: value.RecordIDPath,
			ActionKey: value.ActionKey, ActionKeyPath: value.ActionKeyPath, ActionInput: value.ActionInput, WorkflowInput: value.WorkflowInput,
			AgentID: value.AgentID, ConversationID: value.ConversationID, AgentTaskMode: value.AgentTaskMode, RelatedTaskID: value.RelatedTaskID,
			RelatedTaskIDPath: value.RelatedTaskIDPath, AgentInput: value.AgentInput, EventFields: value.EventFields,
			ExternalIdentity: value.ExternalIdentity, Payload: payload, Enabled: value.Enabled,
		})
	}
	return result
}

func cloneProjectIntegrationPayload(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
