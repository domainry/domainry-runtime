package transactionmodel

import recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

import (
	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type RecordMutationCommit struct {
	Operation           string                                     `json:"operation"`
	Object              definitionmodel.ObjectSchema               `json:"object"`
	Record              recordmodel.Record                         `json:"record"`
	RecordID            string                                     `json:"record_id,omitempty"`
	ExpectedUpdatedAt   string                                     `json:"expected_updated_at,omitempty"`
	Optimistic          OptimisticPrecondition                     `json:"optimistic,omitempty"`
	Conditions          map[string]any                             `json:"conditions,omitempty"`
	Predicates          []MutationPredicate                        `json:"predicates,omitempty"`
	AuthorizationScope  *recordmodel.RecordScopeExpression         `json:"-"`
	Audit               *auditmodel.AuditEvent                     `json:"audit,omitempty"`
	Audits              []auditmodel.AuditEvent                    `json:"audits,omitempty"`
	Outbox              []publicationmodel.Message                 `json:"outbox,omitempty"`
	WorkflowIntents     []workflowmodel.WorkflowExecution          `json:"workflow_intents,omitempty"`
	WorkflowWithdrawals []WorkflowWithdrawalCommit                 `json:"workflow_withdrawals,omitempty"`
	WorkflowStarts      []WorkflowStartCommit                      `json:"workflow_starts,omitempty"`
	NotificationEvents  []notificationmodel.NotificationEvent      `json:"notification_events,omitempty"`
	LocalizedValues     []recordmodel.RecordLocalizedValueMutation `json:"localized_values,omitempty"`
	// Set fields are Runtime-internal authority for one conditional update-many
	// statement. They are assembled only from a locked, authorized record page
	// and never accepted from project Handler JSON.
	SetRecordIDs              []string                            `json:"set_record_ids,omitempty"`
	SetFilterExpression       *recordmodel.RecordFilterExpression `json:"-"`
	SetExpectedAffected       int                                 `json:"set_expected_affected,omitempty"`
	SetOwnerOrganizationScope string                              `json:"-"`
	SetExactCoverageField     string                              `json:"set_exact_coverage_field,omitempty"`
	SetExactCoverageValues    []any                               `json:"set_exact_coverage_values,omitempty"`
}

// WorkflowStartCommit is one Workflow start staged by an Action. The process
// row, its per-instance approval route and the claiming intent are written in
// the same transaction as the Action's record mutations, so a rolled back
// Action leaves no process, no route and no intent behind. The process is
// durable as "starting" and the committed intent activates its first node.
type WorkflowStartCommit struct {
	Process    workflowmodel.WorkflowProcessInstance `json:"process"`
	RouteSteps []workflowmodel.WorkflowRouteStep     `json:"route_steps,omitempty"`
	Intent     workflowmodel.WorkflowExecution       `json:"intent"`
}

// MutationPredicate is a storage-neutral compare-and-set condition evaluated
// by the same SQL statement that applies the record mutation.
type MutationPredicate struct {
	Field     string `json:"field"`
	Operator  string `json:"operator"`
	Value     any    `json:"value,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

// MutationArithmetic describes a planner-owned numeric change. The planner
// lowers it to a canonical patch plus an equality predicate on the observed
// value, closing the read/write race without a second execution path.
type MutationArithmetic struct {
	Field     string `json:"field"`
	Operation string `json:"operation"`
	Operand   any    `json:"operand"`
}

type ConditionalUpdateInput struct {
	Patch      map[string]any       `json:"patch,omitempty"`
	Arithmetic []MutationArithmetic `json:"arithmetic,omitempty"`
	Predicates []MutationPredicate  `json:"predicates,omitempty"`
}

// OptimisticPrecondition is the canonical owner-neutral concurrency contract.
// ExpectedVersion validates a business version field while ExpectedUpdatedAt
// is the final storage compare-and-swap token that closes the read/write race.
type OptimisticPrecondition struct {
	ExpectedVersion   *int64 `json:"expected_version,omitempty"`
	ExpectedUpdatedAt string `json:"expected_updated_at,omitempty"`
}

func (c RecordMutationCommit) OptimisticUpdatedAt() string {
	if c.Optimistic.ExpectedUpdatedAt != "" {
		return c.Optimistic.ExpectedUpdatedAt
	}
	return c.ExpectedUpdatedAt
}

// WorkflowDecisionCommit is the storage-neutral unit of work for an approval
// decision. Service code describes the final durable state; storage owns the
// transaction and never exposes database/sql types above the repository layer.
type WorkflowDecisionCommit struct {
	WorkspaceID              string                                 `json:"workspace_id"`
	DecidedTask              workflowmodel.WorkflowTask             `json:"decided_task"`
	ExpectedTaskStatus       string                                 `json:"expected_task_status"`
	ExpectedAssigneeID       string                                 `json:"expected_assignee_id"`
	ExpectedProcessUpdatedAt string                                 `json:"expected_process_updated_at,omitempty"`
	Process                  *workflowmodel.WorkflowProcessInstance `json:"process,omitempty"`
	InsertNodes              []workflowmodel.WorkflowNodeInstance   `json:"insert_nodes,omitempty"`
	UpdateNodes              []workflowmodel.WorkflowNodeInstance   `json:"update_nodes,omitempty"`
	InsertTasks              []workflowmodel.WorkflowTask           `json:"insert_tasks,omitempty"`
	UpdateTasks              []workflowmodel.WorkflowTask           `json:"update_tasks,omitempty"`
	UpdateRouteSteps         []workflowmodel.WorkflowRouteStep      `json:"update_route_steps,omitempty"`
	Events                   []workflowmodel.WorkflowProcessEvent   `json:"events,omitempty"`
	RecordMutations          []RecordMutationCommit                 `json:"record_mutations,omitempty"`
	WorkflowExecution        *workflowmodel.WorkflowExecution       `json:"workflow_execution,omitempty"`
	InsertExecutions         []workflowmodel.WorkflowExecution      `json:"insert_executions,omitempty"`
	NotificationEvents       []notificationmodel.NotificationEvent  `json:"notification_events,omitempty"`
}

// WorkflowStateCommit atomically persists a process state transition that is
// not itself a new task decision, such as restoring a failed decision attempt
// or resolving an operator-visible process failure.
type WorkflowStateCommit struct {
	WorkspaceID       string                                 `json:"workspace_id"`
	Process           *workflowmodel.WorkflowProcessInstance `json:"process,omitempty"`
	InsertNodes       []workflowmodel.WorkflowNodeInstance   `json:"insert_nodes,omitempty"`
	UpdateNodes       []workflowmodel.WorkflowNodeInstance   `json:"update_nodes,omitempty"`
	UpdateTasks       []workflowmodel.WorkflowTask           `json:"update_tasks,omitempty"`
	Events            []workflowmodel.WorkflowProcessEvent   `json:"events,omitempty"`
	WorkflowExecution *workflowmodel.WorkflowExecution       `json:"workflow_execution,omitempty"`
	InsertExecutions  []workflowmodel.WorkflowExecution      `json:"insert_executions,omitempty"`
}

// WorkflowWithdrawalCommit compares the observed process revision and cancels
// its active children in the same transaction as the business mutations.
type WorkflowWithdrawalCommit struct {
	Process           workflowmodel.WorkflowProcessInstance
	ExpectedUpdatedAt string
	ExpectedStatus    string
	CommandID         string
	ActorID           string
}
