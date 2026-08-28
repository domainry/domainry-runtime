package changeplanmodel

type ReferenceGraph struct {
	Version string          `json:"version"`
	Hash    string          `json:"hash"`
	Nodes   []ReferenceNode `json:"nodes"`
	Edges   []ReferenceEdge `json:"edges"`
}

func (graph ReferenceGraph) ChangePlanReferenceGraph() ReferenceGraph { return graph }

type ReferenceEdge struct {
	FromType string `json:"from_type"`
	FromKey  string `json:"from_key"`
	ToType   string `json:"to_type"`
	ToKey    string `json:"to_key"`
	Kind     string `json:"kind"`
	Path     string `json:"path,omitempty"`
}

type ReferenceNode struct {
	ResourceType string `json:"resource_type"`
	ResourceKey  string `json:"resource_key"`
	ObjectKey    string `json:"object_key,omitempty"`
	Label        string `json:"label,omitempty"`
	Owner        string `json:"owner,omitempty"`
}

type ReferenceImpact struct {
	ResourceType       string          `json:"resource_type"`
	ResourceKey        string          `json:"resource_key"`
	GraphHash          string          `json:"graph_hash"`
	DirectConsumers    []ReferenceEdge `json:"direct_consumers"`
	DirectDependencies []ReferenceEdge `json:"direct_dependencies"`
	IndirectConsumers  []ReferenceEdge `json:"indirect_consumers"`
	DeletionBlocked    bool            `json:"deletion_blocked"`
}
