package policy

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func TestAutomationAuthoringDomainIsOwnerOwnedAndUsesRuntimeEnums(t *testing.T) {
	domain := AutomationAuthoringDomain()
	if domain.Key != "automation" || len(domain.Capabilities) != 8 {
		t.Fatalf("domain=%#v", domain)
	}
	catalog := capabilitycontract.RuntimeAutomationExecutionCatalog()
	instructionTypes := []string{}
	for _, capability := range domain.Capabilities {
		if strings.HasPrefix(capability.Key, "automation.instruction.") {
			instructionTypes = append(instructionTypes, strings.TrimPrefix(capability.Key, "automation.instruction."))
			if _, exists := capability.InputSchema.Properties["type"]; exists {
				t.Fatalf("route-derived instruction type leaked into %s input", capability.Key)
			}
		}
	}
	sort.Strings(instructionTypes)
	wantInstructionTypes := append([]string(nil), catalog.InstructionTypes...)
	sort.Strings(wantInstructionTypes)
	if !reflect.DeepEqual(instructionTypes, wantInstructionTypes) {
		t.Fatalf("instruction types=%v want=%v", instructionTypes, wantInstructionTypes)
	}
	trigger := domain.Capabilities[1]
	if got := automationAuthoringParameterEnum(t, trigger, "phase"); !reflect.DeepEqual(got, catalog.Phases) {
		t.Fatalf("phases=%v want=%v", got, catalog.Phases)
	}
}

func TestAutomationAuthoringSourceAnchorsExist(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	for _, capability := range AutomationAuthoringDomain().Capabilities {
		if len(capability.Sources) == 0 {
			t.Fatalf("capability %s has no sources", capability.Key)
		}
		for _, source := range capability.Sources {
			if _, err := os.Stat(filepath.Join(repositoryRoot, source.Path)); err != nil {
				t.Fatalf("capability %s source %s: %v", capability.Key, source.Path, err)
			}
		}
	}
}

func TestAutomationAuthoringContractsAreClosedAndExecutable(t *testing.T) {
	for _, capability := range AutomationAuthoringDomain().Capabilities {
		if capability.InputSchema == nil || capability.InputSchema.Schema != "https://json-schema.org/draft/2020-12/schema" || capability.InputSchema.Type != "object" || capability.InputSchema.AdditionalProperties == nil || *capability.InputSchema.AdditionalProperties {
			t.Fatalf("capability %s input schema is not closed draft 2020-12: %#v", capability.Key, capability.InputSchema)
		}
		if capability.OutputSchema == nil || capability.OutputSchema.Type != "object" || capability.OutputSchema.AdditionalProperties == nil || *capability.OutputSchema.AdditionalProperties {
			t.Fatalf("capability %s output schema is not closed: %#v", capability.Key, capability.OutputSchema)
		}
		if capability.Execution == nil || capability.Execution.Transaction == "" || capability.Execution.Idempotency == "" || capability.Execution.PermissionModel == "" || capability.Execution.SideEffectLevel == "" {
			t.Fatalf("capability %s execution contract is incomplete: %#v", capability.Key, capability.Execution)
		}
		if len(capability.Examples) != 3 || capability.Examples[0].Name != "minimal_valid" || capability.Examples[1].Name != "representative" || capability.Examples[2].Name != "invalid_with_repair" || len(capability.Examples[2].ExpectedErrorCodes) == 0 {
			t.Fatalf("capability %s examples are incomplete: %#v", capability.Key, capability.Examples)
		}
		parameterKeys := map[string]bool{}
		for _, parameter := range capability.Parameters {
			parameterKeys[parameter.Key] = true
			property, exists := capability.InputSchema.Properties[parameter.Key]
			if !exists {
				t.Fatalf("capability %s parameter %s is absent from input schema", capability.Key, parameter.Key)
			}
			if parameter.Required != automationAuthoringContains(capability.InputSchema.Required, parameter.Key) {
				t.Fatalf("capability %s parameter %s required=%v differs from schema", capability.Key, parameter.Key, parameter.Required)
			}
			if len(parameter.Enum) > 0 && !reflect.DeepEqual(automationAuthoringAnyStrings(property.Enum), parameter.Enum) {
				t.Fatalf("capability %s parameter %s enum=%v differs from schema=%v", capability.Key, parameter.Key, parameter.Enum, property.Enum)
			}
		}
		for propertyKey := range capability.InputSchema.Properties {
			if !parameterKeys[propertyKey] {
				t.Fatalf("capability %s schema property %s is absent from compact parameters", capability.Key, propertyKey)
			}
		}
	}
}

func automationAuthoringContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func automationAuthoringAnyStrings(values []any) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, _ := value.(string)
		result = append(result, text)
	}
	return result
}

func automationAuthoringParameterEnum(t *testing.T, capability capabilitycontract.CapabilityAuthoringDefinition, key string) []string {
	t.Helper()
	for _, parameter := range capability.Parameters {
		if parameter.Key == key {
			return parameter.Enum
		}
	}
	t.Fatalf("capability %s has no parameter %s", capability.Key, key)
	return nil
}
