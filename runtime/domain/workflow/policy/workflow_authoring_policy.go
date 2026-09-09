package policy

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

const (
	workflowAuthoringFragmentValidationEndpoint = "POST /workflow/authoring-fragments/{capabilityKey}/validate"
	workflowAuthoringFragmentAction             = "runtime.workflows.validate_authoring_fragment"
)

// WorkflowAuthoringDomain publishes Workflow-owned authoring contracts. The
// central Capability application only aggregates this stable owner projection.
func WorkflowAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	execution := capabilitycontract.RuntimeExecutionCapabilities()
	nodeTypes := make([]string, 0, len(execution.WorkflowNodes))
	for _, capability := range execution.WorkflowNodes {
		nodeTypes = append(nodeTypes, capability.Type)
	}
	graphCapability := capabilitycontract.CapabilityAuthoringDefinition{
		Key: "workflow.graph_v2", Status: "supported", Lifecycle: "draft_publish_immutable_version",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "nodes", Type: "array", Required: true, ItemSchema: "workflow_graph_node"},
			{Key: "edges", Type: "array", Required: true, ItemSchema: "workflow_graph_edge"},
			{Key: "viewport", Type: "object"},
		},
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.workflow.graph_node_invalid", FieldPath: "graph.nodes", ParameterKeys: []string{"node"}, MessageKey: "backend.workflow.graph_node_invalid"},
			{Code: "backend.workflow.graph_approval_mode_invalid", FieldPath: "graph.nodes[].contract.approval.mode", ParameterKeys: []string{"node"}, MessageKey: "backend.workflow.graph_approval_mode_invalid"},
			{Code: "backend.workflow.approval_required_approvals_invalid", FieldPath: "graph.nodes[].contract.approval.required_approvals", ParameterKeys: []string{"node"}, MessageKey: "backend.workflow.approval_required_approvals_invalid"},
			{Code: "backend.workflow.approval_resolver_invalid", FieldPath: "graph.nodes[].contract.approval.resolvers", ParameterKeys: []string{"node", "resolver"}, MessageKey: "backend.workflow.approval_resolver_invalid"},
		}, Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "validation", Path: "runtime/domain/workflow/policy/workflow_graph_policy.go", Symbol: "WorkflowValidateGraph"}, {Kind: "validation", Path: "runtime/domain/workflow/policy/workflow_node_contract_policy.go", Symbol: "WorkflowAssigneeResolverIsValid"}},
	}
	workflowCompleteGraphAuthoringContract(&graphCapability, nodeTypes)
	capabilities := []capabilitycontract.CapabilityAuthoringDefinition{workflowDefinitionAuthoringCapability(nodeTypes), graphCapability}
	capabilities = append(capabilities, workflowAuthoringComponentCapabilities()...)
	return capabilitycontract.CapabilityAuthoringDomain{Key: "workflow", Capabilities: capabilities}
}

