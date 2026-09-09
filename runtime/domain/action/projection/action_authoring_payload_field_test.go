package projection

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestActionAuthoringPayloadFieldSchemaIsRecursive(t *testing.T) {
	schema := ActionDefinitionAuthoringCapability().InputSchema
	definition, ok := schema.Definitions["action_payload_field"]
	if !ok {
		t.Fatalf("payload field definition missing: %#v", schema.Definitions)
	}
	payloadFields := schema.Properties["payload"].Properties["payload_fields"]
	if payloadFields.Items == nil || payloadFields.Items.Ref != "#/$defs/action_payload_field" {
		t.Fatalf("payload_fields items=%#v", payloadFields.Items)
	}
	fields := definition.Properties["fields"]
	if fields.Items == nil || fields.Items.Ref != "#/$defs/action_payload_field" {
		t.Fatalf("nested fields=%#v", fields)
	}
	for _, key := range []string{"repeated", "min_items", "max_items", "target_object_key"} {
		if _, exists := definition.Properties[key]; !exists {
			t.Fatalf("payload field schema lacks %s: %#v", key, definition.Properties)
		}
	}
	hasObject := false
	for _, value := range definition.Properties["type"].Enum {
		hasObject = hasObject || value == definitionmodel.ActionPayloadTypeObject
	}
	if !hasObject {
		t.Fatalf("type enum lacks object: %#v", definition.Properties["type"].Enum)
	}
	closed := false
	if definition.AdditionalProperties == nil || *definition.AdditionalProperties != closed {
		t.Fatalf("payload field definition must stay closed: %#v", definition.AdditionalProperties)
	}
}
