package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestWorkflowAuthoringDomainIsOwnerOwnedAndTracksExecutionNodeTypes(t *testing.T) {
	domain := WorkflowAuthoringDomain()
	if domain.Key != "workflow" || len(domain.Capabilities) != 10 {
		t.Fatalf("domain=%#v", domain)
	}
	if domain.Capabilities[0].Key != "workflow.definition" {
		t.Fatalf("first capability=%s", domain.Capabilities[0].Key)
	}
	graph := domain.Capabilities[1]
	if graph.Key != "workflow.graph_v2" {
		t.Fatalf("first capability=%s", graph.Key)
	}
	wantNodeTypes := map[string]bool{}
	for _, execution := range capabilitycontract.RuntimeExecutionCapabilities().WorkflowNodes {
		wantNodeTypes[execution.Type] = true
	}
	gotNodeTypes := map[string]bool{}
	for _, value := range graph.InputSchema.Definitions["workflow_graph_node"].Properties["type"].Enum {
		if nodeType, ok := value.(string); ok {
			gotNodeTypes[nodeType] = true
		}
	}
	if len(gotNodeTypes) != len(wantNodeTypes) {
		t.Fatalf("published node types=%v execution node types=%v", gotNodeTypes, wantNodeTypes)
	}
	for nodeType := range wantNodeTypes {
		if !gotNodeTypes[nodeType] {
			t.Fatalf("execution node type %q is not published", nodeType)
		}
	}
}

func TestWorkflowComponentExamplesExecuteOwnerValidators(t *testing.T) {
	for _, capability := range WorkflowAuthoringDomain().Capabilities {
		if capability.InputSchema == nil || capability.OutputSchema == nil || capability.Execution == nil || len(capability.Examples) != 3 {
			t.Fatalf("capability %s is missing component contract evidence", capability.Key)
		}
		for _, example := range capability.Examples {
			code := workflowComponentExampleValidationCode(t, capability.Key, example.Value)
			if len(example.ExpectedErrorCodes) == 0 && code != "" {
				t.Fatalf("capability=%s example=%s code=%s", capability.Key, example.Name, code)
			}
			if len(example.ExpectedErrorCodes) > 0 && code != example.ExpectedErrorCodes[0] {
				t.Fatalf("capability=%s example=%s code=%s want=%s", capability.Key, example.Name, code, example.ExpectedErrorCodes[0])
			}
		}
	}
}

func workflowComponentExampleValidationCode(t *testing.T, capabilityKey string, value map[string]any) string {
	t.Helper()
	switch capabilityKey {
	case "workflow.definition":
		workflowValue, _ := value["payload"].(map[string]any)
		var workflow definitionmodel.WorkflowSchema
		workflowDecodeAuthoringExample(t, workflowValue, &workflow)
		if workflow.TriggerContract == nil || !WorkflowTriggerContractTypeIsValid(workflow.TriggerContract.Type) {
			return "backend.workflow.trigger_type_invalid"
		}
		return workflowAuthoringGraphValidationCode(WorkflowValidateGraph(workflow.Graph))
	case "workflow.graph_v2":
		var graph definitionmodel.WorkflowGraphSchema
		workflowDecodeAuthoringExample(t, value, &graph)
		return workflowAuthoringGraphValidationCode(WorkflowValidateGraph(&graph))
	case "workflow.trigger_contract":
		var contract definitionmodel.WorkflowTriggerContract
		workflowDecodeAuthoringExample(t, value, &contract)
		if !WorkflowTriggerContractTypeIsValid(contract.Type) {
			return "backend.workflow.trigger_type_invalid"
		}
		return ""
	case "workflow.condition_contract":
		var contract definitionmodel.WorkflowConditionContract
		workflowDecodeAuthoringExample(t, value, &contract)
		if !WorkflowConditionContractIsValid(contract) {
			return "backend.workflow.condition_contract_invalid"
		}
		return ""
	case "workflow.assignee_resolver":
		var resolver definitionmodel.WorkflowAssigneeResolver
		workflowDecodeAuthoringExample(t, value, &resolver)
		if !WorkflowAssigneeResolverIsValid(resolver) {
			return "backend.workflow.approval_resolver_invalid"
		}
		return ""
	case "workflow.node.approval":
		var contract definitionmodel.WorkflowApprovalNodeContract
		workflowDecodeAuthoringExample(t, value, &contract)
		return workflowAuthoringGraphValidationCode(WorkflowValidateGraph(workflowApprovalExampleGraph(contract)))
	case "workflow.node.action":
		var contract definitionmodel.WorkflowBusinessActionNodeContract
		workflowDecodeAuthoringExample(t, value, &contract)
		return workflowAuthoringGraphValidationCode(WorkflowValidateGraph(workflowActionExampleGraph(contract)))
	case "workflow.node.cc":
		var contract definitionmodel.WorkflowCCNodeContract
		workflowDecodeAuthoringExample(t, value, &contract)
		return workflowAuthoringGraphValidationCode(WorkflowValidateGraph(workflowCCExampleGraph(contract)))
	case "workflow.node.timer":
		var contract definitionmodel.WorkflowTimerNodeContract
		workflowDecodeAuthoringExample(t, value, &contract)
		nodeType, _ := value["node_type"].(string)
		return workflowAuthoringGraphValidationCode(WorkflowValidateGraph(workflowTimerExampleGraph(nodeType, contract)))
	case "workflow.graph_edge":
		var edge definitionmodel.WorkflowGraphEdge
		workflowDecodeAuthoringExample(t, value, &edge)
		return workflowAuthoringGraphValidationCode(WorkflowValidateGraph(workflowEdgeExampleGraph(edge)))
	default:
		t.Fatalf("unsupported workflow capability %s", capabilityKey)
		return ""
	}
}