func workflowAuthoringComponentCapabilities() []capabilitycontract.CapabilityAuthoringDefinition {
	workflowSource := capabilitycontract.CapabilityAuthoringSource{Kind: "domain", Path: "runtime/domain/definition/model/definition_model.go", Symbol: "WorkflowGraphSchema"}
	capabilities := []capabilitycontract.CapabilityAuthoringDefinition{
		{
			Key: "workflow.trigger_contract", Status: "supported", Lifecycle: "workflow_definition",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{
				{Key: "type", Type: "string", Required: true, Enum: []string{"action_completed", "field_changed", "manual", "record_created", "record_updated", "scheduled"}},
				{Key: "object_key", Type: "object_key"}, {Key: "object_keys", Type: "array", ItemSchema: "object_key"},
				{Key: "field_key", Type: "field_key", RequiredWhen: map[string]any{"type": "field_changed"}},
				{Key: "event", Type: "event_key", RequiredWhen: map[string]any{"type": "action_completed"}}, {Key: "offset", Type: "string"},
			}, Requires: []string{"workflow.graph_v2"}, ValidationEndpoint: workflowAuthoringFragmentValidationEndpoint,
			Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "validation", Path: "runtime/application/workflow/workflow_reference_validation_application_service.go", Symbol: "validateWorkflowTriggerContract"}},
		},
		{
			Key: "workflow.condition_contract", Status: "supported", Lifecycle: "workflow_definition",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{
				{Key: "type", Type: "string", Required: true, Enum: []string{"all", "always", "and", "any", "expression", "field_changed", "field_equals", "not", "or"}},
				{Key: "field", Type: "field_key", RequiredWhen: map[string]any{"type": []string{"field_changed", "field_equals"}}},
				{Key: "value", Type: "any"}, {Key: "expression", Type: "string", RequiredWhen: map[string]any{"type": "expression"}},
				{Key: "conditions", Type: "array", ItemSchema: "workflow_condition", RequiredWhen: map[string]any{"type": []string{"all", "and", "any", "or"}}},
				{Key: "condition", Type: "workflow_condition", RequiredWhen: map[string]any{"type": "not"}},
			}, Requires: []string{"workflow.graph_v2"}, ValidationEndpoint: workflowAuthoringFragmentValidationEndpoint,
			Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "validation", Path: "runtime/domain/workflow/policy/workflow_graph_policy.go", Symbol: "WorkflowConditionContractIsValid"}},
		},
		{
			Key: "workflow.assignee_resolver", Status: "supported", Lifecycle: "workflow_node",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{
				{Key: "type", Type: "string", Required: true, Enum: []string{"initiator_manager", "manager", "manager_of", "record_field", "role", "users"}},
				{Key: "priority", Type: "integer", Minimum: workflowAuthoringFloatPointer(1)}, {Key: "user_ids", Type: "array", ItemSchema: "user_id", RequiredWhen: map[string]any{"type": "users"}},
				{Key: "role_key", Type: "role_key", RequiredWhen: map[string]any{"type": "role"}}, {Key: "field", Type: "field_key", RequiredWhen: map[string]any{"type": "record_field"}},
				{Key: "user_field", Type: "field_key", RequiredWhen: map[string]any{"type": []string{"manager", "manager_of"}}},
			}, Requires: []string{"workflow.graph_v2"}, Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "validation", Path: "runtime/domain/workflow/policy/workflow_node_contract_policy.go", Symbol: "WorkflowAssigneeResolverIsValid"}},
		},
		{
			Key: "workflow.node.approval", Status: "supported", Lifecycle: "workflow_node",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{
				{Key: "mode", Type: "string", Required: true, Enum: []string{"all", "any", "sequential", "quorum"}},
				{Key: "required_approvals", Type: "integer", Minimum: workflowAuthoringFloatPointer(1), RequiredWhen: map[string]any{"mode": "quorum"}},
				{Key: "resolvers", Type: "array", ItemSchema: "workflow_assignee_resolver"}, {Key: "resolver_mode", Type: "string", Default: "first_match", Enum: []string{"first_match", "union"}},
				{Key: "route", Type: "object"},
				{Key: "empty_assignee_policy", Type: "string", Default: "fail", Enum: []string{"admin", "fail", "skip"}},
				{Key: "due_seconds", Type: "integer", Minimum: workflowAuthoringFloatPointer(0)}, {Key: "reminder_action_key", Type: "action_key"}, {Key: "reminder_input", Type: "object"},
				{Key: "escalation_seconds", Type: "integer", Minimum: workflowAuthoringFloatPointer(0)}, {Key: "escalation_resolvers", Type: "array", ItemSchema: "workflow_assignee_resolver"},
			}, Requires: []string{"workflow.assignee_resolver", "workflow.graph_v2"}, ValidationEndpoint: workflowAuthoringFragmentValidationEndpoint,
			Sources: []capabilitycontract.CapabilityAuthoringSource{workflowSource},
		},
		{
			Key: "workflow.node.action", Status: "supported", Lifecycle: "workflow_node",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{
				{Key: "action_key", Type: "action_key", Required: true}, {Key: "object_key", Type: "object_key"}, {Key: "record_id", Type: "string"},
				{Key: "input", Type: "object"}, {Key: "output_variable", Type: "string"}, {Key: "timeout_seconds", Type: "integer", Minimum: workflowAuthoringFloatPointer(0)},
				{Key: "retry", Type: "workflow_retry_policy"},
				{Key: "on_error", Type: "string", Default: "fail", Enum: []string{"continue", "error_branch", "fail"}},
			}, Requires: []string{"action.definition", "workflow.graph_v2"}, Permissions: []string{workflowAuthoringFragmentAction}, ValidationEndpoint: workflowAuthoringFragmentValidationEndpoint,
			Sources: []capabilitycontract.CapabilityAuthoringSource{workflowSource},
		},
		{
			Key: "workflow.node.cc", Status: "supported", Lifecycle: "workflow_node",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{{Key: "notification_action_key", Type: "action_key", Required: true}, {Key: "resolvers", Type: "array", Required: true, ItemSchema: "workflow_assignee_resolver"}, {Key: "input", Type: "object"}},
			Requires:   []string{"workflow.assignee_resolver", "workflow.graph_v2"}, ValidationEndpoint: workflowAuthoringFragmentValidationEndpoint,
			Sources: []capabilitycontract.CapabilityAuthoringSource{workflowSource},
		},
		{
			Key: "workflow.node.timer", Status: "supported", Lifecycle: "workflow_node",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{
				{Key: "node_type", Type: "string", Required: true, Enum: []string{"timer", "wait_duration", "wait_until"}},
				{Key: "timer_key", Type: "string"}, {Key: "purpose", Type: "string"}, {Key: "at", Type: "string"},
				{Key: "duration_seconds", Type: "integer", Minimum: workflowAuthoringFloatPointer(1)}, {Key: "source_field", Type: "field_key"},
				{Key: "offset_seconds", Type: "integer"}, {Key: "timezone", Type: "string", Default: "UTC"}, {Key: "business_calendar_key", Type: "string"},
			}, Requires: []string{"workflow.graph_v2"}, ValidationEndpoint: workflowAuthoringFragmentValidationEndpoint,
			Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "validation", Path: "runtime/domain/workflow/policy/workflow_graph_policy.go", Symbol: "WorkflowTimerNodeContract"}},
		},
		{
			Key: "workflow.graph_edge", Status: "supported", Lifecycle: "workflow_graph",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{{Key: "id", Type: "string", Required: true}, {Key: "source", Type: "node_id", Required: true}, {Key: "target", Type: "node_id", Required: true}, {Key: "branch", Type: "string"}, {Key: "label", Type: "string"}},
			Requires:   []string{"workflow.graph_v2"}, ValidationEndpoint: workflowAuthoringFragmentValidationEndpoint, Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "validation", Path: "runtime/domain/workflow/policy/workflow_graph_policy.go", Symbol: "WorkflowValidateGraph"}},
		},
	}
	for index := range capabilities {
		workflowCompleteComponentAuthoringContract(&capabilities[index])
	}
	return capabilities
}

