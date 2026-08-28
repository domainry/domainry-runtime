package contract

type CapabilityAuthoringSuccessorSummary struct {
	Key                string `json:"key"`
	Domain             string `json:"domain"`
	Status             string `json:"status"`
	DetailEndpoint     string `json:"detail_endpoint"`
	ValidationEndpoint string `json:"validation_endpoint,omitempty"`
}

type CapabilityAuthoringSuccessProjection struct {
	SnapshotHash        string                                `json:"snapshot_hash"`
	AvailableSuccessors []CapabilityAuthoringSuccessorSummary `json:"available_successors"`
}
