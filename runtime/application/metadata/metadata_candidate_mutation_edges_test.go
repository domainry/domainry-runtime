package metadata

import (
	"encoding/json"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func candidateMutation(operation, resourceType, resourceKey, objectKey, payload string) metadatamodel.MetadataDefinitionMutation {
	return metadatamodel.MetadataDefinitionMutation{
		Operation: operation, ResourceType: resourceType, ResourceKey: resourceKey,
		Request: metadatamodel.MetadataDefinitionUpsertRequest{ObjectKey: objectKey, Payload: json.RawMessage(payload)},
	}
}

func ruleSetReferencePayload(inputType, outputType string) json.RawMessage {
	return json.RawMessage(`{"key":"policy.limit","name":"Limit policy","match_policy":"first_match","input_types":{"limit":"` + inputType + `"},"output_types":{"allowed":"` + outputType + `"},"effective_from":"2026-07-01","rules":[{"key":"allow","priority":1,"when":{"kind":"literal","value_type":"boolean","value":true},"outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":true}}}],"default_outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":false}}}`)
}

func TestApplyMetadataCandidateMutationCoversEveryPublishedResourceKind(t *testing.T) {
	candidate := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "customer", Name: "Customer",
		Fields:      []definitionmodel.FieldSchema{{Key: "legacy", Name: "Legacy", Type: "text"}},
		Validations: []definitionmodel.ValidationSchema{{Key: "legacy_rule", ObjectKey: "customer"}},
	}}}

	creates := []metadatamodel.MetadataDefinitionMutation{
		candidateMutation("create", "object", "customer", "", `{"key":"customer","name":"Updated"}`),
		candidateMutation("create", "field", "customer.name", "customer", `{"key":"name","name":"Name","type":"text"}`),
		candidateMutation("create", "validation", "required_name", "customer", `{"key":"required_name","object_key":"customer"}`),
		candidateMutation("create", "view", "customer.list", "", `{"key":"customer.list"}`),
		candidateMutation("create", "action", "customer.update", "", `{"key":"customer.update"}`),
		candidateMutation("create", "workflow", "customer.flow", "", `{"key":"customer.flow"}`),
		candidateMutation("create", "scheduler", "customer.timer", "", `{}`),
		candidateMutation("create", "automation_rule", "customer.changed", "", `{"key":"customer.changed"}`),
		candidateMutation("create", "dictionary", "customer.status", "", `{"key":"customer.status"}`),
		candidateMutation("create", "connector", "crm", "", `{"key":"crm"}`),
		candidateMutation("create", "integration_event_mapping", "crm.updated", "", `{"key":"crm.updated"}`),
		candidateMutation("create", "report", "customer.summary", "", `{"key":"customer.summary"}`),
		candidateMutation("create", "operation_state_example", "customer.example", "", `{"key":"customer.example"}`),
		candidateMutation("create", "sensitive_field_policy", "customer.secret", "", `{"key":"customer.secret"}`),
		candidateMutation("create", "report_export_control", "customer.export", "", `{"key":"customer.export"}`),
		candidateMutation("create", "entrypoint", "customer.home", "", `{"key":"customer.home"}`),
		candidateMutation("create", "skill", "customer.skill", "", `{"key":"customer.skill"}`),
		candidateMutation("create", "agent", "customer.agent", "", `{"key":"customer.agent"}`),
		candidateMutation("create", "identity_profile_binding", "customer", "", `{"object_key":"customer"}`),
		candidateMutation("create", "preference", "customer.limit", "", `{"key":"customer.limit","name":"Limit","value_type":"integer","value":10,"effective_from":"2026-01-01"}`),
		candidateMutation("create", "rule_set", "policy.limit", "", string(ruleSetReferencePayload("integer", "boolean"))),
	}
	for _, mutation := range creates {
		if err := applyMetadataCandidateMutation(&candidate, mutation); err != nil {
			t.Fatalf("create %s: %v", mutation.ResourceType, err)
		}
	}
	if len(candidate.Objects[0].Fields) != 2 || len(candidate.Objects[0].Validations) != 2 {
		t.Fatalf("object update discarded separately authored members: %#v", candidate.Objects[0])
	}

	for index := len(creates) - 1; index >= 0; index-- {
		mutation := creates[index]
		mutation.Operation = "archive"
		if err := applyMetadataCandidateMutation(&candidate, mutation); err != nil {
			t.Fatalf("archive %s: %v", mutation.ResourceType, err)
		}
	}
	if len(candidate.Objects) != 0 {
		t.Fatalf("object archive did not remove object: %#v", candidate.Objects)
	}
}

