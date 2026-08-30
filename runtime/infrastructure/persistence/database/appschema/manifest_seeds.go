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
	for _, workflow := range seed.Workflows {
		if err := appendSeed("workflow", "_application_schema_workflow_definitions", workflow.Key, metadataMapString(workflow.Trigger, "object_key", "object"), workflow.Name, workflow); err != nil {
			return nil, err
		}
	}
	for _, rule := range seed.AutomationRules {
		if err := appendSeed("automation_rule", "_application_schema_automation_rule_definitions", rule.Key, rule.ObjectKey, rule.Name, rule); err != nil {
			return nil, err
		}
	}
	for _, connector := range seed.Integrations.Connectors {
		if err := appendSeed("connector", "_application_schema_connector_requirements", connector.Key, "", connector.Name, connector); err != nil {
			return nil, err
		}
	}
	for _, mapping := range seed.Integrations.EventMappings {
		if err := appendSeed("integration_event_mapping", "_application_schema_integration_event_mapping_requirements", mapping.Key, "", mapping.Provider, mapping); err != nil {
			return nil, err
		}
	}
	return seeds, nil
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
