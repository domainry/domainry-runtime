package workflowmodel

// WorkflowRouteStep is one durable step of a per-instance approval route. The
// rows are the electorate authority for a route-driven approval node: the node
// contract only bounds what a route may contain.
//
// Status lifecycle:
//
//	pending      the step is fully configured and waits for an earlier step
//	configurable the step was deferred and still needs its assignees
//	active       the step's node instance and tasks exist and are open
//	approved     the step reached its threshold
//	rejected     an assignee rejected on this step
//	returned     an assignee returned on this step
//	skipped      the step was never activated
type WorkflowRouteStep struct {
	WorkspaceID       string                  `json:"workspace_id"`
	ID                string                  `json:"id"`
	ProcessID         string                  `json:"process_id"`
	NodeID            string                  `json:"node_id"`
	StepNo            int                     `json:"step_no"`
	StepKey           string                  `json:"step_key"`
	Title             string                  `json:"title,omitempty"`
	Mode              string                  `json:"mode"`
	RequiredApprovals int                     `json:"required_approvals,omitempty"`
	Status            string                  `json:"status"`
	AssigneeSnapshot  []WorkflowRouteAssignee `json:"assignee_snapshot,omitempty"`
	ConfiguredBy      string                  `json:"configured_by,omitempty"`
	ConfiguredAt      string                  `json:"configured_at,omitempty"`
	ConfigureSource   string                  `json:"configure_source,omitempty"`
	NodeInstanceID    string                  `json:"node_instance_id,omitempty"`
	CreatedAt         string                  `json:"created_at"`
	UpdatedAt         string                  `json:"updated_at"`
}

// WorkflowRouteAssignee is one frozen approver of a route step. The display
// name and role are snapshots taken when the step was configured so a later
// Identity change cannot silently rewrite an approval history.
type WorkflowRouteAssignee struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name,omitempty"`
	RoleKey     string `json:"role_key,omitempty"`
}

// WorkflowNextStepConfiguration configures the route step that follows the
// step the decision completes. It is only accepted on an approval.
type WorkflowNextStepConfiguration struct {
	StepKey           string   `json:"step_key,omitempty"`
	AssigneeUserIDs   []string `json:"assignee_user_ids,omitempty"`
	RequiredApprovals int      `json:"required_approvals,omitempty"`
}
