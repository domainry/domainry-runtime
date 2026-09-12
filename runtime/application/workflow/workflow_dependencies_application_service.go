package workflow

import (
	"context"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type WorkflowDependencies struct {
	Definitions              workflowcontract.WorkflowDefinitionStore
	Processes                workflowcontract.WorkflowProcessStore
	Workers                  workflowcontract.WorkflowWorkerStore
	Decisions                workflowcontract.WorkflowDecisionStore
	Routes                   workflowcontract.WorkflowRouteStore
	WorkflowRegistry         WorkflowRegistry
	WaitTimers               WorkflowWaitTimerService
	ApprovalDeadlineTimers   WorkflowApprovalDeadlineTimerService
	Identity                 identitysdk.Projection
	Principals               identitysdk.PrincipalResolver
	WorkloadReleases         *WorkflowWorkloadReleaseState
	Schema                   WorkflowSchemaProvider
	RecordReader             WorkflowRecordReader
	ObjectForAction          func(context.Context, principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	ObjectMap                func(context.Context) map[string]definitionmodel.ObjectSchema
	CanAccessRecord          func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	ActionExists             func(context.Context, string) bool
	InvokeAction             func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error)
	Audit                    func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any)
	AuditMetadata            func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
	Worker                   workerplatform.Dependencies
	CompileNotification      func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	TaskNotificationCommit   WorkflowTaskNotificationCommitter
	StartAgentTask           func(context.Context, WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error)
	WakeWorkflowContinuation func(string, string)
}

type WorkflowAgentTaskPreparation struct {
	RunID                  string
	WorkspaceID            string
	ProcessID              string
	NodeInstanceID         string
	NodeID                 string
	Iteration              int
	DefinitionVersionID    string
	DefinitionSnapshotHash string
	ManifestHash           string
	Contract               definitionmodel.WorkflowAgentTaskNodeContract
	Input                  map[string]any
	Initiator              principalmodel.Principal
	CorrelationID          string
}

type WorkflowTaskNotificationCommitter interface {
	CommitWorkflowTaskNotification(context.Context, string, workflowmodel.WorkflowTask, notificationmodel.NotificationEvent) error
	CommitWorkflowTaskOpeningNotification(context.Context, string, workflowmodel.WorkflowTask, notificationmodel.NotificationEvent) error
	CommitWorkflowTaskReminderNotification(context.Context, string, workflowmodel.WorkflowProcessEvent, notificationmodel.NotificationEvent) error
	CommitWorkflowTaskEscalationNotification(context.Context, string, workflowmodel.WorkflowTask, workflowmodel.WorkflowProcessEvent, []notificationmodel.NotificationEvent) error
}

type WorkflowRegistry interface {
	List() []definitionmodel.WorkflowSchema
	Get(string) (definitionmodel.WorkflowSchema, bool)
	Set(string, definitionmodel.WorkflowSchema)
	Delete(string)
	Count() int
}

type WorkflowSchemaSnapshot struct {
	Actions                []definitionmodel.ActionSchema
	AgentTasks             []agentsdk.AgentTaskDefinition
	AgentServicePrincipals []agentsdk.AgentServicePrincipalBinding
	Dictionaries           []appschemamodel.DictionarySchema
	Integrations           connectormodel.IntegrationSchema
}

type WorkflowSchemaProvider interface {
	WorkflowSchemaSnapshot(context.Context, principalmodel.Principal) WorkflowSchemaSnapshot
	ConnectorAdapterExists(context.Context, string) bool
}

type WorkflowRecordReader interface {
	GetWorkflowRecord(context.Context, string, definitionmodel.ObjectSchema, string, principalmodel.Principal) (recordmodel.Record, bool, error)
	ListWorkflowRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
}

type WorkflowWaitTimerRequest struct {
	WorkspaceID string
	ProcessID   string
	NodeID      string
	ObjectKey   string
	RecordID    string
	Contract    definitionmodel.WorkflowTimerNodeContract
	Variables   map[string]any
	CreatedAt   time.Time
}

type WorkflowWaitTimerService interface {
	ScheduleWorkflowWaitTimer(context.Context, WorkflowWaitTimerRequest) (string, error)
}

type WorkflowApprovalDeadlineTimerRequest struct {
	WorkspaceID string
	ProcessID   string
	NodeID      string
	TaskID      string
	Phase       string
	DueAt       time.Time
	CreatedAt   time.Time
}

type WorkflowApprovalDeadlineTimerService interface {
	ScheduleWorkflowApprovalDeadlineTimer(context.Context, WorkflowApprovalDeadlineTimerRequest) (string, error)
}

type WorkflowBusinessActionSource string

const WorkflowBusinessActionSourceWorkflow WorkflowBusinessActionSource = "workflow"

type WorkflowBusinessActionInvocation struct {
	ActionKey      string
	ObjectKey      string
	RecordID       string
	Input          map[string]any
	Principal      principalmodel.Principal
	Actor          principalmodel.Principal
	Source         WorkflowBusinessActionSource
	ProcessID      string
	NodeID         string
	IdempotencyKey string
}

type WorkflowBusinessActionRecordResult struct {
	RecordID string
}

type WorkflowBusinessActionInvocationResult struct {
	InvocationID string
	Status       string
	Output       map[string]any
	ErrorCode    string
	Retryable    bool
	OutboxIDs    []string
	Record       *WorkflowBusinessActionRecordResult
}
