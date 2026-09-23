package contract

type AutomationSimulationRequest struct {
	Input        map[string]any `json:"input,omitempty"`
	Before       map[string]any `json:"before,omitempty"`
	TargetNodeID string         `json:"target_node_id,omitempty"`
}
