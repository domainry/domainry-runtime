package policy

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func workflowDefinitionAuthoringCapability(nodeTypes []string) capabilitycontract.CapabilityAuthoringDefinition {
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "workflow.definition", Status: "supported", Lifecycle: "versioned_metadata",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "expected_schema_hash", Type: "string"},
			{Key: "workflow", Type: "workflow_definition", Required: true},
		},
		Requires: []string{"workflow.graph_v2"}, Permissions: []string{"workspace.admin"},
		AuditEvents:        []string{"business_change_plan.item_applied"},
		ValidationEndpoint: "POST /workflows/{workflowKey}/validate", SimulationEndpoint: "POST /tenant-admin/change-plans/{planID}/simulate",
		ConfigurationRoutes: []string{
			"GET /metadata/definitions/workflow/{workflowKey}", "GET /metadata/definitions/workflow/{workflowKey}/versions", "POST /workflows/{workflowKey}/validate",
			"GET /domain-system-snapshot", "GET /domain-reference-graph", "GET /tenant-admin/change-plans/{planID}", "PUT /tenant-admin/change-plans/{planID}", "POST /tenant-admin/change-plans/{planID}/simulate",
			"POST /tenant-admin/change-plans/{planID}/review", "POST /tenant-admin/change-plans/{planID}/approve", "POST /tenant-admin/change-plans/apply",
		},
		ResourceKeyPathParameter: "workflowKey", SystemDraftResourceType: "workflow", FrontendSupportKey: "workflow.definition.editor.v1",
		InputSchema: workflowDefinitionInputSchema(nodeTypes), OutputSchema: workflowSystemDraftOutputSchema(),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{
			{Name: "plan_id", JSONPointer: "/plan_id", Type: "change_plan_id", VisibleTo: "subsequent_capability_calls"},
			{Name: "revision", JSONPointer: "/revision", Type: "integer", VisibleTo: "subsequent_capability_calls"},
		},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{
			{Kind: "object_key", InputJSONPointer: "/payload/trigger_contract/object_key", ResolverEndpoint: "GET /tenant-admin/platform-capabilities/references/object_key"},
			{Kind: "field_key", InputJSONPointer: "/payload/trigger_contract/field_key", ScopeFrom: "/payload/trigger_contract/object_key", ResolverEndpoint: "GET /tenant-admin/platform-capabilities/references/field_key"},
			{Kind: "action_key", InputJSONPointer: "/payload/graph/nodes/*/contract/action/action_key", ResolverEndpoint: "GET /tenant-admin/platform-capabilities/references/action_key"},
		},
		Execution: &capabilitycontract.CapabilityAuthoringExecution{
			ReadSet: []string{"metadata.published_snapshot", "changeplan.reference_graph", "action.definition", "identity.role", "schema.object"}, WriteSet: []string{"metadata.workflow_definition", "metadata.definition_version"},
			Transaction: "reviewed_change_plan_transaction", Idempotency: "idempotency_key_and_plan_revision", SideEffects: []string{"audit:business_change_plan.item_applied", "schema_snapshot_rebuild"},
			SideEffectLevel: "internal", Compensation: "restore_as_new_system_draft", PermissionModel: "workspace.admin_maker_checker", ChangeControl: "reviewed_system_draft_change_plan",
		},
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.workflow.definition_identity_required", FieldPath: "payload.key", MessageKey: "backend.workflow.definition_identity_required"},
			{Code: "backend.change_plan.resource_version_conflict", FieldPath: "expected_schema_hash", MessageKey: "backend.change_plan.resource_version_conflict"},
			{Code: "backend.workflow.graph_trigger_required", FieldPath: "payload.graph.nodes", MessageKey: "backend.workflow.graph_trigger_required"},
			{Code: "backend.change_plan.system_draft_required", FieldPath: "payload", MessageKey: "backend.change_plan.system_draft_required"},
		},
		Examples: workflowDefinitionExamples(),
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "validation", Path: "runtime/application/workflow/workflow_definition_lifecycle_application_service.go", Symbol: "WorkflowApplicationService.ValidateWorkflowDefinition"},
			{Kind: "service", Path: "runtime/application/changeplan/change_plan_application_service.go", Symbol: "BusinessChangePlanApplicationService.SaveDraft"},
		},
	}
}

func workflowDefinitionInputSchema(nodeTypes []string) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed,
		Required: []string{"payload"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"expected_schema_hash": {Type: "string"}, "payload": {Ref: "#/$defs/workflow_definition"},
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
		"timeout_seconds": {Type: "integer", Minimum: workflowAuthoringFloatPointer(0)}, "audit_event": {Type: "string"}, "graph": {Ref: "#/$defs/workflow_graph"},
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

func workflowSystemDraftOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed,
		Required: []string{"workspace_id", "plan_id", "revision", "status", "payload", "created_by", "updated_by", "created_at", "updated_at"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"workspace_id": {Type: "string"}, "plan_id": {Type: "string"}, "revision": {Type: "integer"}, "status": {Type: "string", Enum: []any{"draft", "in_review", "approved", "applying", "published"}},
			"payload": {Type: "object", AdditionalProperties: &open}, "created_by": {Type: "string"}, "updated_by": {Type: "string"},
			"created_at": {Type: "string", Format: "date-time"}, "updated_at": {Type: "string", Format: "date-time"},
		},
	}
}

func workflowDefinitionExamples() []capabilitycontract.CapabilityAuthoringExample {
	graphs := workflowGraphExamples()
	minimalWorkflow := map[string]any{"key": "order.complete", "name": "Complete order", "enabled": true, "trigger_contract": map[string]any{"type": "manual"}, "graph": graphs[0].Value}
	representativeWorkflow := map[string]any{"key": "order.approval", "name": "Order approval", "enabled": true, "trigger_contract": map[string]any{"type": "field_changed", "object_key": "order", "field_key": "status"}, "condition_contract": map[string]any{"type": "field_equals", "field": "status", "value": "submitted"}, "idempotency_keys": []any{"record_id"}, "retry": map[string]any{"max_attempts": 3, "delay_seconds": 5}, "timeout_seconds": 86400, "audit_event": "order.approval.completed", "graph": graphs[1].Value}
	invalidWorkflow := map[string]any{"key": "order.invalid", "name": "Invalid workflow", "enabled": true, "trigger_contract": map[string]any{"type": "manual"}, "graph": graphs[2].Value}
	return []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: map[string]any{"payload": minimalWorkflow}},
		{Name: "representative", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": representativeWorkflow}},
		{Name: "invalid_with_repair", Value: map[string]any{"payload": invalidWorkflow}, ExpectedErrorCodes: []string{"backend.workflow.graph_trigger_required"}},
	}
}
