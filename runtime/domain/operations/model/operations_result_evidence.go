package operationsmodel

const OperationsResultArtifactSchemaV1 = "operations.result-artifact.v1"

// OperationsResultArtifactPointer is the only large-result evidence stored in
// _operations. The governed response body lives in the shared Artifact store.
type OperationsResultArtifactPointer struct {
	Schema        string `json:"schema"`
	ArtifactID    string `json:"artifact_id"`
	ContentSHA256 string `json:"content_sha256"`
	SizeBytes     int64  `json:"size_bytes"`
}

type OperationsResultEvidence struct {
	Artifact *OperationsResultArtifactPointer `json:"_operations_result_artifact,omitempty"`
}
