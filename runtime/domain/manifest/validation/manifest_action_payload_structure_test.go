package validation

import (
	"fmt"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestActionsValidateStructuredPayloadFields(t *testing.T) {
	deep := definitionmodel.ActionPayloadField{Key: "l1", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "l2", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "l3", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "l4", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "leaf", Type: "text"}}}}}}}}}
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}},
		Actions: []definitionmodel.ActionSchema{
			{Key: "customer.register_accounts", ObjectKey: "customer", Kind: definitionmodel.ActionKindObjectOperation, PayloadFields: []definitionmodel.ActionPayloadField{
				{Key: "name", Type: "text", Required: true, SourceObjectKey: "customer", SourceFieldKey: "name"},
				{Key: "accounts", Type: "object", Repeated: true, Required: true, Fields: []definitionmodel.ActionPayloadField{{Key: "bank", Type: "text", Required: true}}},
			}},
			{Key: "customer.broken", ObjectKey: "customer", Kind: definitionmodel.ActionKindObjectOperation, PayloadFields: []definitionmodel.ActionPayloadField{
				deep,
				{Key: "accounts", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "owner", Type: "relation"}, {Key: "n", SourceObjectKey: "customer", SourceFieldKey: "ghost"}}},
			}},
		},
	}
	state := newValidationState(manifest, nil)
	state.validateActions()
	joined := fmt.Sprint(state.errs)
	for _, want := range []string{
		"actions[1].payload_fields[0].fields[0].fields[0].fields[0].fields",
		"actions[1].payload_fields[1].fields[0].target_object_key",
		"actions[1].payload_fields[1].fields[1].source_field_key",
		"backend.metadata.action_payload_field_invalid",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, state.errs)
		}
	}
	if strings.Contains(joined, "actions[0]") {
		t.Fatalf("valid structured action reported: %v", state.errs)
	}
}
