package projection

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	actionvalidation "github.com/domainry/domainry-runtime/runtime/domain/action/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestActionAuthoringPublishesMetadataWithoutStepDSL(t *testing.T) {
	definition := ActionDefinitionAuthoringCapability()
	if definition.SimulationEndpoint != "" {
		t.Fatalf("Action definition still publishes Runtime simulation: %q", definition.SimulationEndpoint)
	}
	if len(ActionAuthoringDomain().Capabilities) != 1 {
		t.Fatalf("Action domain still publishes leaf Step capabilities: %#v", ActionAuthoringDomain().Capabilities)
	}
	payload := definition.InputSchema.Properties["payload"]
	for _, forbidden := range []string{"audit_event", "config", "steps", "step_type", "compensation_policy"} {
		if _, exists := payload.Properties[forbidden]; exists {
			t.Fatalf("Action authoring payload still publishes %q", forbidden)
		}
	}
	for _, redundant := range []string{"object_key", "name", "source_kind", "source_id"} {
		if _, exists := definition.InputSchema.Properties[redundant]; exists {
			t.Fatalf("Action authoring request still publishes redundant envelope field %q", redundant)
		}
	}
	for _, reference := range definition.ReferenceContracts {
		if reference.Kind == "capability_key" {
			t.Fatalf("Action authoring still references Step capability: %#v", reference)
		}
	}
}

func TestActionAuthoringSchemaEnforcesWorkflowApprovalFieldContract(t *testing.T) {
	encoded, err := json.Marshal(ActionDefinitionAuthoringCapability().InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	payload := machineSchemaProperty(t, document, "payload")
	assurancePolicy := machineSchemaProperty(t, payload, "assurance_policy")

	tests := []struct {
		name   string
		policy map[string]any
		valid  bool
	}{
		{name: "workflow approval without bound fields", policy: map[string]any{"required_methods": []any{definitionmodel.ActionAssuranceWorkflowApproval}}},
		{name: "workflow approval without hash field", policy: map[string]any{"required_methods": []any{definitionmodel.ActionAssuranceWorkflowApproval}, "approval_version_field": "approval_version"}},
		{name: "workflow approval without version field", policy: map[string]any{"required_methods": []any{definitionmodel.ActionAssuranceWorkflowApproval}, "approval_hash_field": "approval_hash"}},
		{name: "workflow approval with both fields", policy: map[string]any{"required_methods": []any{definitionmodel.ActionAssuranceWorkflowApproval}, "approval_version_field": "approval_version", "approval_hash_field": "approval_hash"}, valid: true},
		{name: "workflow approval with blank fields", policy: map[string]any{"required_methods": []any{definitionmodel.ActionAssuranceWorkflowApproval}, "approval_version_field": " ", "approval_hash_field": "approval_hash"}},
		{name: "other assurance does not require approval fields", policy: map[string]any{"required_methods": []any{definitionmodel.ActionAssuranceOTP}}, valid: true},
		{name: "other assurance permits omitted-value representation", policy: map[string]any{"required_methods": []any{definitionmodel.ActionAssuranceOTP}, "approval_version_field": " ", "approval_hash_field": ""}, valid: true},
		{name: "approval fields require workflow approval", policy: map[string]any{"required_methods": []any{definitionmodel.ActionAssuranceOTP}, "approval_version_field": "approval_version"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := machineSchemaAccepts(assurancePolicy, test.policy)
			if got != test.valid {
				t.Fatalf("published assurance_policy schema accepts=%v, want %v; schema=%s", got, test.valid, encoded)
			}
			policyPayload, err := json.Marshal(test.policy)
			if err != nil {
				t.Fatal(err)
			}
			var policy definitionmodel.ActionAssurancePolicy
			if err := json.Unmarshal(policyPayload, &policy); err != nil {
				t.Fatal(err)
			}
			runtimeValid := true
			for _, issue := range actionvalidation.ActionValidateDefinitionIssues(definitionmodel.ActionSchema{
				Key: "order.approve", ObjectKey: "order", Kind: definitionmodel.ActionKindRecordUpdate, AssurancePolicy: &policy,
			}) {
				if strings.HasPrefix(issue.FieldPath, "assurance_policy") {
					runtimeValid = false
				}
			}
			if got != runtimeValid {
				t.Fatalf("published machine schema accepts=%v but Runtime assurance validator accepts=%v", got, runtimeValid)
			}
		})
	}
}

func machineSchemaProperty(t *testing.T, schema map[string]any, key string) map[string]any {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties: %#v", schema)
	}
	property, ok := properties[key].(map[string]any)
	if !ok {
		t.Fatalf("schema has no property %q: %#v", key, schema)
	}
	return property
}

// machineSchemaAccepts evaluates the Draft 2020-12 keywords used by this
// conditional contract. It intentionally consumes the serialized machine
// document, so a missing or misspelled JSON keyword cannot satisfy the test.
func machineSchemaAccepts(schema map[string]any, value any) bool {
	if expected, exists := schema["const"]; exists && !reflect.DeepEqual(value, expected) {
		return false
	}
	if values, ok := schema["enum"].([]any); ok {
		matched := false
		for _, expected := range values {
			matched = matched || reflect.DeepEqual(value, expected)
		}
		if !matched {
			return false
		}
	}
	switch schema["type"] {
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return false
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return false
		}
	case "string":
		if _, ok := value.(string); !ok {
			return false
		}
	}
	if pattern, ok := schema["pattern"].(string); ok {
		text, ok := value.(string)
		if !ok || !regexp.MustCompile(pattern).MatchString(text) {
			return false
		}
	}
	if object, ok := value.(map[string]any); ok {
		if required, ok := schema["required"].([]any); ok {
			for _, rawKey := range required {
				if _, exists := object[rawKey.(string)]; !exists {
					return false
				}
			}
		}
		if properties, ok := schema["properties"].(map[string]any); ok {
			for key, rawProperty := range properties {
				if item, exists := object[key]; exists && !machineSchemaAccepts(rawProperty.(map[string]any), item) {
					return false
				}
			}
		}
	}
	if items, ok := value.([]any); ok {
		if rawContains, exists := schema["contains"]; exists {
			contains := rawContains.(map[string]any)
			matched := false
			for _, item := range items {
				matched = matched || machineSchemaAccepts(contains, item)
			}
			if !matched {
				return false
			}
		}
	}
	if rawCondition, exists := schema["if"]; exists {
		conditionMatches := machineSchemaAccepts(rawCondition.(map[string]any), value)
		keyword := "else"
		if conditionMatches {
			keyword = "then"
		}
		if branch, exists := schema[keyword]; exists && !machineSchemaAccepts(branch.(map[string]any), value) {
			return false
		}
	}
	return true
}
