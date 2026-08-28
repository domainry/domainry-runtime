package projection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanpolicy "github.com/domainry/domainry-runtime/runtime/domain/changeplan/policy"
)

const ChangePlanReferenceGraphVersion = "domain-reference-graph-v1"

type ChangePlanReferenceGraphBuilder struct {
	nodes map[string]changeplanmodel.ReferenceNode
	edges map[string]changeplanmodel.ReferenceEdge
}

func NewChangePlanReferenceGraphBuilder() *ChangePlanReferenceGraphBuilder {
	return &ChangePlanReferenceGraphBuilder{nodes: map[string]changeplanmodel.ReferenceNode{}, edges: map[string]changeplanmodel.ReferenceEdge{}}
}

func (builder *ChangePlanReferenceGraphBuilder) Node(resourceType, resourceKey, objectKey, label, owner string) {
	resourceType, resourceKey = strings.TrimSpace(resourceType), strings.TrimSpace(resourceKey)
	if resourceType == "" || resourceKey == "" {
		return
	}
	key := resourceType + "\x00" + resourceKey
	existing := builder.nodes[key]
	existing.ResourceType, existing.ResourceKey = resourceType, resourceKey
	if value := strings.TrimSpace(objectKey); value != "" {
		existing.ObjectKey = value
	}
	if value := strings.TrimSpace(label); value != "" {
		existing.Label = value
	}
	if value := strings.TrimSpace(owner); value != "" {
		existing.Owner = value
	}
	builder.nodes[key] = existing
}

func (builder *ChangePlanReferenceGraphBuilder) Edge(fromType, fromKey, toType, toKey, kind, path string) {
	if strings.TrimSpace(fromKey) == "" || strings.TrimSpace(toKey) == "" {
		return
	}
	builder.Node(fromType, fromKey, "", "", "")
	builder.Node(toType, toKey, "", "", "")
	edge := changeplanmodel.ReferenceEdge{FromType: fromType, FromKey: fromKey, ToType: toType, ToKey: toKey, Kind: kind, Path: path}
	builder.edges[strings.Join([]string{fromType, fromKey, toType, toKey, kind, path}, "\x00")] = edge
}

func (builder *ChangePlanReferenceGraphBuilder) Graph() changeplanmodel.ReferenceGraph {
	graph := changeplanmodel.ReferenceGraph{Version: ChangePlanReferenceGraphVersion, Nodes: []changeplanmodel.ReferenceNode{}, Edges: []changeplanmodel.ReferenceEdge{}}
	for _, node := range builder.nodes {
		graph.Nodes = append(graph.Nodes, node)
	}
	for _, edge := range builder.edges {
		graph.Edges = append(graph.Edges, edge)
	}
	sort.Slice(graph.Nodes, func(i, j int) bool {
		return graph.Nodes[i].ResourceType+"\x00"+graph.Nodes[i].ResourceKey < graph.Nodes[j].ResourceType+"\x00"+graph.Nodes[j].ResourceKey
	})
	sort.Slice(graph.Edges, func(i, j int) bool {
		left := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s", graph.Edges[i].FromType, graph.Edges[i].FromKey, graph.Edges[i].ToType, graph.Edges[i].ToKey, graph.Edges[i].Kind, graph.Edges[i].Path)
		right := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s", graph.Edges[j].FromType, graph.Edges[j].FromKey, graph.Edges[j].ToType, graph.Edges[j].ToKey, graph.Edges[j].Kind, graph.Edges[j].Path)
		return left < right
	})
	payload, _ := json.Marshal(graph)
	sum := sha256.Sum256(payload)
	graph.Hash = hex.EncodeToString(sum[:])
	return graph
}

func ChangePlanReferenceImpact(graph changeplanmodel.ReferenceGraph, resourceType, resourceKey string) changeplanmodel.ReferenceImpact {
	return changeplanpolicy.ChangePlanReferenceImpact(graph, resourceType, resourceKey)
}
