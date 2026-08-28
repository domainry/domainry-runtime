package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func WorkflowEdgeMatches(edge definitionmodel.WorkflowGraphEdge, outcome string) bool {
	branch := strings.ToLower(strings.TrimSpace(edge.Branch))
	if branch == "" {
		branch = strings.ToLower(strings.TrimSpace(edge.Label))
	}
	if branch == "" {
		return outcome != "false" && outcome != "rejected"
	}
	switch outcome {
	case "true", "approved":
		return branch == outcome || branch == "true" || branch == "approved" || branch == "success"
	case "false", "rejected":
		return branch == outcome || branch == "false" || branch == "rejected" || branch == "failure"
	default:
		return branch == outcome
	}
}

func WorkflowGraphTrigger(graph *definitionmodel.WorkflowGraphSchema) definitionmodel.WorkflowGraphNode {
	if graph != nil {
		for _, node := range graph.Nodes {
			if node.Type == "trigger" {
				return node
			}
		}
	}
	return definitionmodel.WorkflowGraphNode{}
}

func WorkflowGraphNode(graph *definitionmodel.WorkflowGraphSchema, nodeID string) (definitionmodel.WorkflowGraphNode, bool) {
	if graph != nil {
		for _, node := range graph.Nodes {
			if node.ID == nodeID {
				return node, true
			}
		}
	}
	return definitionmodel.WorkflowGraphNode{}, false
}

func WorkflowDefinitionHash(workflow definitionmodel.WorkflowSchema) string {
	raw, _ := json.Marshal(workflow)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func WorkflowPublishedVersion(workflow definitionmodel.WorkflowSchema) int {
	if workflow.PublishedVersion > 0 {
		return workflow.PublishedVersion
	}
	if workflow.Graph != nil {
		return workflow.Graph.Version
	}
	return 0
}

func WorkflowGraphContractVersion(workflow definitionmodel.WorkflowSchema) int {
	if workflow.Graph != nil {
		return workflow.Graph.Version
	}
	return 0
}

func WorkflowRemoveString(values []string, value string) []string {
	out := []string{}
	for _, existing := range values {
		if existing != value {
			out = append(out, existing)
		}
	}
	return out
}
