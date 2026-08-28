package automationmodel

type AutomationRuleExecution struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	RuleKey       string         `json:"rule_key"`
	ObjectKey     string         `json:"object_key"`
	RecordID      string         `json:"record_id,omitempty"`
	Phase         string         `json:"phase"`
	Operation     string         `json:"operation"`
	Status        string         `json:"status"`
	ActorID       string         `json:"actor_id,omitempty"`
	RoleKey       string         `json:"role_key,omitempty"`
	RequestID     string         `json:"request_id,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	EventID       string         `json:"event_id,omitempty"`
	DurationMS    int64          `json:"duration_ms"`
	ErrorCode     string         `json:"error_code,omitempty"`
	Candidate     map[string]any `json:"candidate,omitempty"`
	Trace         map[string]any `json:"trace"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
}

type AutomationInstructionExecution struct {
	ID             string         `json:"id"`
	WorkspaceID    string         `json:"workspace_id"`
	IdempotencyKey string         `json:"idempotency_key"`
	RuleKey        string         `json:"rule_key"`
	ObjectKey      string         `json:"object_key"`
	RecordID       string         `json:"record_id"`
	RecordVersion  string         `json:"record_version"`
	Operation      string         `json:"operation"`
	InstructionKey string         `json:"instruction_key"`
	Status         string         `json:"status"`
	Result         map[string]any `json:"result,omitempty"`
	ErrorCode      string         `json:"error_code,omitempty"`
	LeaseOwner     string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt string         `json:"lease_expires_at,omitempty"`
	FencingToken   int64          `json:"fencing_token,omitempty"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}

type AutomationExecutionFilter struct {
	RuleKey      string
	ObjectKey    string
	RecordID     string
	Phase        string
	Status       string
	ConnectorKey string
	From         string
	To           string
	Limit        int
}

type AutomationInstructionResult struct {
	Key          string         `json:"key"`
	Type         string         `json:"type"`
	Status       string         `json:"status"`
	ErrorCode    string         `json:"error_code,omitempty"`
	Message      string         `json:"message,omitempty"`
	InvocationID string         `json:"invocation_id,omitempty"`
	OutboxIDs    []string       `json:"outbox_ids,omitempty"`
	ObjectKey    string         `json:"object_key,omitempty"`
	RecordID     string         `json:"record_id,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
}

type AutomationRenderContext struct {
	Payload map[string]any
	Record  map[string]any
	Input   map[string]any
	Before  map[string]any
	Actor   map[string]any
	Event   map[string]any
	Results map[string]AutomationInstructionResult
}
