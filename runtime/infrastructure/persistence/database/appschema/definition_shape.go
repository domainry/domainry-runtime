package appschema

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	reportmodel "github.com/domainry/domainry-report-sdk/model"

	"context"
	"encoding/json"
	"fmt"

	schedulervalidation "github.com/domainry/domainry-runtime/runtime/domain/scheduler/validation"

	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

type metadataDefinitionPayloadShape struct {
	Key       string
	ObjectKey string
	Name      string
	Payload   any
}

func metadataDefinitionTable(resourceType string) (string, error) {
	switch strings.TrimSpace(resourceType) {
	case "workflow":
		return "_application_schema_workflow_definitions", nil
	case "automation_rule":
		return "_application_schema_automation_rule_definitions", nil
	case "connector":
		return "_application_schema_connector_requirements", nil
	case "integration_event_mapping":
		return "_application_schema_integration_event_mapping_requirements", nil
	default:
		return "", fmt.Errorf("unsupported metadata resource type %q", resourceType)
	}
}

func metadataDefinitionShape(ctx context.Context, resourceType string, resourceKey string, req appschemamodel.ApplicationDefinitionUpsertRequest) (metadataDefinitionPayloadShape, error) {
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
	case "dictionary":
		var payload appschemamodel.DictionarySchema
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
