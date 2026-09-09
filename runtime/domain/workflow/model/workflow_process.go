package workflowmodel

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

type WorkflowProcessInstance struct {
	WorkspaceID         string                         `json:"workspace_id"`
	ID                  string                         `json:"id"`
	WorkflowKey         string                         `json:"workflow_key"`
	WorkflowName        string                         `json:"workflow_name"`
	DefinitionVersionID string                         `json:"workflow_definition_version_id,omitempty"`
	DefinitionVersion   int                            `json:"definition_version"`
	DefinitionHash      string                         `json:"definition_hash,omitempty"`
	DefinitionSnapshot  definitionmodel.WorkflowSchema `json:"definition_snapshot"`
	ObjectKey           string                         `json:"object_key,omitempty"`
	RecordID            string                         `json:"record_id,omitempty"`
	InitiatorID         string                         `json:"initiator_id"`
	InitiatorRoleKey    string                         `json:"initiator_role_key,omitempty"`
	Status              string                         `json:"status"`
	CurrentNodeIDs      []string                       `json:"current_node_ids,omitempty"`
	Variables           map[string]any                 `json:"variables,omitempty"`
	Result              map[string]any                 `json:"result,omitempty"`
	ErrorCode           string                         `json:"error_code,omitempty"`
	CreatedAt           string                         `json:"created_at"`
	UpdatedAt           string                         `json:"updated_at"`
	CompletedAt         string                         `json:"completed_at,omitempty"`
	CurrentNodeNames    []string                       `json:"current_node_names,omitempty"`
	FailedNodeNames     []string                       `json:"failed_node_names,omitempty"`
	WaitingSeconds      int64                          `json:"waiting_seconds,omitempty"`
	WorkflowRetryCount  int                            `json:"retry_count,omitempty"`
	BusinessOutcome     string                         `json:"business_outcome,omitempty"`
}

type WorkflowProcessFilter struct {
	ProcessID         string
	WorkflowKey       string
	DefinitionVersion int
	ObjectKey         string
	RecordID          string
	Status            string
	Statuses          []string
	InitiatorID       string
	ApproverID        string
	VisibleToUserID   string
	UpdatedFrom       string
	UpdatedTo         string
	Limit             int
}

type WorkflowNodeInstance struct {
	WorkspaceID string         `json:"workspace_id"`
	ID          string         `json:"id"`
	ProcessID   string         `json:"process_id"`
	NodeID      string         `json:"node_id"`
	NodeType    string         `json:"node_type"`
	Iteration   int            `json:"iteration"`
	Status      string         `json:"status"`
	Input       map[string]any `json:"input,omitempty"`
	Output      map[string]any `json:"output,omitempty"`
	ErrorCode   string         `json:"error_code,omitempty"`
	StartedAt   string         `json:"started_at"`
	CompletedAt string         `json:"completed_at,omitempty"`
}

type WorkflowTask struct {
	WorkspaceID           string                                     `json:"workspace_id"`
	ID                    string                                     `json:"id"`
	ProcessID             string                                     `json:"process_id"`
	NodeInstanceID        string                                     `json:"node_instance_id"`
	NodeID                string                                     `json:"node_id"`
	Title                 string                                     `json:"title"`
	AssigneeUserID        string                                     `json:"assignee_user_id,omitempty"`
	AssigneeName          string                                     `json:"assignee_name,omitempty"`
	AssigneeRoleKey       string                                     `json:"assignee_role_key,omitempty"`
	ResolverSnapshot      []definitionmodel.WorkflowAssigneeResolver `json:"resolver_snapshot,omitempty"`
	CandidateSource       string                                     `json:"candidate_source,omitempty"`
	NodeDefinitionVersion int                                        `json:"node_definition_version,omitempty"`
	Sequence              int                                        `json:"sequence"`
	Status                string                                     `json:"status"`
	Decision              string                                     `json:"decision,omitempty"`
	Comment               string                                     `json:"comment,omitempty"`
	DueAt                 string                                     `json:"due_at,omitempty"`
	CompletedBy           string                                     `json:"completed_by,omitempty"`
	CompletedAt           string                                     `json:"completed_at,omitempty"`
	CreatedAt             string                                     `json:"created_at"`
	UpdatedAt             string                                     `json:"updated_at"`
}

type WorkflowProcessEvent struct {
	WorkspaceID string         `json:"workspace_id"`
	ID          string         `json:"id"`
	ProcessID   string         `json:"process_id"`
	NodeID      string         `json:"node_id,omitempty"`
	TaskID      string         `json:"task_id,omitempty"`
	Event       string         `json:"event"`
	ActorID     string         `json:"actor_id"`
	Summary     string         `json:"summary"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   string         `json:"created_at"`
}

type WorkflowTaskDecisionRequest struct {
	Decision       string `json:"decision"`
	Comment        string `json:"comment,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// NextStep configures the route step that follows the one this decision
	// completes. It is only meaningful for a route-driven approval node and is
	// rejected on a rejection or a return.
	NextStep *WorkflowNextStepConfiguration `json:"next_step,omitempty"`
}
