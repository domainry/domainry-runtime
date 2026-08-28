package projection

type AutomationInstructionTrace struct {
	Key          string            `json:"key"`
	Type         string            `json:"type"`
	Status       string            `json:"status"`
	DurationMS   int64             `json:"duration_ms"`
	ErrorCode    string            `json:"error_code,omitempty"`
	ErrorReason  string            `json:"error_reason,omitempty"`
	ErrorParams  map[string]string `json:"error_params,omitempty"`
	InvocationID string            `json:"invocation_id,omitempty"`
	Input        map[string]any    `json:"input,omitempty"`
	Data         map[string]any    `json:"data,omitempty"`
}

type AutomationNodeTrace struct {
	NodeID       string            `json:"node_id"`
	Kind         string            `json:"kind"`
	Status       string            `json:"status"`
	DurationMS   int64             `json:"duration_ms"`
	ErrorCode    string            `json:"error_code,omitempty"`
	ErrorReason  string            `json:"error_reason,omitempty"`
	ErrorParams  map[string]string `json:"error_params,omitempty"`
	InvocationID string            `json:"invocation_id,omitempty"`
	Input        map[string]any    `json:"input,omitempty"`
	Output       map[string]any    `json:"output,omitempty"`
}

type AutomationRuleTrace struct {
	ExecutionID       string                       `json:"execution_id"`
	RuleKey           string                       `json:"rule_key"`
	Status            string                       `json:"status"`
	Matched           bool                         `json:"matched"`
	DurationMS        int64                        `json:"duration_ms"`
	ErrorCode         string                       `json:"error_code,omitempty"`
	InstructionTraces []AutomationInstructionTrace `json:"instructions,omitempty"`
	NodeTraces        []AutomationNodeTrace        `json:"nodes,omitempty"`
}

type AutomationSimulationResult struct {
	RuleKey      string              `json:"rule_key"`
	Status       string              `json:"status"`
	WouldSave    bool                `json:"would_save"`
	TestedNodeID string              `json:"tested_node_id,omitempty"`
	NodePassed   bool                `json:"node_passed"`
	Candidate    map[string]any      `json:"candidate"`
	Trace        AutomationRuleTrace `json:"trace"`
}
