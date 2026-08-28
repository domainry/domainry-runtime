package policy

import (
	"encoding/json"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// WorkflowValidateAuthoringFragment validates one owner-published leaf
// capability payload without requiring callers to manufacture a complete
// Workflow draft. Composite node fragments are embedded into the smallest
// valid graph that exercises the same Workflow graph policy used at runtime.
func WorkflowValidateAuthoringFragment(capabilityKey string, value map[string]any) error {
	capabilityKey = strings.TrimSpace(capabilityKey)
	switch capabilityKey {
	case "workflow.graph_v2":
		var graph definitionmodel.WorkflowGraphSchema
		if err := workflowDecodeAuthoringFragment(value, &graph); err != nil {
			return err
		}
		return WorkflowValidateGraph(&graph)
	case "workflow.trigger_contract":
		var contract definitionmodel.WorkflowTriggerContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return err
		}
		if !WorkflowTriggerContractTypeIsValid(contract.Type) {
			return badRequest("backend.workflow.trigger_type_invalid")
		}
		return nil
	case "workflow.condition_contract":
		var contract definitionmodel.WorkflowConditionContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return err
		}
		if !WorkflowConditionContractIsValid(contract) {
			return badRequest("backend.workflow.condition_contract_invalid")
		}
		return nil
	case "workflow.assignee_resolver":
		var resolver definitionmodel.WorkflowAssigneeResolver
		if err := workflowDecodeAuthoringFragment(value, &resolver); err != nil {
			return err
		}
		if !WorkflowAssigneeResolverIsValid(resolver) {
			return badRequest("backend.workflow.approval_resolver_invalid")
		}
		return nil
	case "workflow.node.approval":
		var contract definitionmodel.WorkflowApprovalNodeContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return err
		}
		return WorkflowValidateGraph(workflowAuthoringApprovalGraph(contract))
	case "workflow.node.action":
		var contract definitionmodel.WorkflowBusinessActionNodeContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return err
		}
		return WorkflowValidateGraph(workflowAuthoringActionGraph(contract))
	case "workflow.node.cc":
		var contract definitionmodel.WorkflowCCNodeContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return err
		}
		return WorkflowValidateGraph(workflowAuthoringCCGraph(contract))
	case "workflow.node.timer":
		var contract definitionmodel.WorkflowTimerNodeContract
		if err := workflowDecodeAuthoringFragment(value, &contract); err != nil {
			return err
		}
		return WorkflowValidateGraph(workflowAuthoringTimerGraph(strings.TrimSpace(stringValue(value["node_type"])), contract))
	case "workflow.graph_edge":
		var edge definitionmodel.WorkflowGraphEdge
		if err := workflowDecodeAuthoringFragment(value, &edge); err != nil {
			return err
		}
		return WorkflowValidateGraph(workflowAuthoringEdgeGraph(edge))
	default:
		return badRequest("backend.workflow.authoring_capability_unsupported", "capability_key", capabilityKey)
	}
}

func workflowDecodeAuthoringFragment(value map[string]any, target any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return badRequest("backend.workflow.authoring_fragment_invalid")
	}
	if err := json.Unmarshal(raw, target); err != nil {
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