func workflowAuthoringFloatPointer(value float64) *float64 { return &value }

func workflowCompleteComponentAuthoringContract(capability *capabilitycontract.CapabilityAuthoringDefinition) {
	if len(capability.Permissions) == 0 {
		capability.Permissions = []string{workflowAuthoringFragmentAction}
	}
	capability.InputSchema = workflowComponentInputSchema(capability.Parameters)
	if capability.Key == "workflow.node.approval" {
		workflowApprovalQuorumSchemaCondition(capability.InputSchema)
		capability.Errors = append(capability.Errors, capabilitycontract.CapabilityAuthoringError{
			Code: "backend.workflow.approval_required_approvals_invalid", FieldPath: "required_approvals", ParameterKeys: []string{"node"}, MessageKey: "backend.workflow.approval_required_approvals_invalid",
		})
	}
	capability.ValidationEndpoint = workflowAuthoringFragmentValidationEndpoint
	capability.OutputSchema = workflowValidationOutputSchema()
	capability.OutputVariables = workflowValidationOutputVariables()
	capability.ReferenceContracts = workflowComponentReferenceContracts(capability.Parameters)
	capability.Execution = &capabilitycontract.CapabilityAuthoringExecution{
		ReadSet: []string{"metadata.workflow_candidate"}, Transaction: "read_only_validation", Idempotency: "naturally_idempotent",
		PermissionModel: workflowAuthoringFragmentAction, SideEffectLevel: "none",
	}
	capability.Errors = append(capability.Errors, workflowComponentError(capability.Key))
	capability.Examples = workflowComponentExamples(capability.Key)
}

func workflowValidationOutputVariables() []capabilitycontract.CapabilityAuthoringOutput {
	return []capabilitycontract.CapabilityAuthoringOutput{
		{Name: "valid", JSONPointer: "/valid", Type: "boolean", VisibleTo: "subsequent_capability_calls"},
		{Name: "normalized_fragment", JSONPointer: "/fragment", Type: "object", VisibleTo: "subsequent_capability_calls"},
		{Name: "issues", JSONPointer: "/issues", Type: "workflow_validation_issues", VisibleTo: "subsequent_capability_calls"},
	}
}

