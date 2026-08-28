package contract

import (
	"context"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type WorkflowDecisionStore interface {
	CommitWorkflowDecision(context.Context, transactionmodel.WorkflowDecisionCommit) (bool, error)
}

type WorkflowStateStore interface {
	CommitWorkflowState(context.Context, transactionmodel.WorkflowStateCommit) error
}

type WorkflowDefinitionStore interface {
	InsertDefinition(context.Context, workflowmodel.WorkflowDefinition, workflowmodel.WorkflowDefinitionVersion) error
	GetDefinitionByKey(context.Context, string) (workflowmodel.WorkflowDefinition, bool, error)
	ListDefinitions(context.Context) ([]workflowmodel.WorkflowDefinition, error)
	GetVersion(context.Context, string) (workflowmodel.WorkflowDefinitionVersion, bool, error)
	ListVersions(context.Context, string) ([]workflowmodel.WorkflowDefinitionVersion, error)
	UpdateDraft(context.Context, workflowmodel.WorkflowDefinitionVersion, int) (bool, error)
	InsertDraftVersion(context.Context, string, workflowmodel.WorkflowDefinitionVersion) (bool, error)
	DeleteDraft(context.Context, string, string) (bool, error)
	PublishDraft(context.Context, workflowmodel.WorkflowDefinition, workflowmodel.WorkflowDefinitionVersion, string) (bool, error)
	ArchiveVersion(context.Context, string, string, string) (bool, error)
	SetDefinitionEnabled(context.Context, string, bool, string) (bool, error)
}

type WorkflowProcessStore interface {
	InsertProcess(context.Context, string, workflowmodel.WorkflowProcessInstance) error
	UpdateProcess(context.Context, string, workflowmodel.WorkflowProcessInstance) error
	GetProcess(context.Context, string, string) (workflowmodel.WorkflowProcessInstance, bool, error)
	ListProcesses(context.Context, string, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error)
	InsertNode(context.Context, string, workflowmodel.WorkflowNodeInstance) error
	UpdateNode(context.Context, string, workflowmodel.WorkflowNodeInstance) error
	ListNodes(context.Context, string, string) ([]workflowmodel.WorkflowNodeInstance, error)
	InsertTask(context.Context, string, workflowmodel.WorkflowTask) error
	UpdateTask(context.Context, string, workflowmodel.WorkflowTask) error
	GetTask(context.Context, string, string) (workflowmodel.WorkflowTask, bool, error)
	ListTasks(context.Context, string, string, string, string, int) ([]workflowmodel.WorkflowTask, error)
	DecideTask(context.Context, string, string, string, string, string, string) (workflowmodel.WorkflowTask, bool, error)
	InsertEvent(context.Context, string, workflowmodel.WorkflowProcessEvent) error
	ListEvents(context.Context, string, string, int) ([]workflowmodel.WorkflowProcessEvent, error)
}

type WorkflowWorkerStore interface {
	InsertExecution(context.Context, string, workflowmodel.WorkflowExecution) error
	TryBeginExecution(context.Context, workflowmodel.WorkflowExecutionClaimRequest) (workflowmodel.WorkflowExecutionClaimResult, error)
	CompleteExecutionReceipt(context.Context, workflowmodel.WorkflowExecutionReceiptCompletion) error
	GetExecution(context.Context, string, string) (workflowmodel.WorkflowExecution, bool, error)
	ListExecutions(context.Context, string, int) ([]workflowmodel.WorkflowExecution, error)
	UpdateExecution(context.Context, string, workflowmodel.WorkflowExecution) error
	UpdateExecutionWhere(context.Context, string, workflowmodel.WorkflowExecution, map[string]any) (bool, error)
	ListTasks(context.Context, string, string, string, string, int) ([]workflowmodel.WorkflowTask, error)
	GetProcess(context.Context, string, string) (workflowmodel.WorkflowProcessInstance, bool, error)
	ListProcessEvents(context.Context, string, string, int) ([]workflowmodel.WorkflowProcessEvent, error)
	UpdateTask(context.Context, string, workflowmodel.WorkflowTask) error
	InsertProcessEvent(context.Context, string, workflowmodel.WorkflowProcessEvent) error
}
