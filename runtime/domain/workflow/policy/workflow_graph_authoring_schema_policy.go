package policy

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func workflowCompleteGraphAuthoringContract(capability *capabilitycontract.CapabilityAuthoringDefinition, nodeTypes []string) {
	capability.Permissions = []string{workflowAuthoringFragmentAction}
	capability.ValidationEndpoint = workflowAuthoringFragmentValidationEndpoint
	capability.InputSchema = workflowGraphInputSchema(nodeTypes)
	capability.OutputSchema = workflowValidationOutputSchema()
	capability.OutputVariables = workflowValidationOutputVariables()
	capability.Execution = &capabilitycontract.CapabilityAuthoringExecution{
		ReadSet: []string{"metadata.workflow_candidate", "action.definition", "identity.role", "schema.object"}, Transaction: "read_only_validation",
		Idempotency: "naturally_idempotent", SideEffectLevel: "none", PermissionModel: workflowAuthoringFragmentAction,
	}
	capability.ReferenceContracts = []capabilitycontract.CapabilityAuthoringReference{
		{Kind: "action_key", InputJSONPointer: "/nodes/*/contract/action/action_key", ResolverEndpoint: "/discovery/references/action_key"},
		{Kind: "role_key", InputJSONPointer: "/nodes/*/contract/approval/resolvers/*/role_key", ResolverEndpoint: "/discovery/references/role_key"},
		{Kind: "user_id", InputJSONPointer: "/nodes/*/contract/approval/resolvers/*/user_ids/*", ResolverEndpoint: "/discovery/references/user_id"},
		{Kind: "role_key", InputJSONPointer: "/nodes/*/contract/approval/route/eligible_roles/*", ResolverEndpoint: "/discovery/references/role_key"},
	}
	capability.Errors = append(capability.Errors,
		capabilitycontract.CapabilityAuthoringError{Code: "backend.workflow.graph_trigger_required", FieldPath: "nodes", MessageKey: "backend.workflow.graph_trigger_required"},
		capabilitycontract.CapabilityAuthoringError{Code: "backend.workflow.graph_edge_invalid", FieldPath: "edges", MessageKey: "backend.workflow.graph_edge_invalid"},
		capabilitycontract.CapabilityAuthoringError{Code: "backend.workflow.timer_contract_invalid", FieldPath: "nodes[].contract.timer", MessageKey: "backend.workflow.timer_contract_invalid"},
	)
	capability.Examples = workflowGraphExamples()
}

func workflowGraphInputSchema(nodeTypes []string) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	node := capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/workflow_graph_node"}
	edge := capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/workflow_graph_edge"}
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed,
		Required: []string{"nodes", "edges"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"nodes": {Type: "array", Items: &node, MinItems: workflowAuthoringIntPointer(1)},
			"edges": {Type: "array", Items: &edge}, "viewport": workflowGraphViewportSchema(),
		}, Definitions: workflowGraphSchemaDefinitions(nodeTypes),
	}
}

func workflowGraphSchemaDefinitions(nodeTypes []string) map[string]capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	definitions := workflowComponentSchemaDefinitions()
	definitions["workflow_approval"] = workflowGraphApprovalSchema()
	definitions["workflow_action"] = workflowGraphActionSchema()
	definitions["workflow_cc"] = workflowGraphCCSchema()
	definitions["workflow_timer"] = workflowGraphTimerSchema()
	definitions["workflow_node_contract"] = capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"condition": {Ref: "#/$defs/workflow_condition"}, "approval": {Ref: "#/$defs/workflow_approval"}, "action": {Ref: "#/$defs/workflow_action"},
		"cc": {Ref: "#/$defs/workflow_cc"}, "timer": {Ref: "#/$defs/workflow_timer"},
	}}
	definitions["workflow_graph_node"] = capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"id", "type"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"id": {Type: "string"}, "type": {Type: "string", Enum: workflowAuthoringStringEnums(nodeTypes)}, "name": {Type: "string"},
		"i18n": workflowGraphOpenObjectSchema(), "position": workflowGraphPositionSchema(), "config": workflowGraphOpenObjectSchema(), "contract": {Ref: "#/$defs/workflow_node_contract"},
	}}
	definitions["workflow_graph_edge"] = capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"id", "source", "target"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"id": {Type: "string"}, "source": {Type: "string"}, "target": {Type: "string"}, "branch": {Type: "string"}, "label": {Type: "string"},
	}}
	return definitions
}

func workflowGraphApprovalSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	resolver := capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/workflow_assignee_resolver"}
	schema := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"mode"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"mode": {Type: "string", Enum: []any{"all", "any", "sequential", "quorum"}}, "title": {Type: "string"}, "resolvers": {Type: "array", Items: &resolver},
		"required_approvals": {Type: "integer", Minimum: workflowAuthoringFloatPointer(1)},
		"resolver_mode":      {Type: "string", Enum: []any{"first_match", "union"}}, "empty_assignee_policy": {Type: "string", Enum: []any{"admin", "fail", "skip"}},
		"due_seconds": {Type: "integer", Minimum: workflowAuthoringFloatPointer(0)}, "reminder_action_key": {Type: "string"}, "reminder_input": workflowGraphOpenObjectSchema(),
		"escalation_seconds": {Type: "integer", Minimum: workflowAuthoringFloatPointer(0)}, "escalation_resolvers": {Type: "array", Items: &resolver},
		"route": workflowGraphApprovalRouteSchema(),
	}}
	workflowApprovalElectorateSchemaCondition(&schema)
	workflowApprovalQuorumSchemaCondition(&schema)
	return schema
}

// workflowApprovalElectorateSchemaCondition keeps the closed contract honest
// while admitting both electorate authorities: a template-fixed node needs at
// least one resolver, a route-driven node needs the route and ignores the
// node-level mode and threshold, and no node may declare both.
func workflowApprovalElectorateSchemaCondition(schema *capabilitycontract.CapabilityAuthoringSchema) {
	resolver := capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/workflow_assignee_resolver"}
	schema.OneOf = []capabilitycontract.CapabilityAuthoringSchema{
		{Required: []string{"resolvers"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"resolvers": {Type: "array", Items: &resolver, MinItems: workflowAuthoringIntPointer(1)}}},
		{Required: []string{"route"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"resolvers": {Type: "array", Items: &resolver, MaxItems: workflowAuthoringIntPointer(0)}}},
	}
}

// workflowGraphApprovalRouteSchema is the per-instance approval route envelope.
// Only the bounds live in the definition; the concrete steps are staged by the
// initiating Action and are never authored here.
func workflowGraphApprovalRouteSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	role := capabilitycontract.CapabilityAuthoringSchema{Type: "string"}
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"source"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"source":                   {Type: "string", Enum: []any{"instance"}},
		"min_steps":                {Type: "integer", Minimum: workflowAuthoringFloatPointer(1)},
		"max_steps":                {Type: "integer", Minimum: workflowAuthoringFloatPointer(1)},
		"max_assignees_per_step":   {Type: "integer", Minimum: workflowAuthoringFloatPointer(1)},
		"eligible_roles":           {Type: "array", Items: &role},
		"deferred_steps":           {Type: "string", Enum: []any{"allow", "deny"}, Default: "deny"},
		"deferred_configurer":      {Type: "string", Enum: []any{"previous_step_approver", "initiator"}, Default: "previous_step_approver"},
		"revalidate_on_activation": {Type: "string", Enum: []any{"fail", "skip_invalid", "admin"}, Default: "fail"},
	}}
}

func workflowApprovalQuorumSchemaCondition(schema *capabilitycontract.CapabilityAuthoringSchema) {
	schema.If = &capabilitycontract.CapabilityAuthoringSchema{Required: []string{"mode"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"mode": {Const: "quorum"}}}
	schema.Then = &capabilitycontract.CapabilityAuthoringSchema{Required: []string{"required_approvals"}}
	// Other modes have no quorum setting. Combined with the positive property
	// minimum this rejects a supplied threshold outside quorum mode.
	schema.Else = &capabilitycontract.CapabilityAuthoringSchema{Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"required_approvals": {Maximum: workflowAuthoringFloatPointer(0)}}}
}

func workflowGraphActionSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"action_key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"action_key": {Type: "string"}, "object_key": {Type: "string"}, "record_id": {Type: "string"}, "input": workflowGraphOpenObjectSchema(),
		"output_variable": {Type: "string"}, "timeout_seconds": {Type: "integer", Minimum: workflowAuthoringFloatPointer(0)}, "retry": {Ref: "#/$defs/workflow_retry_policy"},
		"on_error": {Type: "string", Enum: []any{"continue", "error_branch", "fail"}},
	}}
}

func workflowGraphCCSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	resolver := capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/workflow_assignee_resolver"}
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"notification_action_key", "resolvers"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"notification_action_key": {Type: "string"}, "resolvers": {Type: "array", Items: &resolver, MinItems: workflowAuthoringIntPointer(1)}, "input": workflowGraphOpenObjectSchema(),
	}}
}

func workflowGraphTimerSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"timer_key": {Type: "string"}, "purpose": {Type: "string"}, "at": {Type: "string", Format: "date-time"}, "duration_seconds": {Type: "integer", Minimum: workflowAuthoringFloatPointer(1)},
		"source_field": {Type: "string"}, "offset_seconds": {Type: "integer"}, "timezone": {Type: "string", Default: "UTC"}, "business_calendar_key": {Type: "string"},
	}}
}

func workflowGraphPositionSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"x": {Type: "number"}, "y": {Type: "number"}}}
}

func workflowGraphViewportSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"x": {Type: "number"}, "y": {Type: "number"}, "zoom": {Type: "number"}}}
}

func workflowGraphOpenObjectSchema() capabilitycontract.CapabilityAuthoringSchema {
	open := true
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}
}

func workflowAuthoringStringEnums(values []string) []any {
	result := make([]any, len(values))
	for index := range values {
		result[index] = values[index]
	}
	return result
}

func workflowGraphExamples() []capabilitycontract.CapabilityAuthoringExample {
	return []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: map[string]any{"nodes": []any{map[string]any{"id": "trigger", "type": "trigger"}, map[string]any{"id": "complete", "type": "action", "contract": map[string]any{"action": map[string]any{"action_key": "order.complete"}}}}, "edges": []any{map[string]any{"id": "start", "source": "trigger", "target": "complete"}}}},
		{Name: "representative", Value: map[string]any{"nodes": []any{map[string]any{"id": "trigger", "type": "trigger", "position": map[string]any{"x": 0, "y": 0}}, map[string]any{"id": "approval", "type": "approval", "contract": map[string]any{"approval": map[string]any{"mode": "any", "resolvers": []any{map[string]any{"type": "role", "role_key": "finance_manager"}}, "empty_assignee_policy": "fail"}}}, map[string]any{"id": "approved", "type": "action", "contract": map[string]any{"action": map[string]any{"action_key": "order.complete"}}}, map[string]any{"id": "rejected", "type": "action", "contract": map[string]any{"action": map[string]any{"action_key": "order.reject"}}}}, "edges": []any{map[string]any{"id": "start", "source": "trigger", "target": "approval"}, map[string]any{"id": "approved", "source": "approval", "target": "approved", "branch": "approved"}, map[string]any{"id": "rejected", "source": "approval", "target": "rejected", "branch": "rejected"}}, "viewport": map[string]any{"x": 0, "y": 0, "zoom": 1}}},
		{Name: "invalid_with_repair", Value: map[string]any{"nodes": []any{map[string]any{"id": "complete", "type": "action", "contract": map[string]any{"action": map[string]any{"action_key": "order.complete"}}}}, "edges": []any{}}, ExpectedErrorCodes: []string{"backend.workflow.graph_trigger_required"}},
	}
}
