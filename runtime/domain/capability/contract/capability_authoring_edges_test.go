package contract

import (
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeAuthoringErrorContractMappings(t *testing.T) {
	for name, values := range map[string][]string{
		"types": RuntimeConnectorTypes(), "methods": RuntimeConnectorMethods(), "execution_modes": RuntimeConnectorExecutionModes(),
		"side_effects": RuntimeConnectorSideEffects(), "field_types": RuntimeConnectorProtocolFieldTypes(),
		"connection_statuses": RuntimeIntegrationConnectionStatuses(), "outbox_statuses": RuntimeIntegrationOutboxStatuses(),
	} {
		if len(values) == 0 {
			t.Fatalf("%s inventory is empty", name)
		}
	}
	tests := map[string]string{
		"backend.action.invalid":                                    "action.definition",
		"backend.automation.invalid":                                "automation.rule",
		"backend.workflow.invalid":                                  "workflow.graph_v2",
		"backend.scheduler.invalid":                                 "scheduler.business_job",
		"backend.report.invalid":                                    "report.definition",
		"backend.change_plan.invalid":                               "",
		"backend.integration.binding.invalid":                       "integration.binding_validation",
		"backend.integration.connector.operation_invalid":           "integration.connector_operation",
		"backend.integration.connector.protocol_field_invalid":      "integration.connector_operation",
		"backend.integration.connector.compensation_invalid":        "integration.connector_operation",
		"backend.integration.connector.reserve_contract_incomplete": "integration.connector_operation",
		"backend.integration.connector.invalid":                     "integration.connector_definition",
		"backend.integration.connection.invalid":                    "integration.connection",
		"backend.integration.secret_missing":                        "integration.connection",
		"backend.integration.webhook_signature.invalid":             "integration.connection",
		"backend.integration.outbox.invalid":                        "integration.outbox",
		"backend.unknown":                                           "",
	}
	for code, want := range tests {
		result := RuntimeAuthoringErrorContract(" "+code+" ", map[string]string{"field": " object.key "})
		if result.CapabilityKey != want || result.FieldPath != "object.key" || result.ContractVersion != RuntimeAuthoringContractVersion {
			t.Fatalf("code=%q result=%#v want capability=%q", code, result, want)
		}
	}
	resources := map[string]string{"field": "schema.field", "action": "action.definition", "automation_rule": "automation.rule", "connector": "integration.connector_definition", "report": "report.definition", "unknown": ""}
	for resource, want := range resources {
		result := RuntimeAuthoringErrorContract("backend.metadata.definition_version_conflict", map[string]string{"resource_type": " " + resource + " ", "parameter_path": "definition.version"})
		if result.CapabilityKey != want || result.FieldPath != "definition.version" {
			t.Fatalf("resource=%q result=%#v", resource, result)
		}
	}
	if got := runtimeErrorFieldPath(map[string]string{"field_path": " ", "path": "fallback.path"}); got != "fallback.path" {
		t.Fatalf("fallback path = %q", got)
	}
}

func TestCapabilityAuthoringValidationRejectsEveryStructuralViolation(t *testing.T) {
	valid := validAuthoringContractForTest()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid contract: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*CapabilityRuntimeAuthoringContract)
		want   string
	}{
		{name: "missing identity", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.RuntimeVersion = "" }, want: "requires contract_version"},
		{name: "missing contract version", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.ContractVersion = "" }, want: "requires contract_version"},
		{name: "missing contract hash", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.ContractHash = "" }, want: "requires contract_version"},
		{name: "stale hash", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.RuntimeVersion = "changed" }, want: "hash is stale"},
		{name: "empty domains", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains = nil }, want: "requires domains"},
		{name: "blank domain", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains[0].Key = "" }, want: "blank or duplicated"},
		{name: "duplicate domain", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains = append(c.Domains, c.Domains[0]) }, want: "blank or duplicated"},
		{name: "blank capability", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains[0].Capabilities[0].Key = "" }, want: "capability"},
		{name: "duplicate capability", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities = append(c.Domains[0].Capabilities, c.Domains[0].Capabilities[0])
		}, want: "blank or duplicated"},
		{name: "wrong domain", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains[0].Capabilities[0].Key = "other.create" }, want: "outside domain"},
		{name: "unsupported", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains[0].Capabilities[0].Status = "planned" }, want: "supported status"},
		{name: "blank lifecycle", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains[0].Capabilities[0].Lifecycle = "" }, want: "supported status"},
		{name: "missing source", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains[0].Capabilities[0].Sources = nil }, want: "supported status"},
		{name: "blank parameter", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains[0].Capabilities[0].Parameters[0].Key = "" }, want: "invalid parameter"},
		{name: "blank parameter type", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains[0].Capabilities[0].Parameters[0].Type = "" }, want: "invalid parameter"},
		{name: "duplicate parameter", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities[0].Parameters = append(c.Domains[0].Capabilities[0].Parameters, c.Domains[0].Capabilities[0].Parameters[0])
		}, want: "invalid parameter"},
		{name: "unsorted enum", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities[0].Parameters[0].Enum = []string{"z", "a"}
		}, want: "enum is not sorted"},
		{name: "inverted range", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities[0].Parameters[0].Minimum = floatPointer(2)
			c.Domains[0].Capabilities[0].Parameters[0].Maximum = floatPointer(1)
		}, want: "inverted range"},
		{name: "invalid error code", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities[0].Errors[0].MessageKey = "different"
		}, want: "invalid error contract"},
		{name: "blank error code", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities[0].Errors[0].Code = ""
		}, want: "invalid error contract"},
		{name: "missing error path", mutate: func(c *CapabilityRuntimeAuthoringContract) { c.Domains[0].Capabilities[0].Errors[0].FieldPath = "" }, want: "requires field_path"},
		{name: "expected without actual", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities[0].Errors[0].ParameterKeys = []string{"minimum"}
		}, want: "without actual"},
		{name: "self require", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities[0].Requires = []string{"schema.create"}
		}, want: "requires unknown or self"},
		{name: "unknown require", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities[0].Requires = []string{"schema.missing"}
		}, want: "requires unknown or self"},
		{name: "self conflict", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities[0].Conflicts = []string{"schema.create"}
		}, want: "conflicts with unknown or self"},
		{name: "unknown conflict", mutate: func(c *CapabilityRuntimeAuthoringContract) {
			c.Domains[0].Capabilities[0].Conflicts = []string{"schema.missing"}
		}, want: "conflicts with unknown or self"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := validAuthoringContractForTest()
			test.mutate(&contract)
			if test.name != "stale hash" && test.name != "missing identity" && test.name != "missing contract version" && test.name != "missing contract hash" {
				contract.ContractHash = authoringContractHash(contract)
			}
			if err := contract.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
	knownConflict := validAuthoringContractForTest()
	second := knownConflict.Domains[0].Capabilities[0]
	second.Key = "schema.update"
	knownConflict.Domains[0].Capabilities = append(knownConflict.Domains[0].Capabilities, second)
	knownConflict.Domains[0].Capabilities[0].Conflicts = []string{"schema.update"}
	knownConflict.ContractHash = authoringContractHash(knownConflict)
	if err := knownConflict.Validate(); err != nil {
		t.Fatalf("known conflict contract: %v", err)
	}
	knownRequirement := validAuthoringContractForTest()
	secondRequired := knownRequirement.Domains[0].Capabilities[0]
	secondRequired.Key = "schema.update"
	knownRequirement.Domains[0].Capabilities = append(knownRequirement.Domains[0].Capabilities, secondRequired)
	knownRequirement.Domains[0].Capabilities[0].Requires = []string{"schema.update"}
	knownRequirement.ContractHash = authoringContractHash(knownRequirement)
	if err := knownRequirement.Validate(); err != nil {
		t.Fatalf("known requirement contract: %v", err)
	}
	minimumOnly := validAuthoringContractForTest()
	minimumOnly.Domains[0].Capabilities[0].Parameters[0].Maximum = nil
	minimumOnly.ContractHash = authoringContractHash(minimumOnly)
	if err := minimumOnly.Validate(); err != nil {
		t.Fatalf("minimum-only parameter contract: %v", err)
	}
	unconstrained := validAuthoringContractForTest()
	unconstrained.Domains[0].Capabilities[0].Parameters[0].Enum = nil
	unconstrained.Domains[0].Capabilities[0].Parameters[0].Minimum = nil
	unconstrained.Domains[0].Capabilities[0].Parameters[0].Maximum = nil
	unconstrained.Domains[0].Capabilities[0].Errors[0].ParameterKeys = []string{"actual"}
	unconstrained.ContractHash = authoringContractHash(unconstrained)
	if err := unconstrained.Validate(); err != nil {
		t.Fatalf("unconstrained parameter contract: %v", err)
	}
}

