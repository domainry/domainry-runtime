package appschema

import (
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func manifestMetadataSeeds(seed manifestmodel.ManifestSchema) ([]metadataResourceSeed, error) {
	version := strings.TrimSpace(seed.Version)
	if version == "" {
		version = "1"
	}
	sourceID := strings.TrimSpace(seed.TemplateID)
	if sourceID == "" {
		sourceID = "generated-template"
	}
	newSeed := func(resourceType, table, key, objectKey, name string, payload any) (metadataResourceSeed, error) {
		key = strings.TrimSpace(key)
		if key == "" {
			return metadataResourceSeed{}, fmt.Errorf("%s metadata key is required", resourceType)
		}
		return metadataResourceSeed{
			ResourceType:  resourceType,
			Table:         table,
			Key:           key,
			ObjectKey:     strings.TrimSpace(objectKey),
			Name:          strings.TrimSpace(name),
			SchemaVersion: version,
			SourceKind:    "generated",
			SourceID:      sourceID,
			Payload:       payload,
		}, nil
	}
	seeds := []metadataResourceSeed{}
	appendSeed := func(resourceType, table, key, objectKey, name string, payload any) error {
		seed, err := newSeed(resourceType, table, key, objectKey, name, payload)
		if err != nil {
			return err
		}
		seeds = append(seeds, seed)
		return nil
	}
	appendDerivedSeed := func(resourceType, table, key, objectKey, name string, payload any) {
		seeds = append(seeds, metadataResourceSeed{
			ResourceType: resourceType, Table: table, Key: strings.TrimSpace(key), ObjectKey: strings.TrimSpace(objectKey), Name: strings.TrimSpace(name),
			SchemaVersion: version, SourceKind: "generated", SourceID: sourceID, Payload: payload,
		})
	}
	for _, object := range seed.Objects {
		objectCopy := object
		objectCopy.Fields = nil
		objectCopy.Validations = nil
		if err := appendSeed("object", "object_definitions", object.Key, object.Key, object.Name, objectCopy); err != nil {
			return nil, err
		}
		for _, field := range object.Fields {
			fieldCopy := field
			config := map[string]any{}
			for key, value := range field.Config {
				config[key] = value
			}
			config["_definition_object_key"] = object.Key
			fieldCopy.Config = config
			fieldKey := metadataJoinedKey(object.Key, field.Key)
			appendDerivedSeed("field", "field_definitions", fieldKey, object.Key, field.Name, fieldCopy)
		}
		for index, validation := range object.Validations {
			if strings.TrimSpace(validation.ObjectKey) == "" {
				validation.ObjectKey = object.Key
			}
			key := validation.Key
			if strings.TrimSpace(key) == "" {
				key = validationMetadataKey(object.Key, index, validation)
			}
			appendDerivedSeed("validation", "validation_definitions", key, validation.ObjectKey, validation.Message, validation)
		}
	}
	for _, view := range seed.Views {
		if err := appendSeed("view", "view_definitions", view.Key, view.ObjectKey, view.Name, view); err != nil {
			return nil, err
		}
	}
	for _, action := range seed.Actions {
		if err := appendSeed("action", "action_definitions", action.Key, action.ObjectKey, action.Label, action); err != nil {
			return nil, err
		}
	}
	for _, workflow := range seed.Workflows {
		if err := appendSeed("workflow", "workflow_definitions", workflow.Key, metadataMapString(workflow.Trigger, "object_key", "object"), workflow.Name, workflow); err != nil {
			return nil, err
		}
	}
	for _, scheduler := range seed.SchedulerDefinitions {
		if err := appendSeed("scheduler", "scheduler_definitions", metadataMapString(scheduler, "key"), "", metadataMapString(scheduler, "name"), scheduler); err != nil {
			return nil, err
		}
	}
	for _, rule := range seed.AutomationRules {
		if err := appendSeed("automation_rule", "automation_rule_definitions", rule.Key, rule.ObjectKey, rule.Name, rule); err != nil {
			return nil, err
		}
	}
	for _, dictionary := range seed.Dictionaries {
		if err := appendSeed("dictionary", "dictionary_definitions", dictionary.Key, "", dictionary.Name, dictionary); err != nil {
			return nil, err
		}
	}
	for _, connector := range seed.Integrations.Connectors {
		if err := appendSeed("connector", "connector_definitions", connector.Key, "", connector.Name, connector); err != nil {
			return nil, err
		}
	}
	for _, mapping := range seed.Integrations.EventMappings {
		if err := appendSeed("integration_event_mapping", "integration_event_mapping_definitions", mapping.Key, "", mapping.Provider, mapping); err != nil {
			return nil, err
		}
	}
	for _, report := range seed.Reports {
		if err := appendSeed("report", "report_definitions", report.Key, "", report.Name, report); err != nil {
			return nil, err
		}
	}
	for _, example := range seed.OperationStateExamples {
		if err := appendSeed("operation_state_example", "operation_state_example_definitions", example.Key, example.ObjectKey, example.Name, example); err != nil {
			return nil, err
		}
	}
	for _, policy := range seed.SensitiveFieldPolicies {
		if err := appendSeed("sensitive_field_policy", "sensitive_field_policy_definitions", policy.Key, policy.ObjectKey, policy.Name, policy); err != nil {
			return nil, err
		}
	}
	for _, control := range seed.ReportExportControls {
		if err := appendSeed("report_export_control", "report_export_control_definitions", control.Key, control.ReportKey, control.Name, control); err != nil {
			return nil, err
		}
	}
	for _, entrypoint := range seed.EntryPoints {
		if err := appendSeed("entrypoint", "entrypoint_definitions", entrypoint.Key, "", entrypoint.Name, entrypoint); err != nil {
			return nil, err
		}
	}
	for _, skill := range seed.Skills {
		if err := appendSeed("skill", "skill_definitions", skill.Key, "", skill.Name, skill); err != nil {
			return nil, err
		}
	}
	for _, agent := range seed.Agents {
		if err := appendSeed("agent", "agent_definitions", agent.Key, "", agent.Name, agent); err != nil {
			return nil, err
		}
	}
	for _, task := range seed.AgentTasks {
		if err := appendSeed("agent_task", "agent_task_definitions", metadataAgentTaskKey(task.Key, task.Version), "", task.Name, task); err != nil {
			return nil, err
		}
	}
	for _, entrypoint := range seed.AgentEntrypoints {
		if err := appendSeed("agent_entrypoint", "agent_entrypoint_definitions", entrypoint.Key, "", entrypoint.Key, entrypoint); err != nil {
			return nil, err
		}
	}
	for _, principal := range seed.AgentServicePrincipals {
		if err := appendSeed("agent_service_principal", "agent_service_principal_definitions", principal.Key, "", principal.Key, principal); err != nil {
			return nil, err
		}
	}
	for _, binding := range seed.IdentityProfileExtensions {
		if err := appendSeed("identity_profile_binding", "identity_profile_binding_definitions", binding.ObjectKey, binding.ObjectKey, binding.BusinessIdentity.Key, binding); err != nil {
			return nil, err
		}
	}
	return seeds, nil
}

func metadataAgentTaskKey(key, version string) string {
	key, version = strings.TrimSpace(key), strings.TrimSpace(version)
	if key == "" || version == "" {
		return ""
	}
	return key + "@" + version
}

func metadataPayload(payload any) ([]byte, string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func metadataResourceID(resourceType, key string) string {
	return strings.TrimSpace(resourceType) + ":" + strings.TrimSpace(key)
}

func metadataHashPrefix(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func metadataJoinedKey(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return left + "." + right
}

func metadataFieldObjectKey(field definitionmodel.FieldSchema) string {
	if value, ok := field.Config["_definition_object_key"]; ok {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	if value, ok := field.Config["definition_object_key"]; ok {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func validationMetadataKey(objectKey string, index int, validation definitionmodel.ValidationSchema) string {
	parts := []string{objectKey, validation.Type, validation.FieldKey, strings.Join(validation.Fields, "_")}
	out := []string{}
	for _, part := range parts {
		if text := strings.Trim(strings.TrimSpace(part), "."); text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return fmt.Sprintf("%s.validation.%d", strings.TrimSpace(objectKey), index+1)
	}
	return strings.Join(out, ".")
}

func metadataMapString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}
