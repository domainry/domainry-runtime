package policy

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// WorkflowValidateAuthoringFragment validates one owner-published leaf
// capability payload without requiring callers to manufacture a complete
// Workflow draft. Composite node fragments are embedded into the smallest
// valid graph that exercises the same Workflow graph policy used at runtime.
func WorkflowValidateAuthoringFragment(capabilityKey string, value map[string]any) error {
	_, err := WorkflowNormalizeAuthoringFragment(capabilityKey, value)
	return err
}

// WorkflowNormalizeAuthoringFragment materializes protocol values implied by
// the selected capability and validates that exact canonical candidate. Model
// clients can persist or repair the returned fragment without reimplementing
// Workflow defaults.
func WorkflowNormalizeAuthoringFragment(capabilityKey string, value map[string]any) (map[string]any, error) {
	capabilityKey = strings.TrimSpace(capabilityKey)
	value = WorkflowAuthoringFragmentWithDefaults(capabilityKey, value)
	switch capabilityKey {
	case "workflow.graph_v2":
		var graph definitionmodel.WorkflowGraphSchema
		if err := workflowDecodeAuthoringFragment(value, &graph); err != nil {
			return nil, err
		}
		if err := WorkflowValidateGraph(&graph); err != nil {
			return nil, err
		}
	case "workflow.trigger_contract":
		var contract definitionmodel.WorkflowTriggerContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return nil, err
		}
		if !WorkflowTriggerContractTypeIsValid(contract.Type) {
			return nil, badRequest("backend.workflow.trigger_type_invalid")
		}
	case "workflow.condition_contract":
		var contract definitionmodel.WorkflowConditionContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return nil, err
		}
		if !WorkflowConditionContractIsValid(contract) {
			return nil, badRequest("backend.workflow.condition_contract_invalid")
		}
	case "workflow.assignee_resolver":
		var resolver definitionmodel.WorkflowAssigneeResolver
		if err := workflowDecodeAuthoringFragment(value, &resolver); err != nil {
			return nil, err
		}
		if !WorkflowAssigneeResolverIsValid(resolver) {
			return nil, badRequest("backend.workflow.approval_resolver_invalid")
		}
	case "workflow.node.approval":
		var contract definitionmodel.WorkflowApprovalNodeContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return nil, err
		}
		if err := WorkflowValidateGraph(workflowAuthoringApprovalGraph(contract)); err != nil {
			return nil, err
		}
	case "workflow.node.action":
		var contract definitionmodel.WorkflowBusinessActionNodeContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return nil, err
		}
		if err := WorkflowValidateGraph(workflowAuthoringActionGraph(contract)); err != nil {
			return nil, err
		}
	case "workflow.node.cc":
		var contract definitionmodel.WorkflowCCNodeContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return nil, err
		}
		if err := WorkflowValidateGraph(workflowAuthoringCCGraph(contract)); err != nil {
			return nil, err
		}
	case "workflow.node.timer":
		var contract definitionmodel.WorkflowTimerNodeContract
		contractValue := make(map[string]any, len(value))
		for key, item := range value {
			if key != "node_type" {
				contractValue[key] = item
			}
		}
		if err := workflowDecodeAuthoringFragment(contractValue, &contract); err != nil {
			return nil, err
		}
		if err := WorkflowValidateGraph(workflowAuthoringTimerGraph(strings.TrimSpace(stringValue(value["node_type"])), contract)); err != nil {
			return nil, err
		}
	case "workflow.graph_edge":
		var edge definitionmodel.WorkflowGraphEdge
		if err := workflowDecodeAuthoringFragment(value, &edge); err != nil {
			return nil, err
		}
		if err := WorkflowValidateGraph(workflowAuthoringEdgeGraph(edge)); err != nil {
			return nil, err
		}
	default:
		return nil, badRequest("backend.workflow.authoring_capability_unsupported", "capability_key", capabilityKey)
	}
	return value, nil
}

func WorkflowAuthoringFragmentWithDefaults(capabilityKey string, value map[string]any) map[string]any {
	normalized := make(map[string]any, len(value)+1)
	for key, item := range value {
		normalized[key] = item
	}
	if strings.TrimSpace(capabilityKey) == "workflow.graph_v2" {
		normalized["version"] = 2
	}
	return normalized
}

func workflowDecodeAuthoringFragment(value map[string]any, target any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return badRequest("backend.workflow.authoring_fragment_invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return badRequest("backend.workflow.authoring_fragment_invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return badRequest("backend.workflow.authoring_fragment_invalid")
	}
	return nil
}

func workflowAuthoringApprovalGraph(contract definitionmodel.WorkflowApprovalNodeContract) *definitionmodel.WorkflowGraphSchema {
	return &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "trigger", Type: "trigger"}, {ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &contract}},
			workflowAuthoringActionNode("approved"), workflowAuthoringActionNode("rejected"),
		},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "approval"}, {ID: "approved", Source: "approval", Target: "approved", Branch: "approved"}, {ID: "rejected", Source: "approval", Target: "rejected", Branch: "rejected"}},
	}
}

func workflowAuthoringActionGraph(contract definitionmodel.WorkflowBusinessActionNodeContract) *definitionmodel.WorkflowGraphSchema {
	return &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &contract}}},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "action"}},
	}
}

func workflowAuthoringCCGraph(contract definitionmodel.WorkflowCCNodeContract) *definitionmodel.WorkflowGraphSchema {
	return &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "cc", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &contract}}},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "cc"}},
	}
}

func workflowAuthoringTimerGraph(nodeType string, contract definitionmodel.WorkflowTimerNodeContract) *definitionmodel.WorkflowGraphSchema {
	return &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "timer", Type: nodeType, Contract: &definitionmodel.WorkflowNodeContract{Timer: &contract}}, workflowAuthoringActionNode("action")},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "timer"}, {ID: "resume", Source: "timer", Target: "action"}},
	}
}

func workflowAuthoringEdgeGraph(edge definitionmodel.WorkflowGraphEdge) *definitionmodel.WorkflowGraphSchema {
	if edge.Source == "approval" {
		approval := definitionmodel.WorkflowApprovalNodeContract{Mode: "any", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "initiator_manager"}}}
		return &definitionmodel.WorkflowGraphSchema{Version: 2,
			Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &approval}}, workflowAuthoringActionNode(edge.Target), workflowAuthoringActionNode("rejected")},
			Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "approval"}, edge, {ID: "rejected", Source: "approval", Target: "rejected", Branch: "rejected"}},
		}
	}
	return &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, workflowAuthoringActionNode("action")},
		Edges: []definitionmodel.WorkflowGraphEdge{edge},
	}
}

func workflowAuthoringActionNode(id string) definitionmodel.WorkflowGraphNode {
	return definitionmodel.WorkflowGraphNode{ID: id, Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "authoring.validation"}}}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
