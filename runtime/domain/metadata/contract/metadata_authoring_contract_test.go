package contract

import (
	"reflect"
	"strings"
	"testing"
)

func TestMetadataAuthoringCapabilitiesAreOwnerOwnedAndComplete(t *testing.T) {
	capabilities := MetadataAuthoringCapabilities()
	wantKeys := []string{"schema.object", "schema.field", "schema.relation", "schema.dictionary"}
	if len(capabilities) != len(wantKeys) {
		t.Fatalf("metadata authoring capability count=%d want=%d", len(capabilities), len(wantKeys))
	}
	for index, capability := range capabilities {
		if capability.Key != wantKeys[index] || len(capability.Parameters) == 0 || capability.ValidationEndpoint == "" || len(capability.ConfigurationRoutes) == 0 || capability.InputSchema == nil || capability.OutputSchema == nil || capability.Execution == nil {
			t.Fatalf("metadata authoring capability[%d]=%#v", index, capability)
		}
		if capability.InputSchema.AdditionalProperties == nil || *capability.InputSchema.AdditionalProperties {
			t.Fatalf("metadata capability %s input schema is not closed", capability.Key)
		}
		if len(capability.Examples) != 3 || capability.Examples[0].Name != "minimal_valid" || capability.Examples[1].Name != "representative" || capability.Examples[2].Name != "invalid_with_repair" || len(capability.Examples[2].ExpectedErrorCodes) == 0 {
			t.Fatalf("metadata capability %s examples=%#v", capability.Key, capability.Examples)
		}
		for _, source := range capability.Sources {
			if source.Kind == "contract" && !strings.HasPrefix(source.Path, "runtime/domain/metadata/contract/") {
				t.Fatalf("metadata capability %s contract source=%s", capability.Key, source.Path)
			}
		}
	}
	if fieldTypes := MetadataAuthoringFieldTypes(); !reflect.DeepEqual(fieldTypes, []string{"boolean", "currency", "date", "datetime", "email", "integer", "long_text", "number", "percent", "phone", "relation", "select", "text", "url", "user"}) {
		t.Fatalf("metadata field types=%v", fieldTypes)
	}
}

func TestMetadataObjectAuthoringPublishesConsumedUXContract(t *testing.T) {
	capability := MetadataObjectAuthoringCapability()
	payload := capability.InputSchema.Properties["payload"]
	ux := payload.Properties["ux"]
	if ux.Type != "object" || ux.AdditionalProperties == nil || *ux.AdditionalProperties {
		t.Fatalf("object ux schema=%#v", ux)
	}
	kind := ux.Properties["kind"]
	if !reflect.DeepEqual(kind.Enum, []any{"identity_profile_extension"}) {
		t.Fatalf("object ux.kind enum=%#v", kind.Enum)
	}
	config := ux.Properties["config"]
	if config.Properties["identity_relation_field"].Type != "string" {
		t.Fatalf("object ux.config schema=%#v", config)
	}
	if ux.Properties["display"].Properties["title_field"].Type != "string" {
		t.Fatalf("object ux.display schema=%#v", ux.Properties["display"])
	}
}
