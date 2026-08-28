package capability

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
)

func TestSupportedAuthoringCapabilitiesDeclareDirectPermissionsAndValidation(t *testing.T) {
	for _, domainContract := range RuntimeAuthoringCapabilities().Domains {
		for _, item := range domainContract.Capabilities {
			if item.Status != "supported" {
				continue
			}
			if len(item.Permissions) == 0 {
				t.Errorf("supported capability %q does not declare direct permissions", item.Key)
			}
			mutable := (item.Lifecycle == "business_schedule" || strings.Contains(item.Lifecycle, "configuration") || strings.Contains(item.Lifecycle, "versioned_metadata") || strings.Contains(item.Lifecycle, "draft")) && hasMutationRoute(item.ConfigurationRoutes)
			if mutable && (len(item.Parameters) == 0 || strings.TrimSpace(item.ValidationEndpoint) == "" || len(item.Errors) == 0) {
				t.Errorf("mutable capability %q has incomplete authoring contract", item.Key)
			}
		}
	}
}

func hasMutationRoute(routes []string) bool {
	for _, route := range routes {
		method, _, _ := strings.Cut(strings.TrimSpace(route), " ")
		if method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE" {
			return true
		}
	}
	return false
}

func TestConnectorOperationAuthoringContractPublishesEditorOptions(t *testing.T) {
	checks := map[string][]string{
		"method": integrationcontract.RuntimeConnectorMethods(), "execution_mode": integrationcontract.RuntimeConnectorExecutionModes(),
		"side_effect": integrationcontract.RuntimeConnectorSideEffects(), "protocol_field_type": integrationcontract.RuntimeConnectorProtocolFieldTypes(),
	}
	contract := RuntimeAuthoringCapabilities()
	for key, expected := range checks {
		sort.Strings(expected)
		if actual := authoringParameterEnumForTest(t, contract, "integration.connector_operation", key); !reflect.DeepEqual(actual, expected) {
			t.Errorf("parameter %s=%v want=%v", key, actual, expected)
		}
	}
}

func TestRuntimeAuthoringCapabilitiesRejectUnknownDependencies(t *testing.T) {
	contract := RuntimeAuthoringCapabilities()
	contract.Domains[0].Capabilities[0].Requires = append(contract.Domains[0].Capabilities[0].Requires, "missing.capability")
	SortAuthoringContract(&contract)
	contract.ContractHash = ContractHash(contract)
	if err := contract.Validate(); err == nil {
		t.Fatal("unknown capability dependency must invalidate authoring contract")
	}
}

func TestRuntimeAuthoringCapabilitiesDoNotPublishActionStepDSL(t *testing.T) {
	for _, domain := range RuntimeAuthoringCapabilities().Domains {
		for _, item := range domain.Capabilities {
			if strings.HasPrefix(item.Key, "action.step.") {
				t.Fatalf("retired Action Step capability remains published: %s", item.Key)
			}
		}
	}
	automationInstructions := []string{}
	for _, domain := range RuntimeAuthoringCapabilities().Domains {
		for _, item := range domain.Capabilities {
			if strings.HasPrefix(item.Key, "automation.instruction.") {
				automationInstructions = append(automationInstructions, strings.TrimPrefix(item.Key, "automation.instruction."))
			}
		}
	}
	sort.Strings(automationInstructions)
	want := append([]string(nil), RuntimeAutomationCapabilities().InstructionTypes...)
	sort.Strings(want)
	if !reflect.DeepEqual(automationInstructions, want) {
		t.Fatalf("automation instruction contract drifted: got=%v want=%v", automationInstructions, want)
	}
}

func authoringParameterEnumForTest(t *testing.T, contract capabilitycontract.CapabilityRuntimeAuthoringContract, capabilityKey, parameterKey string) []string {
	t.Helper()
	for _, domainContract := range contract.Domains {
		for _, item := range domainContract.Capabilities {
			if item.Key != capabilityKey {
				continue
			}
			for _, parameter := range item.Parameters {
				if parameter.Key == parameterKey {
					return parameter.Enum
				}
			}
		}
	}
	t.Fatalf("missing authoring parameter %s/%s", capabilityKey, parameterKey)
	return nil
}