func workflowComponentReferenceContracts(parameters []capabilitycontract.CapabilityAuthoringParameter) []capabilitycontract.CapabilityAuthoringReference {
	result := []capabilitycontract.CapabilityAuthoringReference{}
	for _, parameter := range parameters {
		resolver := ""
		scope := ""
		switch parameter.Type {
		case "object_key":
			resolver = "/discovery/references/object_key"
		case "field_key":
			resolver, scope = "/discovery/references/field_key", "/object_key"
		case "action_key":
			resolver = "/discovery/references/action_key"
		case "role_key":
			resolver = "/discovery/references/role_key"
		}
		if resolver != "" {
			result = append(result, capabilitycontract.CapabilityAuthoringReference{Kind: parameter.Type, InputJSONPointer: "/" + parameter.Key, ScopeFrom: scope, ResolverEndpoint: resolver})
		}
	}
	return result
}

func workflowComponentInputSchema(parameters []capabilitycontract.CapabilityAuthoringParameter) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	properties := make(map[string]capabilitycontract.CapabilityAuthoringSchema, len(parameters))
	required := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		property := capabilitycontract.CapabilityAuthoringSchema{Minimum: parameter.Minimum, Maximum: parameter.Maximum, Default: parameter.Default}
		switch parameter.Type {
		case "string", "object_key", "field_key", "event_key", "role_key", "action_key", "node_id":
			property.Type = "string"
		case "integer":
			property.Type = "integer"
		case "boolean":
			property.Type = "boolean"
		case "array":
			property.Type = "array"
			property.Items = &capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/" + parameter.ItemSchema}
			if parameter.Required {
				property.MinItems = workflowAuthoringIntPointer(1)
			}
		case "workflow_condition", "workflow_retry_policy":
			property.Ref = "#/$defs/" + parameter.Type
		case "object":
			property.Type = "object"
		}
		for _, value := range parameter.Enum {
			property.Enum = append(property.Enum, value)
		}
		properties[parameter.Key] = property
		if parameter.Required {
			required = append(required, parameter.Key)
		}
	}
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed,
		Properties: properties, Required: required, Definitions: workflowComponentSchemaDefinitions(),
	}
}

func workflowComponentSchemaDefinitions() map[string]capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	stringItem := capabilitycontract.CapabilityAuthoringSchema{Type: "string"}
	resolver := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"type"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"type":     {Type: "string", Enum: []any{"initiator_manager", "manager", "manager_of", "record_field", "role", "users"}},
		"priority": {Type: "integer", Minimum: workflowAuthoringFloatPointer(1)}, "user_ids": {Type: "array", Items: &stringItem, MinItems: workflowAuthoringIntPointer(1)},
		"role_key": {Type: "string"}, "field": {Type: "string"}, "user_field": {Type: "string"},
	}}
	conditionItem := capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/workflow_condition"}
	condition := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"type"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"type":  {Type: "string", Enum: []any{"all", "always", "and", "any", "expression", "field_changed", "field_equals", "not", "or"}},
		"field": {Type: "string"}, "value": {}, "expression": {Type: "string"}, "conditions": {Type: "array", Items: &conditionItem}, "condition": conditionItem,
	}}
	retry := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"max_attempts"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"max_attempts": {Type: "integer", Minimum: workflowAuthoringFloatPointer(1)}, "delay_seconds": {Type: "integer", Minimum: workflowAuthoringFloatPointer(0)},
	}}
	return map[string]capabilitycontract.CapabilityAuthoringSchema{
		"object_key": {Type: "string"}, "user_id": {Type: "string"}, "workflow_assignee_resolver": resolver, "workflow_condition": condition, "workflow_retry_policy": retry,
	}
}

func workflowValidationOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	openParams := true
	issue := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"severity", "code", "message_key", "message", "field_path", "capability_key", "contract_version"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"severity": {Type: "string"}, "code": {Type: "string"}, "message_key": {Type: "string"}, "message": {Type: "string"}, "field_path": {Type: "string"},
		"node_id": {Type: "string"}, "edge_id": {Type: "string"}, "params": {Type: "object", AdditionalProperties: &openParams},
		"capability_key": {Type: "string"}, "contract_version": {Type: "string"}, "diagnostic": {Type: "string"},
	}}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"valid", "capability_key", "issues", "validated_at"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"valid": {Type: "boolean"}, "capability_key": {Type: "string"}, "fragment": workflowGraphOpenObjectSchema(), "issues": {Type: "array", Items: &issue}, "validated_at": {Type: "string", Format: "date-time"},
	}}
}