func TestApplyMetadataCandidateMutationRejectsEveryMalformedBoundary(t *testing.T) {
	candidate := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}}
	if err := applyMetadataCandidateMutation(&candidate, candidateMutation("noop", "unknown", "x", "", `{`)); err != nil {
		t.Fatalf("noop must not inspect payload: %v", err)
	}
	for _, test := range []struct {
		name     string
		mutation metadatamodel.MetadataDefinitionMutation
		contains string
	}{
		{"operation", candidateMutation("publish", "object", "x", "", `{}`), "unsupported operation"},
		{"resource", candidateMutation("create", "unknown", "x", "", `{}`), "unsupported candidate resource type"},
		{"object json", candidateMutation("create", "object", "x", "", `{`), "unexpected end"},
		{"object key", candidateMutation("create", "object", "x", "", `{"key":"y"}`), "resource key mismatch"},
		{"field object", candidateMutation("create", "field", "missing.name", "", `{"key":"name"}`), "unknown object"},
		{"field json", candidateMutation("create", "field", "customer.name", "", `{`), "unexpected end"},
		{"validation object", candidateMutation("create", "validation", "rule", "missing", `{"key":"rule","object_key":"missing"}`), "unknown object"},
		{"validation inferred object", candidateMutation("create", "validation", "rule", "", `{"key":"rule","object_key":"missing"}`), "unknown object"},
		{"validation json", candidateMutation("create", "validation", "rule", "customer", `{`), "unexpected end"},
		{"validation mismatch", candidateMutation("create", "validation", "rule", "customer", `{"key":"rule","object_key":"other"}`), "object key mismatch"},
		{"slice json", candidateMutation("create", "view", "x", "", `{`), "unexpected end"},
		{"slice key", candidateMutation("create", "view", "x", "", `{"key":"y"}`), "resource key mismatch"},
		{"identity role", candidateMutation("create", "role", "operator", "", `{"key":"operator"}`), "unsupported candidate resource type"},
		{"preference", candidateMutation("create", "preference", "x", "", `{}`), "preference"},
		{"rule set", candidateMutation("create", "rule_set", "x", "", `{}`), "rule"},
		{"surface", candidateMutation("create", "surface", "x", "", `{}`), "unsupported candidate resource type"},
		{"component", candidateMutation("create", "component", "x", "", `{}`), "unsupported candidate resource type"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := applyMetadataCandidateMutation(&candidate, test.mutation)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.contains)) {
				t.Fatalf("error=%v, want substring %q", err, test.contains)
			}
		})
	}

	for _, resourceType := range []string{"preference", "rule_set"} {
		if err := applyMetadataCandidateMutation(&candidate, candidateMutation("delete", resourceType, "x", "", `{`)); err != nil {
			t.Fatalf("delete %s must not decode payload: %v", resourceType, err)
		}
	}
}

func TestCandidateObjectMemberKeyUsesExplicitAndQualifiedKeys(t *testing.T) {
	objectKey, fieldKey := candidateObjectMemberKey(candidateMutation("create", "field", " customer.name ", "", `{}`))
	if objectKey != "customer" || fieldKey != "name" {
		t.Fatalf("qualified key = %q.%q", objectKey, fieldKey)
	}
	objectKey, fieldKey = candidateObjectMemberKey(candidateMutation("create", "field", "plain", " explicit ", `{}`))
	if objectKey != "explicit" || fieldKey != "" {
		t.Fatalf("explicit key = %q.%q", objectKey, fieldKey)
	}
}