func workflowAuthoringGraphValidationCode(err error) string {
	if err == nil {
		return ""
	}
	return apperror.CodeOf(err)
}

func workflowApprovalExampleGraph(contract definitionmodel.WorkflowApprovalNodeContract) *definitionmodel.WorkflowGraphSchema {
	return &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "trigger", Type: "trigger"}, {ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &contract}},
			workflowAuthoringTestActionNode("approved"), workflowAuthoringTestActionNode("rejected"),
		},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "approval"}, {ID: "approved", Source: "approval", Target: "approved", Branch: "approved"}, {ID: "rejected", Source: "approval", Target: "rejected", Branch: "rejected"}},
	}
}

func workflowActionExampleGraph(contract definitionmodel.WorkflowBusinessActionNodeContract) *definitionmodel.WorkflowGraphSchema {
	return &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &contract}}},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "action"}},
	}
}

func workflowCCExampleGraph(contract definitionmodel.WorkflowCCNodeContract) *definitionmodel.WorkflowGraphSchema {
	return &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "cc", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &contract}}},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "cc"}},
	}
}

func workflowTimerExampleGraph(nodeType string, contract definitionmodel.WorkflowTimerNodeContract) *definitionmodel.WorkflowGraphSchema {
	return &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "timer", Type: nodeType, Contract: &definitionmodel.WorkflowNodeContract{Timer: &contract}}, workflowAuthoringTestActionNode("action")},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "timer"}, {ID: "resume", Source: "timer", Target: "action"}},
	}
}

func workflowEdgeExampleGraph(edge definitionmodel.WorkflowGraphEdge) *definitionmodel.WorkflowGraphSchema {
	if edge.Source == "trigger" {
		return &definitionmodel.WorkflowGraphSchema{Version: 2,
			Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, workflowAuthoringTestActionNode("action")}, Edges: []definitionmodel.WorkflowGraphEdge{edge},
		}
	}
	if edge.Source == "approval" {
		approval := definitionmodel.WorkflowApprovalNodeContract{Mode: "any", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"user-1"}}}}
		return &definitionmodel.WorkflowGraphSchema{Version: 2,
			Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &approval}}, workflowAuthoringTestActionNode(edge.Target), workflowAuthoringTestActionNode("rejected")},
			Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "approval"}, edge, {ID: "rejected", Source: "approval", Target: "rejected", Branch: "rejected"}},
		}
	}
	return &definitionmodel.WorkflowGraphSchema{Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, workflowAuthoringTestActionNode("action")},
		Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "action"}, edge},
	}
}

func workflowAuthoringTestActionNode(id string) definitionmodel.WorkflowGraphNode {
	return definitionmodel.WorkflowGraphNode{ID: id, Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "order.complete"}}}
}

func workflowDecodeAuthoringExample(t *testing.T, value map[string]any, target any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowAuthoringSourceAnchorsExist(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	for _, capability := range WorkflowAuthoringDomain().Capabilities {
		if len(capability.Sources) == 0 {
			t.Fatalf("capability %s has no source anchor", capability.Key)
		}
		for _, source := range capability.Sources {
			if _, err := os.Stat(filepath.Join(repositoryRoot, source.Path)); err != nil {
				t.Fatalf("capability %s source %s: %v", capability.Key, source.Path, err)
			}
		}
	}
}
