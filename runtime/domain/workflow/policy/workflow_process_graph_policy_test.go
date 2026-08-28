package policy

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestWorkflowProcessGraphHelpersMatrix(t *testing.T) {
	for _, test := range []struct {
		edge    definitionmodel.WorkflowGraphEdge
		outcome string
		want    bool
	}{{definitionmodel.WorkflowGraphEdge{}, "approved", true}, {definitionmodel.WorkflowGraphEdge{}, "rejected", false}, {definitionmodel.WorkflowGraphEdge{Label: "success"}, "approved", true}, {definitionmodel.WorkflowGraphEdge{Branch: "failure"}, "false", true}, {definitionmodel.WorkflowGraphEdge{Branch: "custom"}, "custom", true}, {definitionmodel.WorkflowGraphEdge{Branch: "other"}, "custom", false}} {
		if got := WorkflowEdgeMatches(test.edge, test.outcome); got != test.want {
			t.Fatalf("edge %#v/%q = %v", test.edge, test.outcome, got)
		}
	}
	graph := &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "action", Type: "action"}, {ID: "trigger", Type: "trigger"}}}
	if got := WorkflowGraphTrigger(graph); got.ID != "trigger" || WorkflowGraphTrigger(nil).ID != "" {
		t.Fatal("trigger lookup mismatch")
	}
	if node, ok := WorkflowGraphNode(graph, "action"); !ok || node.ID != "action" {
		t.Fatalf("node = %#v/%v", node, ok)
	}
	if _, ok := WorkflowGraphNode(graph, "missing"); ok {
		t.Fatal("missing node resolved")
	}
	if _, ok := WorkflowGraphNode(nil, "missing"); ok {
		t.Fatal("nil graph node resolved")
	}
	workflow := definitionmodel.WorkflowSchema{Key: "flow", Graph: graph}
	if WorkflowDefinitionHash(workflow) == "" || WorkflowDefinitionHash(workflow) != WorkflowDefinitionHash(workflow) {
		t.Fatal("definition hash unstable")
	}
	if WorkflowPublishedVersion(definitionmodel.WorkflowSchema{PublishedVersion: 3, Graph: graph}) != 3 || WorkflowPublishedVersion(workflow) != 2 || WorkflowPublishedVersion(definitionmodel.WorkflowSchema{}) != 0 || WorkflowGraphContractVersion(workflow) != 2 || WorkflowGraphContractVersion(definitionmodel.WorkflowSchema{}) != 0 {
		t.Fatal("workflow version helpers mismatch")
	}
	if got := WorkflowRemoveString([]string{"a", "b", "a"}, "a"); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("removed strings = %#v", got)
	}
}
