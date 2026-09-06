package contract

import (
	"fmt"
	"sort"
	"strings"
)

func (contract CapabilityRuntimeAuthoringContract) Validate() error {
	if strings.TrimSpace(contract.ContractVersion) == "" || strings.TrimSpace(contract.RuntimeVersion) == "" || strings.TrimSpace(contract.ContractHash) == "" {
		return fmt.Errorf("authoring capability contract requires contract_version, runtime_version and contract_hash")
	}
	if contract.ContractHash != authoringContractHash(contract) {
		return fmt.Errorf("authoring capability contract hash is stale")
	}
	if len(contract.Domains) == 0 {
		return fmt.Errorf("authoring capability contract requires domains")
	}
	domainKeys := map[string]bool{}
	capabilityKeys := map[string]bool{}
	for _, domainContract := range contract.Domains {
		if strings.TrimSpace(domainContract.Key) == "" || domainKeys[domainContract.Key] {
			return fmt.Errorf("authoring capability domain %q is blank or duplicated", domainContract.Key)
		}
		domainKeys[domainContract.Key] = true
		for _, capability := range domainContract.Capabilities {
			if err := capability.validate(domainContract.Key, capabilityKeys); err != nil {
				return err
			}
			capabilityKeys[capability.Key] = true
		}
	}
	for _, domainContract := range contract.Domains {
		for _, capability := range domainContract.Capabilities {
			for _, required := range capability.Requires {
				if required == capability.Key || !capabilityKeys[required] {
					return fmt.Errorf("authoring capability %q requires unknown or self capability %q", capability.Key, required)
				}
			}
			for _, conflictKey := range capability.Conflicts {
				if conflictKey == capability.Key || !capabilityKeys[conflictKey] {
					return fmt.Errorf("authoring capability %q conflicts with unknown or self capability %q", capability.Key, conflictKey)
				}
			}
		}
	}
	return nil
}

func (capability CapabilityAuthoringDefinition) validate(domainKey string, known map[string]bool) error {
	if strings.TrimSpace(capability.Key) == "" || known[capability.Key] {
		return fmt.Errorf("authoring capability %q is blank or duplicated", capability.Key)
	}
	if !strings.HasPrefix(capability.Key, domainKey+".") {
		return fmt.Errorf("authoring capability %q is outside domain %q", capability.Key, domainKey)
	}
	if capability.Status != "supported" || strings.TrimSpace(capability.Lifecycle) == "" || len(capability.Sources) == 0 {
		return fmt.Errorf("authoring capability %q requires supported status, lifecycle and sources", capability.Key)
	}
	parameterKeys := map[string]bool{}
	for _, parameter := range capability.Parameters {
		if strings.TrimSpace(parameter.Key) == "" || strings.TrimSpace(parameter.Type) == "" || parameterKeys[parameter.Key] {
			return fmt.Errorf("authoring capability %q has invalid parameter %q", capability.Key, parameter.Key)
		}
		if len(parameter.Enum) > 0 && !sort.StringsAreSorted(parameter.Enum) {
			return fmt.Errorf("authoring capability %q parameter %q enum is not sorted", capability.Key, parameter.Key)
		}
		if parameter.Minimum != nil && parameter.Maximum != nil && *parameter.Minimum > *parameter.Maximum {
			return fmt.Errorf("authoring capability %q parameter %q has inverted range", capability.Key, parameter.Key)
		}
		parameterKeys[parameter.Key] = true
	}
	for _, contractError := range capability.Errors {
		if strings.TrimSpace(contractError.Code) == "" || contractError.MessageKey != contractError.Code {
			return fmt.Errorf("authoring capability %q has invalid error contract %q", capability.Key, contractError.Code)
		}
		if strings.TrimSpace(contractError.FieldPath) == "" {
			return fmt.Errorf("authoring capability %q error %q requires field_path", capability.Key, contractError.Code)
		}
		if authoringErrorDeclaresExpectedValue(contractError.ParameterKeys) && !authoringErrorParameterDeclared(contractError.ParameterKeys, "actual") {
			return fmt.Errorf("authoring capability %q error %q declares expected values without actual", capability.Key, contractError.Code)
		}
	}
	return nil
}

func validateAuthoringSchemaReferences(root, schema *CapabilityAuthoringSchema) error {
	if strings.HasPrefix(schema.Ref, "#/$defs/") {
		key := strings.TrimPrefix(schema.Ref, "#/$defs/")
		if _, exists := root.Definitions[key]; !exists {
			return fmt.Errorf("local definition %q is unresolved", key)
		}
	}
	if schema.Type == "object" && schema.AdditionalProperties == nil && len(schema.Properties) > 0 {
		return fmt.Errorf("object schema with declared properties is not closed")
	}
	if schema.Items != nil {
		if err := validateAuthoringSchemaReferences(root, schema.Items); err != nil {
			return err
		}
	}
	for _, nested := range []struct {
		keyword string
		schema  *CapabilityAuthoringSchema
	}{{"if", schema.If}, {"then", schema.Then}, {"else", schema.Else}, {"contains", schema.Contains}} {
		keyword, conditional := nested.keyword, nested.schema
		if conditional != nil {
			if err := validateAuthoringSchemaReferences(root, conditional); err != nil {
				return fmt.Errorf("%s: %w", keyword, err)
			}
		}
	}
	for key, property := range schema.Properties {
		property := property
		if err := validateAuthoringSchemaReferences(root, &property); err != nil {
			return fmt.Errorf("property %q: %w", key, err)
		}
	}
	for key, definition := range schema.Definitions {
		definition := definition
		if err := validateAuthoringSchemaReferences(root, &definition); err != nil {
			return fmt.Errorf("definition %q: %w", key, err)
		}
	}
	for index := range schema.OneOf {
		if err := validateAuthoringSchemaReferences(root, &schema.OneOf[index]); err != nil {
			return fmt.Errorf("oneOf[%d]: %w", index, err)
		}
	}
	return nil
}

func authoringErrorDeclaresExpectedValue(keys []string) bool {
	for _, key := range keys {
		switch key {
		case "allowed", "minimum", "maximum", "expected", "expected_type":
			return true
		}
	}
	return false
}

func authoringErrorParameterDeclared(keys []string, expected string) bool {
	for _, key := range keys {
		if key == expected {
			return true
		}
	}
	return false
}