func TestCapabilityAuthoringEnumsPointersAndHashes(t *testing.T) {
	if *floatPointer(1.5) != 1.5 || *intPointer(3) != 3 {
		t.Fatal("pointer helpers changed values")
	}
	for name, values := range map[string][]string{
		"protocol fields": RuntimeConnectorProtocolFieldTypes(),
		"connections":     RuntimeIntegrationConnectionStatuses(),
		"outbox":          RuntimeIntegrationOutboxStatuses(),
	} {
		if len(values) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
	instance := CapabilityAuthoringInstance{ObjectKeys: []string{"object"}}
	if CapabilityAuthoringInstanceHash(instance) != authoringInstanceHash(instance) || len(authoringInstanceHash(instance)) != 64 {
		t.Fatal("instance hash is unstable")
	}
	contract := validAuthoringContractForTest()
	copy := contract
	copy.ContractHash = "ignored"
	copy.InstanceHash = "ignored"
	copy.Instance = instance
	if ContractHash(copy) != ContractHash(contract) {
		t.Fatal("contract hash included derived fields")
	}
	if !reflect.DeepEqual(RuntimeConnectorMethods(), []string{"DELETE", "GET", "PATCH", "POST", "PUT", "SMTP"}) {
		t.Fatal("connector method contract changed")
	}
	unsorted := validAuthoringContractForTest()
	unsorted.Domains[0].Capabilities[0].Examples = []CapabilityAuthoringExample{
		{Name: "second", Value: map[string]any{"order": 2}},
		{Name: "first", Value: map[string]any{"order": 1}},
	}
	SortAuthoringContract(&unsorted)
	if names := []string{unsorted.Domains[0].Capabilities[0].Examples[0].Name, unsorted.Domains[0].Capabilities[0].Examples[1].Name}; !reflect.DeepEqual(names, []string{"first", "second"}) {
		t.Fatalf("authoring examples are not sorted deterministically: %v", names)
	}
}

func TestAuthoringSchemaReferenceTraversalAcceptsResolvedGenericShapes(t *testing.T) {
	closed := false
	root := &CapabilityAuthoringSchema{
		Definitions: map[string]CapabilityAuthoringSchema{"value": {Type: "string"}},
		Properties: map[string]CapabilityAuthoringSchema{
			"reference":    {Ref: "#/$defs/value"},
			"empty_object": {Type: "object"},
		},
		Items:                &CapabilityAuthoringSchema{Type: "string"},
		OneOf:                []CapabilityAuthoringSchema{{Type: "string"}},
		AdditionalProperties: &closed,
	}
	if err := validateAuthoringSchemaReferences(root, root); err != nil {
		t.Fatalf("resolved schema traversal: %v", err)
	}
}

func TestAuthoringSchemaReferenceTraversalRejectsUnresolvedNestedShapes(t *testing.T) {
	closed := false
	tests := []struct {
		name   string
		schema CapabilityAuthoringSchema
		want   string
	}{
		{name: "root reference", schema: CapabilityAuthoringSchema{Ref: "#/$defs/missing"}, want: "unresolved"},
		{
			name: "open declared object",
			schema: CapabilityAuthoringSchema{
				Type:       "object",
				Properties: map[string]CapabilityAuthoringSchema{"value": {Type: "string"}},
			},
			want: "not closed",
		},
		{
			name: "item",
			schema: CapabilityAuthoringSchema{
				Items: &CapabilityAuthoringSchema{Ref: "#/$defs/missing"},
			},
			want: "unresolved",
		},
		{
			name: "property",
			schema: CapabilityAuthoringSchema{
				Properties:           map[string]CapabilityAuthoringSchema{"value": {Ref: "#/$defs/missing"}},
				AdditionalProperties: &closed,
			},
			want: `property "value"`,
		},
		{
			name: "definition",
			schema: CapabilityAuthoringSchema{
				Definitions: map[string]CapabilityAuthoringSchema{"nested": {Ref: "#/$defs/missing"}},
			},
			want: `definition "nested"`,
		},
		{
			name: "oneOf",
			schema: CapabilityAuthoringSchema{
				OneOf: []CapabilityAuthoringSchema{{Ref: "#/$defs/missing"}},
			},
			want: "oneOf[0]",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := test.schema
			if err := validateAuthoringSchemaReferences(&root, &root); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}

	explicitlyClosed := CapabilityAuthoringSchema{
		Type:                 "object",
		Properties:           map[string]CapabilityAuthoringSchema{"value": {Type: "string"}},
		AdditionalProperties: &closed,
	}
	if err := validateAuthoringSchemaReferences(&explicitlyClosed, &explicitlyClosed); err != nil {
		t.Fatalf("explicitly closed object: %v", err)
	}
}

func validAuthoringContractForTest() CapabilityRuntimeAuthoringContract {
	contract := CapabilityRuntimeAuthoringContract{
		ContractVersion: RuntimeAuthoringContractVersion,
		RuntimeVersion:  "test",
		Domains: []CapabilityAuthoringDomain{
			{Key: "schema", Capabilities: []CapabilityAuthoringDefinition{
				{
					Key: "schema.create", Status: "supported", Lifecycle: "stable",
					Parameters: []CapabilityAuthoringParameter{{Key: "kind", Type: "string", Enum: []string{"a", "z"}, Minimum: floatPointer(1), Maximum: floatPointer(2), MinLength: intPointer(1), MaxLength: intPointer(10)}},
					Errors:     []CapabilityAuthoringError{{Code: "backend.schema.invalid", MessageKey: "backend.schema.invalid", FieldPath: "kind", ParameterKeys: []string{"expected", "actual"}}},
					Sources:    []CapabilityAuthoringSource{{Kind: "go", Path: "schema.go"}},
				},
			}},
		},
	}
	contract.ContractHash = authoringContractHash(contract)
	return contract
}
