package appschema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func (s ApplicationSchemaStore) syncMetadataModuleDefinitions(ctx context.Context, manifest manifestmodel.ManifestSchema) error {
	if s.metadata == nil || s.metadata.Projection() == nil {
		return fmt.Errorf("Metadata projection is unavailable")
	}
	definitions := []metadatasdk.Definition{}
	appendDefinition := func(resourceType, key, objectKey, name string, value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return err
		}
		definitions = append(definitions, metadatasdk.Definition{ResourceType: resourceType, ResourceKey: strings.TrimSpace(key), ObjectKey: strings.TrimSpace(objectKey), Name: strings.TrimSpace(name), Payload: payload})
		return nil
	}
	for _, object := range manifest.Objects {
		objectCopy := object
		objectCopy.Fields, objectCopy.Validations = nil, nil
		if err := appendDefinition("object", object.Key, object.Key, object.Name, objectCopy); err != nil {
			return err
		}
		for _, field := range object.Fields {
			fieldCopy := field
			fieldCopy.Config = cloneMetadataModuleConfig(field.Config)
			fieldCopy.Config["_definition_object_key"] = object.Key
			if err := appendDefinition("field", metadataJoinedKey(object.Key, field.Key), object.Key, field.Name, fieldCopy); err != nil {
				return err
			}
		}
		for index, validation := range object.Validations {
			if strings.TrimSpace(validation.ObjectKey) == "" {
				validation.ObjectKey = object.Key
			}
			key := strings.TrimSpace(validation.Key)
			if key == "" {
				key = validationMetadataKey(object.Key, index, validation)
			}
			if err := appendDefinition("validation", key, validation.ObjectKey, validation.Message, validation); err != nil {
				return err
			}
		}
	}
	for _, action := range manifest.Actions {
		if err := appendDefinition("action", action.Key, action.ObjectKey, action.Label, action); err != nil {
			return err
		}
	}
	for _, workflow := range manifest.Workflows {
		if err := appendDefinition("workflow", workflow.Key, metadataMapString(workflow.Trigger, "object_key", "object"), workflow.Name, workflow); err != nil {
			return err
		}
	}
	for _, rule := range manifest.AutomationRules {
		if err := appendDefinition("automation_rule", rule.Key, rule.ObjectKey, rule.Name, rule); err != nil {
			return err
		}
	}
	for _, dictionary := range manifest.Dictionaries {
		if err := appendDefinition("dictionary", dictionary.Key, "", dictionary.Name, dictionary); err != nil {
			return err
		}
	}
	for _, mapping := range manifest.Integrations.EventMappings {
		if err := appendDefinition("integration_event_mapping", mapping.Key, mapping.ObjectKey, mapping.Provider, mapping); err != nil {
			return err
		}
	}
	for _, report := range manifest.Reports {
		if err := appendDefinition("report", report.Key, "", report.Name, report); err != nil {
			return err
		}
	}
	for _, example := range manifest.OperationStateExamples {
		if err := appendDefinition("operation_state_example", example.Key, example.ObjectKey, example.Name, example); err != nil {
			return err
		}
	}
	for _, policy := range manifest.SensitiveFieldPolicies {
		if err := appendDefinition("sensitive_field_policy", policy.Key, policy.ObjectKey, policy.Name, policy); err != nil {
			return err
		}
	}
	for _, control := range manifest.ReportExportControls {
		if err := appendDefinition("report_export_control", control.Key, control.ReportKey, control.Name, control); err != nil {
			return err
		}
	}
	for _, binding := range manifest.IdentityProfileExtensions {
		if err := appendDefinition("identity_profile_binding", binding.ObjectKey, binding.ObjectKey, binding.BusinessIdentity.Key, binding); err != nil {
			return err
		}
	}
	for _, skill := range manifest.Skills {
		if err := appendDefinition("skill", skill.Key, "", skill.Name, skill); err != nil {
			return err
		}
	}
	for _, agent := range manifest.Agents {
		if err := appendDefinition("agent", agent.Key, "", agent.Name, agent); err != nil {
			return err
		}
	}
	for _, scheduler := range manifest.SchedulerDefinitions {
		if err := appendDefinition("scheduler", metadataMapString(scheduler, "key"), "", metadataMapString(scheduler, "name"), scheduler); err != nil {
			return err
		}
	}
	version := strings.TrimSpace(manifest.Version)
	if version == "" {
		version = "1"
	}
	sourceID := manifestGeneratedSourceID(manifest)
	localized := manifestLocalizedTextSeeds(manifest)
	return s.metadata.Projection().Sync(ctx, metadatasdk.ProjectionSnapshot{
		SchemaVersion: version, SourceKind: "generated", SourceID: sourceID, Name: strings.TrimSpace(manifest.Name), DefaultLocale: manifestDefaultLocale(manifest),
		Definitions: definitions, LocalizedText: localized,
	})
}

func cloneMetadataModuleConfig(value map[string]any) map[string]any {
	result := make(map[string]any, len(value)+1)
	for key, item := range value {
		result[key] = item
	}
	return result
}

func decodeMetadataModuleDefinition(key string, payload json.RawMessage, target any) error {
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode Metadata definition %s: %w", key, err)
	}
	return nil
}