func workflowComponentError(capabilityKey string) capabilitycontract.CapabilityAuthoringError {
	codes := map[string]string{
		"workflow.trigger_contract": "backend.workflow.trigger_type_invalid", "workflow.condition_contract": "backend.workflow.condition_contract_invalid",
		"workflow.assignee_resolver": "backend.workflow.approval_resolver_invalid", "workflow.node.approval": "backend.workflow.graph_approval_mode_invalid",
		"workflow.node.action": "backend.workflow.action_key_required", "workflow.node.cc": "backend.workflow.cc_contract_required", "workflow.node.timer": "backend.workflow.timer_contract_invalid", "workflow.graph_edge": "backend.workflow.graph_edge_invalid",
	}
	code := codes[capabilityKey]
	return capabilitycontract.CapabilityAuthoringError{Code: code, FieldPath: workflowComponentErrorField(capabilityKey), MessageKey: code}
}

func workflowComponentErrorField(capabilityKey string) string {
	fields := map[string]string{
		"workflow.trigger_contract": "trigger_contract", "workflow.condition_contract": "condition_contract", "workflow.assignee_resolver": "graph.nodes[].contract.approval.resolvers[]",
		"workflow.node.approval": "graph.nodes[].contract.approval", "workflow.node.action": "graph.nodes[].contract.action", "workflow.node.cc": "graph.nodes[].contract.cc", "workflow.node.timer": "graph.nodes[].contract.timer", "workflow.graph_edge": "graph.edges[]",
	}
	return fields[capabilityKey]
}

func workflowComponentExamples(capabilityKey string) []capabilitycontract.CapabilityAuthoringExample {
	values := map[string][]map[string]any{
		"workflow.trigger_contract":   {{"type": "manual"}, {"type": "field_changed", "object_key": "order", "field_key": "status"}, {"type": "unknown"}},
		"workflow.condition_contract": {{"type": "always"}, {"type": "field_equals", "field": "status", "value": "paid"}, {"type": "unknown"}},
		"workflow.assignee_resolver":  {{"type": "initiator_manager"}, {"type": "users", "priority": 1, "user_ids": []any{"user-1"}}, {"type": "users"}},
		"workflow.node.approval":      {{"mode": "any", "resolvers": []any{map[string]any{"type": "initiator_manager"}}}, {"mode": "sequential", "resolver_mode": "union", "resolvers": []any{map[string]any{"type": "role", "role_key": "finance_manager"}}, "empty_assignee_policy": "fail", "due_seconds": 86400}, {"mode": "parallel", "resolvers": []any{map[string]any{"type": "initiator_manager"}}}},
		"workflow.node.action":        {{"action_key": "order.complete"}, {"action_key": "order.complete", "object_key": "order", "record_id": "$record.id", "input": map[string]any{"status": "completed"}, "output_variable": "completed_order", "timeout_seconds": 30, "retry": map[string]any{"max_attempts": 3, "delay_seconds": 5}, "on_error": "fail"}, {"action_key": ""}},
		"workflow.node.cc":            {{"notification_action_key": "order.notify", "resolvers": []any{map[string]any{"type": "initiator_manager"}}}, {"notification_action_key": "order.notify", "resolvers": []any{map[string]any{"type": "users", "user_ids": []any{"user-1"}}}, "input": map[string]any{"event": "approved"}}, {"resolvers": []any{map[string]any{"type": "initiator_manager"}}}},
		"workflow.node.timer":         {{"node_type": "wait_duration", "duration_seconds": 30}, {"node_type": "timer", "timer_key": "order_due", "purpose": "resume", "source_field": "due_at", "timezone": "Asia/Shanghai", "business_calendar_key": "weekday"}, {"node_type": "timer", "duration_seconds": 30}},
		"workflow.graph_edge":         {{"id": "start", "source": "trigger", "target": "action"}, {"id": "approved", "source": "approval", "target": "action", "branch": "approved", "label": "Approved"}, {"id": "loop", "source": "action", "target": "action"}},
	}
	examples := values[capabilityKey]
	errorCode := workflowComponentError(capabilityKey).Code
	result := []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: examples[0]}, {Name: "representative", Value: examples[1]}, {Name: "invalid_with_repair", Value: examples[2], ExpectedErrorCodes: []string{errorCode}},
	}
	if capabilityKey == "workflow.node.approval" {
		result[1] = capabilitycontract.CapabilityAuthoringExample{Name: "representative", Value: map[string]any{
			"mode": "quorum", "required_approvals": 2,
			"resolvers": []any{map[string]any{"type": "users", "user_ids": []any{"user-1", "user-2", "user-3"}}},
		}}
	}
	return result
}

func workflowAuthoringIntPointer(value int) *int { return &value }
