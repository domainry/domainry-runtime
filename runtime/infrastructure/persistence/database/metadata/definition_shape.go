package metadata

import (
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	preferencevalidation "github.com/domainry/domainry-runtime/runtime/domain/preference/validation"

	rulesetvalidation "github.com/domainry/domainry-runtime/runtime/domain/ruleset/validation"

	"context"
	"encoding/json"
	"fmt"

	schedulervalidation "github.com/domainry/domainry-runtime/runtime/domain/scheduler/validation"

	"strings"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

type metadataDefinitionPayloadShape struct {
	Key       string
	ObjectKey string
	Name      string
	Payload   any
}

func metadataDefinitionTable(resourceType string) (string, error) {
	switch strings.TrimSpace(resourceType) {
	case "object":
		return "object_definitions", nil
	case "field":
		return "field_definitions", nil
	case "validation":
		return "validation_definitions", nil
	case "view":
		return "view_definitions", nil
	case "action":
		return "action_definitions", nil
	case "workflow":
		return "workflow_definitions", nil
	case "scheduler":
		return "scheduler_definitions", nil
	case "automation_rule":
		return "automation_rule_definitions", nil
	case "preference":
		return "preference_definitions", nil
	case "rule_set":
		return "rule_set_definitions", nil
	case "dictionary":
		return "dictionary_definitions", nil
	case "connector":
		return "connector_definitions", nil
	case "integration_event_mapping":
		return "integration_event_mapping_definitions", nil
	case "report":
		return "report_definitions", nil
	case "operation_state_example":
		return "operation_state_example_definitions", nil
	case "sensitive_field_policy":
		return "sensitive_field_policy_definitions", nil
	case "report_export_control":
		return "report_export_control_definitions", nil
	case "identity_profile_binding":
		return "identity_profile_binding_definitions", nil
	case "surface":
		return "surface_definitions", nil
	case "component":
		return "component_definitions", nil
	case "entrypoint":
		return "entrypoint_definitions", nil
	case "skill":
		return "skill_definitions", nil
	case "agent":
		return "agent_definitions", nil
	default:
		return "", fmt.Errorf("unsupported metadata resource type %q", resourceType)
	}
}

func metadataDefinitionShape(ctx context.Context, resourceType string, resourceKey string, req metadatamodel.MetadataDefinitionUpsertRequest) (metadataDefinitionPayloadShape, error) {
	switch strings.TrimSpace(resourceType) {
	case "object":
		var payload definitionmodel.ObjectSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		payload.Fields = nil
		payload.Validations = nil
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: key, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("object", key)
	case "field":
		var payload definitionmodel.FieldSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		if strings.TrimSpace(payload.Key) == "" {
			return metadataDefinitionPayloadShape{}, fmt.Errorf("metadata.field.missingKey")
		}
		if strings.TrimSpace(payload.Type) == "" {
			return metadataDefinitionPayloadShape{}, fmt.Errorf("metadata.field.missingType")
		}
		if strings.TrimSpace(payload.Name) == "" && strings.TrimSpace(req.Name) == "" {
			return metadataDefinitionPayloadShape{}, fmt.Errorf("metadata.field.missingName")
		}
		objectKey := valueOrFirstNonEmpty(req.ObjectKey, metadataFieldObjectKey(payload))
		key := valueOrFirstNonEmpty(resourceKey, metadataJoinedKey(objectKey, payload.Key))
		if payload.Config == nil {
			payload.Config = map[string]any{}
		}
		payload.Config["_definition_object_key"] = objectKey
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: objectKey, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("field", key)
	case "validation":
		var payload definitionmodel.ValidationSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		objectKey := valueOrFirstNonEmpty(req.ObjectKey, payload.ObjectKey)
		payload.ObjectKey = objectKey
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: objectKey, Name: valueOrFirstNonEmpty(req.Name, payload.Message), Payload: payload}, metadataDefinitionKeyError("validation", key)
	case "view":
		var payload definitionmodel.ViewSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		objectKey := valueOrFirstNonEmpty(req.ObjectKey, payload.ObjectKey)
		payload.ObjectKey = objectKey
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: objectKey, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("view", key)
	case "action":
		var payload definitionmodel.ActionSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		objectKey := valueOrFirstNonEmpty(req.ObjectKey, payload.ObjectKey)
		if strings.TrimSpace(objectKey) == "" {
			return metadataDefinitionPayloadShape{}, fmt.Errorf("metadata.action.missingObjectKey")
		}
		if strings.TrimSpace(payload.Kind) == "" {
			return metadataDefinitionPayloadShape{}, fmt.Errorf("metadata.action.missingKind")
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		payload.ObjectKey = objectKey
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: objectKey, Name: valueOrFirstNonEmpty(req.Name, payload.Label), Payload: payload}, metadataDefinitionKeyError("action", key)
	case "automation_rule":
		var payload automationmodel.AutomationRuleSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		objectKey := valueOrFirstNonEmpty(req.ObjectKey, payload.ObjectKey)
		payload.ObjectKey = objectKey
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: objectKey, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("automation_rule", key)
	case "workflow":
		var payload definitionmodel.WorkflowSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: metadataMapString(payload.Trigger, "object_key", "object"), Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("workflow", key)
	case "scheduler":
		var payload map[string]any
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		if payload == nil {
			return metadataDefinitionPayloadShape{}, fmt.Errorf("metadata.scheduler.invalidPayload")
		}
		key := valueOrFirstNonEmpty(resourceKey, metadataMapString(payload, "key"))
		payload["key"] = key
		if err := schedulervalidation.SchedulerValidateDefinitionContract(ctx, payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		return metadataDefinitionPayloadShape{Key: key, Name: valueOrFirstNonEmpty(req.Name, metadataMapString(payload, "name"), key), Payload: payload}, metadataDefinitionKeyError("scheduler", key)
	case "preference":
		payload, err := preferencevalidation.DecodeWorkspacePreferenceDefinition(resourceKey, req.Payload)
		if err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		return metadataDefinitionPayloadShape{Key: payload.Key, Name: payload.Name, Payload: payload}, metadataDefinitionKeyError("preference", payload.Key)
	case "rule_set":
		payload, err := rulesetvalidation.DecodeRuleSetDefinition(resourceKey, req.Payload)
		if err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		return metadataDefinitionPayloadShape{Key: payload.Key, Name: payload.Name, Payload: payload}, metadataDefinitionKeyError("rule_set", payload.Key)
	case "dictionary":
		var payload metadatamodel.DictionarySchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		return metadataDefinitionPayloadShape{Key: key, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("dictionary", key)
	case "connector":
		var payload integrationmodel.ConnectorSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		if strings.TrimSpace(payload.Type) == "" {
			return metadataDefinitionPayloadShape{}, fmt.Errorf("metadata.connector.missingType")
		}
		if strings.TrimSpace(payload.Provider) == "" {
			return metadataDefinitionPayloadShape{}, fmt.Errorf("metadata.connector.missingProvider")
		}
		return metadataDefinitionPayloadShape{Key: key, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("connector", key)
	case "integration_event_mapping":
		var payload integrationmodel.IntegrationEventMappingSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		objectKey := valueOrFirstNonEmpty(req.ObjectKey, payload.ObjectKey)
		payload.ObjectKey = objectKey
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: objectKey, Name: valueOrFirstNonEmpty(req.Name, payload.Provider), Payload: payload}, metadataDefinitionKeyError("integration_event_mapping", key)
	case "report":
		var payload reportmodel.ReportSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		return metadataDefinitionPayloadShape{Key: key, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("report", key)
	case "operation_state_example":
		var payload reportmodel.ReportOperationStateExampleSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		objectKey := valueOrFirstNonEmpty(req.ObjectKey, payload.ObjectKey)
		payload.ObjectKey = objectKey
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: objectKey, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("operation_state_example", key)
	case "sensitive_field_policy":
		var payload reportmodel.ReportSensitiveFieldPolicySchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		objectKey := valueOrFirstNonEmpty(req.ObjectKey, payload.ObjectKey)
		payload.ObjectKey = objectKey
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: objectKey, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("sensitive_field_policy", key)
	case "report_export_control":
		var payload reportmodel.ReportExportControlSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		objectKey := valueOrFirstNonEmpty(req.ObjectKey, payload.ReportKey)
		payload.ReportKey = objectKey
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: objectKey, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("report_export_control", key)
	case "identity_profile_binding":
		var payload profilebindingmodel.Binding
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.ObjectKey)
		payload.ObjectKey = key
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: key, Name: valueOrFirstNonEmpty(req.Name, payload.BusinessIdentity.Key, key), Payload: payload}, metadataDefinitionKeyError("identity_profile_binding", key)
	case "surface", "component":
		var payload map[string]any
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		if payload == nil {
			return metadataDefinitionPayloadShape{}, fmt.Errorf("metadata.%s.invalidPayload", strings.TrimSpace(resourceType))
		}
		key := valueOrFirstNonEmpty(resourceKey, metadataMapString(payload, "key"))
		payload["key"] = key
		objectKey := valueOrFirstNonEmpty(req.ObjectKey, metadataMapString(payload, "object_key"))
		name := valueOrFirstNonEmpty(req.Name, metadataMapString(payload, "name", "label"), key)
		return metadataDefinitionPayloadShape{Key: key, ObjectKey: objectKey, Name: name, Payload: payload}, metadataDefinitionKeyError(strings.TrimSpace(resourceType), key)
	case "entrypoint":
		var payload definitionmodel.EntryPointSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		return metadataDefinitionPayloadShape{Key: key, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("entrypoint", key)
	case "skill":
		var payload agentmodel.SkillSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		return metadataDefinitionPayloadShape{Key: key, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("skill", key)
	case "agent":
		var payload agentmodel.AgentSchema
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return metadataDefinitionPayloadShape{}, err
		}
		key := valueOrFirstNonEmpty(resourceKey, payload.Key)
		payload.Key = key
		return metadataDefinitionPayloadShape{Key: key, Name: valueOrFirstNonEmpty(req.Name, payload.Name), Payload: payload}, metadataDefinitionKeyError("agent", key)
	default:
		return metadataDefinitionPayloadShape{}, fmt.Errorf("unsupported metadata resource type %q", resourceType)
	}
}

func metadataDefinitionKeyError(resourceType string, key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("metadata.%s.missingKey", resourceType)
	}
	return nil
}

func valueOrFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func firstString(values []string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}
