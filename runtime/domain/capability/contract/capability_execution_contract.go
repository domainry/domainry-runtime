package contract

const RuntimeCapabilityContractVersion = "runtime-capabilities-v1"

type CapabilityExecution struct {
	Type              string   `json:"type"`
	Owner             string   `json:"owner"`
	Lifecycle         string   `json:"lifecycle"`
	SupportedContexts []string `json:"supported_contexts"`
}

type CapabilityExecutionCatalog struct {
	RuntimeVersion         string                `json:"runtime_version"`
	AutomationInstructions []CapabilityExecution `json:"automation_instructions"`
	WorkflowNodes          []CapabilityExecution `json:"workflow_nodes"`
}

// RuntimeExecutionCapabilities returns the static execution contract shared by
// the HTTP discovery endpoint and the repository-owned Builder catalog.
func RuntimeExecutionCapabilities() CapabilityExecutionCatalog {
	return CapabilityExecutionCatalog{
		RuntimeVersion:         RuntimeCapabilityContractVersion,
		AutomationInstructions: executionCapabilities("automation_rule", "record_lifecycle", []string{"assert", "derive_fields", "invoke_business_action", "start_workflow", "emit_event"}, []string{"before", "after"}),
		WorkflowNodes:          executionCapabilities("workflow", "stateful_process", []string{"trigger", "condition", "approval", "action", "agent_task", "cc", "wait_until", "wait_duration", "timer"}, []string{"graph_v2"}),
	}
}

func executionCapabilities(owner, lifecycle string, types, contexts []string) []CapabilityExecution {
	items := make([]CapabilityExecution, 0, len(types))
	for _, capabilityType := range types {
		items = append(items, CapabilityExecution{Type: capabilityType, Owner: owner, Lifecycle: lifecycle, SupportedContexts: append([]string(nil), contexts...)})
	}
	return items
}
