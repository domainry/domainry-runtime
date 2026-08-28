package validation

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestConnectorDefinitionValidationOwnsProtocolIssues(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "finance", Type: "http", Provider: "finance", Config: map[string]any{"providers": []any{}}, Operations: []integrationmodel.ConnectorOperationSchema{{
		Key: "push", Method: "TRACE", ExecutionMode: "later", SideEffect: "mutation", TimeoutDefaultSeconds: 30, TimeoutMaxSeconds: 10,
		Input: []definitionmodel.FieldSchema{{Key: "payload", Type: "mystery"}},
	}}}
	issues := MetadataValidateConnectorDefinitionIssues(connector)
	want := map[string]bool{
		"config.providers":                      false,
		"operations[0].execution_mode":          false,
		"operations[0].method":                  false,
		"operations[0].side_effect":             false,
		"operations[0].timeout_default_seconds": false,
		"operations[0].input[0].type":           false,
	}
	for _, issue := range issues {
		if _, ok := want[issue.FieldPath]; ok {
			want[issue.FieldPath] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Fatalf("expected issue for %s, got %#v", path, issues)
		}
	}
}
