package contract

import automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

type AutomationSimulationRequest struct {
	Rule         *automationmodel.AutomationRuleSchema `json:"rule,omitempty"`
	Input        map[string]any                        `json:"input,omitempty"`
	Before       map[string]any                        `json:"before,omitempty"`
	TargetNodeID string                                `json:"target_node_id,omitempty"`
}
