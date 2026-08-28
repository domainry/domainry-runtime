package integration

import (
	"fmt"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"strings"
)

const eventRoutingConnectionKey = "_integration_routing_connection_key"

type EventMappingExecutionPlan struct {
	Mapping          integrationmodel.IntegrationEventMappingSchema
	TargetType       string
	ExternalIdentity integrationmodel.IntegrationExternalIdentityResolveRequest
	Payload          map[string]any
	WorkflowKey      string
	ObjectKey        string
	RecordID         string
	ActionKey        string
}

// PlanEventMappingExecution owns mapping selection, target normalization and
// target-specific required-field validation. Composition only executes the
// selected Workflow, Action, or Record port.
func (s *IntegrationApplicationService) PlanEventMappingExecution(event integrationmodel.IntegrationEvent) (EventMappingExecutionPlan, bool, error) {
	mapping, ok := s.EventMappingForEvent(event)
	if !ok {
		return EventMappingExecutionPlan{}, false, nil
	}
	if issues := integrationcontract.IntegrationValidateEventPayload(mapping, event.Payload); len(issues) > 0 {
		return EventMappingExecutionPlan{}, true, badRequest("backend.integration.event_mapping.event_contract_invalid")
	}
	plan := EventMappingExecutionPlan{
		Mapping: mapping, TargetType: strings.TrimSpace(mapping.TargetType),
		ExternalIdentity: ExternalIdentityFromEventMapping(mapping, event),
		Payload:          EventMappingPayload(mapping, event),
	}
	switch plan.TargetType {
	case "workflow":
		plan.WorkflowKey = strings.TrimSpace(mapping.WorkflowKey)
		if plan.WorkflowKey == "" {
			return EventMappingExecutionPlan{}, true, badRequest("backend.integration.event_mapping.missing_workflow")
		}
	case "action":
		if strings.TrimSpace(mapping.ObjectKeyPath) != "" || strings.TrimSpace(mapping.ActionKeyPath) != "" {
			return EventMappingExecutionPlan{}, true, badRequest("backend.integration.event_mapping.dynamic_target_unsupported")
		}
		plan.ObjectKey = strings.TrimSpace(mapping.ObjectKey)
		plan.RecordID = MappingValue(mapping.RecordID, event.Payload, mapping.RecordIDPath)
		plan.ActionKey = strings.TrimSpace(mapping.ActionKey)
		if plan.ObjectKey == "" || plan.RecordID == "" || plan.ActionKey == "" {
			return EventMappingExecutionPlan{}, true, badRequest("backend.integration.event_mapping.missing_action_target")
		}
	case "owner_task":
	default:
		return EventMappingExecutionPlan{}, true, badRequest("backend.integration.event_mapping.invalid_target")
	}
	return plan, true, nil
}

func (s *IntegrationApplicationService) EventMappingForEvent(event integrationmodel.IntegrationEvent) (integrationmodel.IntegrationEventMappingSchema, bool) {
	provider, eventType := strings.TrimSpace(event.Provider), strings.TrimSpace(event.EventType)
	command := PayloadPathString(event.Payload, "command")
	for _, mapping := range s.registry.Schema().EventMappings {
		if strings.TrimSpace(mapping.Key) == "" || strings.TrimSpace(mapping.Provider) != provider {
			continue
		}
		if expected := strings.TrimSpace(mapping.EventType); expected != "" && expected != eventType {
			continue
		}
		if prefix := strings.TrimSpace(mapping.CommandPrefix); prefix != "" && !strings.HasPrefix(command, prefix) {
			continue
		}
		if connectionKey := eventMappingConnectionKey(mapping); connectionKey != "" && connectionKey != eventRoutingConnection(event.Payload) {
			continue
		}
		return mapping, true
	}
	return integrationmodel.IntegrationEventMappingSchema{}, false
}

func ExternalIdentityFromEventMapping(mapping integrationmodel.IntegrationEventMappingSchema, event integrationmodel.IntegrationEvent) integrationmodel.IntegrationExternalIdentityResolveRequest {
	provider := strings.TrimSpace(mapping.ExternalIdentity.Provider)
	if provider == "" {
		provider = strings.TrimSpace(mapping.Provider)
	}
	if provider == "" {
		provider = strings.TrimSpace(event.Provider)
	}
	onUnmapped := valueOrDefault(strings.TrimSpace(mapping.ExternalIdentity.OnUnmapped), "reject")
	return integrationmodel.IntegrationExternalIdentityResolveRequest{
		Provider: provider, ExternalSubject: PayloadPathString(event.Payload, mapping.ExternalIdentity.SubjectPath),
		ExternalSubjectType: strings.TrimSpace(mapping.ExternalIdentity.SubjectType),
		ExternalName:        PayloadPathString(event.Payload, mapping.ExternalIdentity.NamePath),
		OnUnmapped:          onUnmapped,
	}
}

func EventMappingPayload(mapping integrationmodel.IntegrationEventMappingSchema, event integrationmodel.IntegrationEvent) map[string]any {
	bindings := mapping.ActionInput
	if mapping.TargetType == "workflow" {
		bindings = mapping.WorkflowInput
	}
	if mapping.TargetType == "action" || mapping.TargetType == "workflow" {
		payload := make(map[string]any, len(bindings)+len(mapping.Payload)+5)
		for inputKey, sourcePath := range bindings {
			if value, ok := PayloadPathValue(event.Payload, sourcePath); ok {
				payload[inputKey] = value
			}
		}
		for key, value := range mapping.Payload {
			if key == "connection_key" {
				continue
			}
			payload[key] = value
		}
		if mapping.TargetType == "workflow" {
			payload["integration_event_id"], payload["integration_provider"] = event.ID, event.Provider
			payload["integration_event_type"], payload["integration_external_id"] = event.EventType, event.ExternalID
			payload["integration_mapping_key"] = mapping.Key
		}
		return payload
	}
	payload := cloneMap(event.Payload)
	if payload == nil {
		payload = map[string]any{}
	}
	delete(payload, eventRoutingConnectionKey)
	for key, value := range mapping.Payload {
		if key == "connection_key" {
			continue
		}
		payload[key] = value
	}
	payload["integration_event_id"], payload["integration_provider"] = event.ID, event.Provider
	payload["integration_event_type"], payload["integration_external_id"] = event.EventType, event.ExternalID
	payload["integration_mapping_key"] = mapping.Key
	return payload
}

func eventMappingConnectionKey(mapping integrationmodel.IntegrationEventMappingSchema) string {
	if mapping.Payload == nil {
		return ""
	}
	value, _ := mapping.Payload["connection_key"].(string)
	return strings.TrimSpace(value)
}

func eventRoutingConnection(payload map[string]any) string {
	if value, _ := payload[eventRoutingConnectionKey].(string); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return PayloadPathString(payload, "_integration_context.connection_key")
}

func MappingValue(static string, payload map[string]any, path string) string {
	if value := strings.TrimSpace(static); value != "" {
		return value
	}
	return PayloadPathString(payload, path)
}

func PayloadPathString(payload map[string]any, path string) string {
	value, ok := PayloadPathValue(payload, path)
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func PayloadPathValue(payload map[string]any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	var current any = payload
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok := object[part]
		if !ok || value == nil {
			return nil, false
		}
		current = value
	}
	return current, true
}
