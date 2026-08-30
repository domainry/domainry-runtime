package capability

import (
	"encoding/json"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemavalidation "github.com/domainry/domainry-runtime/runtime/domain/appschema/validation"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationConnectorAuthoringExamplesExecuteRuntimeValidator(t *testing.T) {
	capability := integrationcontract.IntegrationConnectorDefinitionAuthoringCapability()
	for _, example := range capability.Examples {
		connector := integrationConnectorExample(t, example.Value["payload"])
		assertIntegrationConnectorExampleIssues(t, capability.Key, example.Name, example.ExpectedErrorCodes, appschemavalidation.ApplicationSchemaValidateConnectorDefinitionIssues(connector))
	}
}

func TestIntegrationConnectorOperationExamplesExecuteRuntimeValidator(t *testing.T) {
	capability := integrationcontract.IntegrationConnectorOperationAuthoringCapability()
	for _, example := range capability.Examples {
		payload, err := json.Marshal(example.Value)
		if err != nil {
			t.Fatal(err)
		}
		var operation integrationmodel.ConnectorOperationSchema
		if err := json.Unmarshal(payload, &operation); err != nil {
			t.Fatal(err)
		}
		connector := integrationmodel.ConnectorSchema{Key: "example", Type: "http", Provider: "default", Operations: []integrationmodel.ConnectorOperationSchema{operation}}
		assertIntegrationConnectorExampleIssues(t, capability.Key, example.Name, example.ExpectedErrorCodes, appschemavalidation.ApplicationSchemaValidateConnectorDefinitionIssues(connector))
	}
}

func integrationConnectorExample(t *testing.T, value any) integrationmodel.ConnectorSchema {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var connector integrationmodel.ConnectorSchema
	if err := json.Unmarshal(payload, &connector); err != nil {
		t.Fatal(err)
	}
	return connector
}

func assertIntegrationConnectorExampleIssues(t *testing.T, capabilityKey, exampleName string, expected []string, issues []appschemamodel.ApplicationDefinitionValidationIssue) {
	t.Helper()
	if len(expected) == 0 {
		if len(issues) != 0 {
			t.Fatalf("capability=%s example=%s issues=%#v", capabilityKey, exampleName, issues)
		}
		return
	}
	for _, issue := range issues {
		if issue.ErrorCode == expected[0] {
			return
		}
	}
	t.Fatalf("capability=%s example=%s issues=%#v want=%s", capabilityKey, exampleName, issues, expected[0])
}
