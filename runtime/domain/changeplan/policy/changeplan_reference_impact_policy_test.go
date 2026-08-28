package policy

import (
	"reflect"
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

func TestChangePlanReferenceImpactDirectIndirectCycleAndStableOrder(t *testing.T) {
	target := changeplanmodel.ReferenceEdge{FromType: "view", FromKey: "b", ToType: "field", ToKey: "status", Kind: "reads", Path: "z"}
	graph := changeplanmodel.ReferenceGraph{Hash: "hash-1", Edges: []changeplanmodel.ReferenceEdge{
		target,
		{FromType: "action", FromKey: "a", ToType: "field", ToKey: "status", Kind: "writes", Path: "a"},
		{FromType: "field", FromKey: "other", ToType: "field", ToKey: "status", Kind: "depends"},
		{FromType: "field", FromKey: "status", ToType: "object", ToKey: "order", Kind: "belongs_to"},
		{FromType: "surface", FromKey: "s", ToType: "view", ToKey: "b", Kind: "renders"},
		{FromType: "surface", FromKey: "other", ToType: "field", ToKey: "other", Kind: "renders"},
		{FromType: "action", FromKey: "unrelated", ToType: "view", ToKey: "other", Kind: "unrelated"},
		{FromType: "report", FromKey: "r", ToType: "action", ToKey: "a", Kind: "invokes"},
		{FromType: "view", FromKey: "b", ToType: "surface", ToKey: "s", Kind: "cycle"},
	}}
	impact := ChangePlanReferenceImpact(graph, "field", "status")
	if impact.ResourceType != "field" || impact.ResourceKey != "status" || impact.GraphHash != "hash-1" || !impact.DeletionBlocked {
		t.Fatalf("impact metadata = %#v", impact)
	}
	if len(impact.DirectConsumers) != 3 || impact.DirectConsumers[0].FromKey != "a" || impact.DirectConsumers[1].FromKey != "other" || impact.DirectConsumers[2].FromKey != "b" {
		t.Fatalf("direct consumers = %#v", impact.DirectConsumers)
	}
	if len(impact.DirectDependencies) != 1 || impact.DirectDependencies[0].ToKey != "order" {
		t.Fatalf("direct dependencies = %#v", impact.DirectDependencies)
	}
	if len(impact.IndirectConsumers) != 3 || impact.IndirectConsumers[0].FromKey != "r" || impact.IndirectConsumers[1].FromKey != "other" || impact.IndirectConsumers[2].FromKey != "s" {
		t.Fatalf("indirect consumers = %#v", impact.IndirectConsumers)
	}
	empty := ChangePlanReferenceImpact(graph, "field", "missing")
	if empty.DeletionBlocked || len(empty.DirectConsumers) != 0 || len(empty.DirectDependencies) != 0 || len(empty.IndirectConsumers) != 0 {
		t.Fatalf("empty impact = %#v", empty)
	}
	edges := []changeplanmodel.ReferenceEdge{{FromType: "z", FromKey: "z"}, {FromType: "a", FromKey: "a"}}
	changePlanSortReferenceEdges(edges)
	if !reflect.DeepEqual([]string{edges[0].FromKey, edges[1].FromKey}, []string{"a", "z"}) {
		t.Fatalf("sorted edges = %#v", edges)
	}
}
