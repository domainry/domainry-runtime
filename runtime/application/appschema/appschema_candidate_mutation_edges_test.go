package appschema

import (
	"encoding/json"
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func candidateMutation(operation, resourceType, resourceKey, objectKey, payload string) appschemamodel.ApplicationDefinitionMutation {
	return appschemamodel.ApplicationDefinitionMutation{
		Operation: operation, ResourceType: resourceType, ResourceKey: resourceKey,
		Request: appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: objectKey, Payload: json.RawMessage(payload)},
	}
}

func TestApplyMetadataCandidateMutationCoversEveryPublishedResourceKind(t *testing.T) {
	candidate := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "customer", Name: "Customer",
		Fields:      []definitionmodel.FieldSchema{{Key: "legacy", Name: "Legacy", Type: "text"}},
		Validations: []definitionmodel.ValidationSchema{{Key: "legacy_rule", ObjectKey: "customer"}},
	}}}

	creates := []appschemamodel.ApplicationDefinitionMutation{
		candidateMutation("create", "object", "customer", "", `{"key":"customer","name":"Updated"}`),
		candidateMutation("create", "field", "customer.name", "customer", `{"key":"name","name":"Name","type":"text"}`),
		candidateMutation("create", "validation", "required_name", "customer", `{"key":"required_name","object_key":"customer"}`),
		candidateMutation("create", "action", "customer.update", "", `{"key":"customer.update"}`),
		candidateMutation("create", "workflow", "customer.flow", "", `{"key":"customer.flow"}`),
		candidateMutation("create", "automation_rule", "customer.changed", "", `{"key":"customer.changed"}`),
		candidateMutation("create", "dictionary", "customer.status", "", `{"key":"customer.status"}`),
		candidateMutation("create", "integration_event_mapping", "crm.updated", "", `{"key":"crm.updated"}`),
		candidateMutation("create", "skill", "customer.skill", "", `{"key":"customer.skill"}`),
		candidateMutation("create", "agent", "customer.agent", "", `{"key":"customer.agent"}`),
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
		mutation appschemamodel.ApplicationDefinitionMutation
		contains string
	}{
		{"operation", candidateMutation("publish", "object", "x", "", `{}`), "unsupported operation"},
		{"resource", candidateMutation("create", "unknown", "x", "", `{}`), "unsupported candidate resource type"},
		{"connector owner", candidateMutation("create", "connector", "crm", "", `{}`), "unsupported candidate resource type"},
		{"object json", candidateMutation("create", "object", "x", "", `{`), "unexpected end"},
		{"object key", candidateMutation("create", "object", "x", "", `{"key":"y"}`), "resource key mismatch"},
		{"field object", candidateMutation("create", "field", "missing.name", "", `{"key":"name"}`), "unknown object"},
		{"field json", candidateMutation("create", "field", "customer.name", "", `{`), "unexpected end"},
		{"validation object", candidateMutation("create", "validation", "rule", "missing", `{"key":"rule","object_key":"missing"}`), "unknown object"},
		{"validation inferred object", candidateMutation("create", "validation", "rule", "", `{"key":"rule","object_key":"missing"}`), "unknown object"},
		{"validation json", candidateMutation("create", "validation", "rule", "customer", `{`), "unexpected end"},
		{"validation mismatch", candidateMutation("create", "validation", "rule", "customer", `{"key":"rule","object_key":"other"}`), "object key mismatch"},
		{"identity role", candidateMutation("create", "role", "operator", "", `{"key":"operator"}`), "unsupported candidate resource type"},
		{"preference", candidateMutation("create", "preference", "x", "", `{}`), "unsupported candidate resource type"},
		{"rule set", candidateMutation("create", "rule_set", "x", "", `{}`), "unsupported candidate resource type"},
		{"component", candidateMutation("create", "component", "x", "", `{}`), "unsupported candidate resource type"},
		{"scheduler module", candidateMutation("create", "scheduler", "x", "", `{}`), "unsupported candidate resource type"},
		{"report module", candidateMutation("create", "report", "x", "", `{}`), "unsupported candidate resource type"},
		{"identity module", candidateMutation("create", "identity_profile_binding", "x", "", `{}`), "unsupported candidate resource type"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := applyMetadataCandidateMutation(&candidate, test.mutation)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.contains)) {
				t.Fatalf("error=%v, want substring %q", err, test.contains)
			}
		})
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
