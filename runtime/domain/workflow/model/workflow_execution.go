package workflowmodel

type WorkflowExecution struct {
	WorkspaceID    string         `json:"workspace_id"`
	ID             string         `json:"id"`
	OperationID    string         `json:"operation_id,omitempty"`
	WorkflowKey    string         `json:"workflow_key"`
	Name           string         `json:"name"`
	Trigger        string         `json:"trigger"`
	Status         string         `json:"status"`
	ActionType     string         `json:"action_type"`
	Action         map[string]any `json:"action"`
	Payload        map[string]any `json:"payload,omitempty"`
	Result         map[string]any `json:"result,omitempty"`
	ProcessID      string         `json:"process_id,omitempty"`
	NodeID         string         `json:"node_id,omitempty"`
	ObjectKey      string         `json:"object_key,omitempty"`
	RecordID       string         `json:"record_id,omitempty"`
	ActorID        string         `json:"actor_id"`
	RunAs          string         `json:"run_as,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	Attempt        int            `json:"attempt"`
	MaxAttempts    int            `json:"max_attempts"`
	NextRunAt      string         `json:"next_run_at,omitempty"`
	LastError      string         `json:"last_error,omitempty"`
	LeaseOwner     string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt string         `json:"lease_expires_at,omitempty"`
	FencingToken   int64          `json:"fencing_token,omitempty"`
	Message        string         `json:"message"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}
