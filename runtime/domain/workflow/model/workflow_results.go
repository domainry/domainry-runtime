package workflowmodel

type WorkflowRunResult struct {
	WorkflowKey string            `json:"workflow_key"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Action      map[string]any    `json:"action"`
	Payload     map[string]any    `json:"payload,omitempty"`
	Execution   WorkflowExecution `json:"execution"`
}

type WorkflowResolveResult struct {
	Status    string            `json:"status"`
	Message   string            `json:"message"`
	Execution WorkflowExecution `json:"execution"`
}

type WorkflowProcessResult struct {
	Processed  int                 `json:"processed"`
	Executions []WorkflowExecution `json:"executions"`
}

// WorkflowScheduledPage is one bounded traversal step over the deterministic
// record set addressed by a Scheduler window. Checkpoint is opaque to the
// Scheduler; Workflow owns its schema and advances it only after a candidate
// record has been examined. A replay of the same page remains safe because the
// workflow execution fingerprint includes the scheduled window and record.
type WorkflowScheduledPage struct {
	WorkflowProcessResult
	Scanned    int    `json:"scanned"`
	Checkpoint string `json:"checkpoint,omitempty"`
	Complete   bool   `json:"complete"`
}

type WorkflowSimulationResult struct {
	WorkflowKey    string                   `json:"workflow_key"`
	Name           string                   `json:"name"`
	Status         string                   `json:"status"`
	WouldExecute   bool                     `json:"would_execute"`
	Action         map[string]any           `json:"action"`
	Payload        map[string]any           `json:"payload,omitempty"`
	RunAs          string                   `json:"run_as"`
	IdempotencyKey string                   `json:"idempotency_key,omitempty"`
	Message        string                   `json:"message"`
	Nodes          []WorkflowSimulationNode `json:"nodes,omitempty"`
}

type WorkflowSimulationNode struct {
	NodeID            string         `json:"node_id"`
	NodeType          string         `json:"node_type"`
	Status            string         `json:"status"`
	Outcome           string         `json:"outcome,omitempty"`
	ResolvedAssignees []string       `json:"resolved_assignees,omitempty"`
	ActionKey         string         `json:"action_key,omitempty"`
	Details           map[string]any `json:"details,omitempty"`
}
