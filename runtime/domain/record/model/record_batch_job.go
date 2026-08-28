package recordmodel

type RecordBatchJob struct {
	ID               string `json:"id"`
	WorkspaceID      string `json:"workspace_id"`
	Kind             string `json:"kind"`
	ObjectKey        string `json:"object_key"`
	Status           string `json:"status"`
	IdempotencyKey   string `json:"-"`
	Fingerprint      string `json:"-"`
	PayloadJSON      string `json:"-"`
	Checkpoint       int    `json:"checkpoint"`
	CheckpointCursor string `json:"-"`
	Total            int    `json:"total"`
	ResultFilename   string `json:"result_filename,omitempty"`
	ResultType       string `json:"result_content_type,omitempty"`
	ResultChunks     int    `json:"result_chunks,omitempty"`
	AuditID          string `json:"audit_id,omitempty"`
	ResultArtifactID string `json:"result_artifact_id,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
	AttemptCount     int    `json:"attempt_count"`
	NextAttemptAt    string `json:"-"`
	LeaseOwner       string `json:"-"`
	LeaseExpiresAt   string `json:"-"`
	FencingToken     int64  `json:"-"`
	ActorID          string `json:"actor_id"`
	RoleKey          string `json:"role_key"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type RecordBatchJobChunk struct {
	WorkspaceID string
	JobID       string
	Sequence    int
	Content     string
}
