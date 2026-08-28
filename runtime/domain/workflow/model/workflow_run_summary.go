package workflowmodel

type WorkflowRunSummary struct {
	WorkflowKey string         `json:"workflow_key"`
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	Action      map[string]any `json:"action"`
	ExecutionID string         `json:"execution_id,omitempty"`
	Message     string         `json:"message,omitempty"`
}
