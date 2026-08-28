package changeplan

import (
	"reflect"
	"testing"
)

func TestReferenceGraphPlaceholderEnrichmentIsOrderIndependent(t *testing.T) {
	build := func(edgeFirst bool) ReferenceGraph {
		builder := NewReferenceGraphBuilder()
		addEdge := func() {
			builder.Edge("field", "calibration_request.instrument", "object", "instrument", "references_object", "fields.instrument")
		}
		addNode := func() {
			builder.Node("object", "instrument", "instrument", "Instrument", "metadata")
		}
		if edgeFirst {
			addEdge()
			addNode()
		} else {
			addNode()
			addEdge()
		}
		return builder.Graph()
	}

	edgeFirst := build(true)
	nodeFirst := build(false)
	if !reflect.DeepEqual(edgeFirst, nodeFirst) {
		t.Fatalf("reference graph depends on discovery order:\nedge first: %#v\nnode first: %#v", edgeFirst, nodeFirst)
	}
	for _, node := range edgeFirst.Nodes {
		if node.ResourceType == "object" && node.ResourceKey == "instrument" {
			if node.ObjectKey != "instrument" || node.Label != "Instrument" || node.Owner != "metadata" {
				t.Fatalf("placeholder was not enriched: %#v", node)
			}
			return
		}
	}
	t.Fatal("missing enriched object node")
}
