package policy

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func workflowDefinitionAuthoringCapability(nodeTypes []string) capabilitycontract.CapabilityAuthoringDefinition {
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "workflow.definition", Status: "supported", Lifecycle: "source_controlled_json",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "workflow", Type: "workflow_definition", Required: true},
		},
		Requires: []string{"workflow.graph_v2"}, Permissions: []string{"runtime.workflows.validate_workflow_definition"},
		ValidationEndpoint: "POST /workflow/definitions/{workflowKey}/validate",
		ConfigurationRoutes: []string{
			"GET /metadata/definitions/workflow/{workflowKey}", "POST /workflow/definitions/{workflowKey}/validate", "POST /workflow/definitions/{workflowKey}/simulate",
			"GET /authoring/snapshot", "GET /references",
		},
		ResourceKeyPathParameter: "workflowKey", InputSchema: workflowDefinitionInputSchema(nodeTypes),
		OutputSchema:    workflowValidationOutputSchema(),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "valid", JSONPointer: "/valid", Type: "boolean", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{
			{Kind: "object_key", InputJSONPointer: "/payload/trigger_contract/object_key", ResolverEndpoint: "GET /discovery/references/object_key"},
			{Kind: "field_key", InputJSONPointer: "/payload/trigger_contract/field_key", ScopeFrom: "/payload/trigger_contract/object_key", ResolverEndpoint: "GET /discovery/references/field_key"},
			{Kind: "action_key", InputJSONPointer: "/payload/graph/nodes/*/contract/action/action_key", ResolverEndpoint: "GET /discovery/references/action_key"},
		},
		Execution: &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"metadata.published_snapshot", "action.definition", "identity.role", "schema.object"}, Transaction: "read_only_candidate_validation", Idempotency: "naturally_idempotent_at_candidate_hash", SideEffectLevel: "none", PermissionModel: "runtime.workflows.validate_workflow_definition", ChangeControl: "source_controlled_json"},
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.workflow.definition_identity_required", FieldPath: "payload.key", MessageKey: "backend.workflow.definition_identity_required"},
			{Code: "backend.workflow.graph_trigger_required", FieldPath: "payload.graph.nodes", MessageKey: "backend.workflow.graph_trigger_required"},
		},
		Examples: workflowDefinitionExamples(),
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "validation", Path: "runtime/application/workflow/workflow_definition_lifecycle_application_service.go", Symbol: "WorkflowApplicationService.ValidateWorkflowDefinition"},
		},
	}
}

func workflowDefinitionInputSchema(nodeTypes []string) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed,
		Required: []string{"payload"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"payload": {Ref: "#/$defs/workflow_definition"},
		}, Definitions: workflowDefinitionSchemaDefinitions(nodeTypes),
	}
}

func workflowDefinitionSchemaDefinitions(nodeTypes []string) map[string]capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	definitions := workflowGraphSchemaDefinitions(nodeTypes)
	definitions["workflow_trigger"] = workflowComponentSchemaForKey("workflow.trigger_contract")
	definitions["workflow_definition"] = capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "name", "trigger_contract", "graph"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string"}, "name": {Type: "string"}, "i18n": workflowGraphOpenObjectSchema(), "enabled": {Type: "boolean"},
		"trigger_contract": {Ref: "#/$defs/workflow_trigger"}, "condition_contract": {Ref: "#/$defs/workflow_condition"},
		"run_as": {Type: "string"}, "idempotency_keys": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}},
		"retry": {Ref: "#/$defs/workflow_retry_policy"}, "dead_letter_policy": workflowGraphOpenObjectSchema(),
		"timeout_seconds": {Type: "integer", Minimum: workflowAuthoringFloatPointer(0)}, "graph": {Ref: "#/$defs/workflow_graph"},
	}}
	graph := *workflowGraphInputSchema(nodeTypes)
	graph.Schema, graph.Definitions = "", nil
	definitions["workflow_graph"] = graph
	return definitions
}

func workflowComponentSchemaForKey(capabilityKey string) capabilitycontract.CapabilityAuthoringSchema {
	for _, capability := range workflowAuthoringComponentCapabilities() {
		if capability.Key == capabilityKey {
			// Component capabilities are constructed with an input schema in the
			// owner-controlled table returned above.
			schema := *capability.InputSchema
			schema.Schema, schema.Definitions = "", nil
			return schema
		}
	}
	return capabilitycontract.CapabilityAuthoringSchema{}
}

func workflowDefinitionExamples() []capabilitycontract.CapabilityAuthoringExample {
	graphs := workflowGraphExamples()
	minimalWorkflow := map[string]any{"key": "order.complete", "name": "Complete order", "enabled": true, "trigger_contract": map[string]any{"type": "manual"}, "graph": graphs[0].Value}
	representativeWorkflow := map[string]any{"key": "order.approval", "name": "Order approval", "enabled": true, "trigger_contract": map[string]any{"type": "field_changed", "object_key": "order", "field_key": "status"}, "condition_contract": map[string]any{"type": "field_equals", "field": "status", "value": "submitted"}, "idempotency_keys": []any{"record_id"}, "retry": map[string]any{"max_attempts": 3, "delay_seconds": 5}, "timeout_seconds": 86400, "graph": graphs[1].Value}
	invalidWorkflow := map[string]any{"key": "order.invalid", "name": "Invalid workflow", "enabled": true, "trigger_contract": map[string]any{"type": "manual"}, "graph": graphs[2].Value}
	return []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: map[string]any{"payload": minimalWorkflow}},
		{Name: "representative", Value: map[string]any{"payload": representativeWorkflow}},
		{Name: "invalid_with_repair", Value: map[string]any{"payload": invalidWorkflow}, ExpectedErrorCodes: []string{"backend.workflow.graph_trigger_required"}},
	}
}
