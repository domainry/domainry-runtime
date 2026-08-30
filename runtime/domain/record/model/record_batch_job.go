package recordmodel

type RecordBatchJob struct {
	ID               string `json:"id"`
	WorkspaceID      string `json:"workspace_id"`
	Kind             string `json:"kind"`
	ObjectKey        string `json:"object_key"`
	Status           string `json:"status"`
	Checkpoint       int    `json:"checkpoint"`
	Total            int    `json:"total"`
	ResultFilename   string `json:"result_filename,omitempty"`
	ResultType       string `json:"result_content_type,omitempty"`
	ResultArtifactID string `json:"result_artifact_id,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
	ActorID          string `json:"actor_id"`
	RoleKey          string `json:"role_key"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}
