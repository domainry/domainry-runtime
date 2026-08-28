package policy

import (
	"sort"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

func ChangePlanReferenceImpact(graph changeplanmodel.ReferenceGraph, resourceType, resourceKey string) changeplanmodel.ReferenceImpact {
	result := changeplanmodel.ReferenceImpact{ResourceType: resourceType, ResourceKey: resourceKey, GraphHash: graph.Hash, DirectConsumers: []changeplanmodel.ReferenceEdge{}, DirectDependencies: []changeplanmodel.ReferenceEdge{}, IndirectConsumers: []changeplanmodel.ReferenceEdge{}}
	for _, edge := range graph.Edges {
		if edge.ToType == resourceType && edge.ToKey == resourceKey {
			result.DirectConsumers = append(result.DirectConsumers, edge)
		}
		if edge.FromType == resourceType && edge.FromKey == resourceKey {
			result.DirectDependencies = append(result.DirectDependencies, edge)
		}
	}
	visited := map[string]bool{resourceType + "\x00" + resourceKey: true}
	frontier := append([]changeplanmodel.ReferenceEdge(nil), result.DirectConsumers...)
	for len(frontier) > 0 {
		edge := frontier[0]
		frontier = frontier[1:]
		consumerID := edge.FromType + "\x00" + edge.FromKey
		if visited[consumerID] {
			continue
		}
		visited[consumerID] = true
		if edge.ToType != resourceType || edge.ToKey != resourceKey {
			result.IndirectConsumers = append(result.IndirectConsumers, edge)
		}
		for _, candidate := range graph.Edges {
			if candidate.ToType == edge.FromType && candidate.ToKey == edge.FromKey {
				frontier = append(frontier, candidate)
			}
		}
	}
	changePlanSortReferenceEdges(result.DirectConsumers)
	changePlanSortReferenceEdges(result.DirectDependencies)
	changePlanSortReferenceEdges(result.IndirectConsumers)
	result.DeletionBlocked = len(result.DirectConsumers) > 0
	return result
}

func changePlanSortReferenceEdges(edges []changeplanmodel.ReferenceEdge) {
	sort.Slice(edges, func(i, j int) bool {
		return edges[i].FromType+"\x00"+edges[i].FromKey+"\x00"+edges[i].Kind+"\x00"+edges[i].Path < edges[j].FromType+"\x00"+edges[j].FromKey+"\x00"+edges[j].Kind+"\x00"+edges[j].Path
	})
}
