package workflow

import (
	"context"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

// BusinessWorkflowTaskDTO intentionally omits resolver snapshots and Runtime
// execution details. It is the participant-facing task contract.
type BusinessWorkflowTaskDTO struct {
	ID              string `json:"id"`
	ProcessID       string `json:"process_id"`
	NodeInstanceID  string `json:"node_instance_id"`
	NodeID          string `json:"node_id"`
	Title           string `json:"title"`
	AssigneeUserID  string `json:"assignee_user_id,omitempty"`
	AssigneeName    string `json:"assignee_name,omitempty"`
	AssigneeRoleKey string `json:"assignee_role_key,omitempty"`
	Sequence        int    `json:"sequence"`
	Status          string `json:"status"`
	Decision        string `json:"decision,omitempty"`
	Comment         string `json:"comment,omitempty"`
	DueAt           string `json:"due_at,omitempty"`
	CompletedBy     string `json:"completed_by,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type BusinessWorkflowProcessDTO struct {
	ID               string   `json:"id"`
	WorkflowKey      string   `json:"workflow_key"`
	WorkflowName     string   `json:"workflow_name"`
	ObjectKey        string   `json:"object_key,omitempty"`
	RecordID         string   `json:"record_id,omitempty"`
	InitiatorID      string   `json:"initiator_id"`
	Status           string   `json:"status"`
	CurrentNodeNames []string `json:"current_node_names,omitempty"`
	BusinessOutcome  string   `json:"business_outcome,omitempty"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	CompletedAt      string   `json:"completed_at,omitempty"`
}

type BusinessWorkflowRunDTO struct {
	WorkflowKey string `json:"workflow_key"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	ProcessID   string `json:"process_id,omitempty"`
}

type BusinessWorkflowNodeDTO struct {
	ID          string `json:"id"`
	NodeID      string `json:"node_id"`
	Name        string `json:"name"`
	NodeType    string `json:"node_type"`
	ActionKey   string `json:"action_key,omitempty"`
	Iteration   int    `json:"iteration"`
	Status      string `json:"status"`
	ErrorCode   string `json:"error_code,omitempty"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type BusinessWorkflowEventDTO struct {
	ID        string `json:"id"`
	NodeID    string `json:"node_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Event     string `json:"event"`
	ActorID   string `json:"actor_id,omitempty"`
	Summary   string `json:"summary,omitempty"`
	CreatedAt string `json:"created_at"`
}

type BusinessWorkflowProcessDetailDTO struct {
	Process BusinessWorkflowProcessDTO `json:"process"`
	Nodes   []BusinessWorkflowNodeDTO  `json:"nodes"`
	Tasks   []BusinessWorkflowTaskDTO  `json:"tasks"`
	Events  []BusinessWorkflowEventDTO `json:"events"`
}

type OpsWorkflowExecutionDTO struct {
	ID             string `json:"id"`
	WorkflowKey    string `json:"workflow_key"`
	Status         string `json:"status"`
	ProcessID      string `json:"process_id,omitempty"`
	NodeID         string `json:"node_id,omitempty"`
	Attempt        int    `json:"attempt"`
	MaxAttempts    int    `json:"max_attempts"`
	NextRunAt      string `json:"next_run_at,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	LeaseOwner     string `json:"lease_owner,omitempty"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	FencingToken   int64  `json:"fencing_token,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type OpsWorkflowProcessDTO struct {
	ID                  string   `json:"id"`
	WorkflowKey         string   `json:"workflow_key"`
	DefinitionVersionID string   `json:"workflow_definition_version_id,omitempty"`
	DefinitionVersion   int      `json:"definition_version"`
	DefinitionHash      string   `json:"definition_hash,omitempty"`
	Status              string   `json:"status"`
	CurrentNodeIDs      []string `json:"current_node_ids,omitempty"`
	FailedNodeNames     []string `json:"failed_node_names,omitempty"`
	ErrorCode           string   `json:"error_code,omitempty"`
	RetryCount          int      `json:"retry_count,omitempty"`
	ObjectKey           string   `json:"object_key,omitempty"`
	RecordID            string   `json:"record_id,omitempty"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
	CompletedAt         string   `json:"completed_at,omitempty"`
}

type OpsWorkflowNodeDTO struct {
	ID          string `json:"id"`
	NodeID      string `json:"node_id"`
	NodeType    string `json:"node_type"`
	Status      string `json:"status"`
	ErrorCode   string `json:"error_code,omitempty"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type OpsWorkflowEventDTO struct {
	ID        string `json:"id"`
	NodeID    string `json:"node_id,omitempty"`
	Event     string `json:"event"`
	ActorID   string `json:"actor_id,omitempty"`
	Summary   string `json:"summary,omitempty"`
	CreatedAt string `json:"created_at"`
}

type OpsWorkflowProcessDetailDTO struct {
	Process OpsWorkflowProcessDTO `json:"process"`
	Nodes   []OpsWorkflowNodeDTO  `json:"nodes"`
	Events  []OpsWorkflowEventDTO `json:"events"`
}

type OpsWorkflowExecutionCommandDTO struct {
	Status    string                  `json:"status"`
	Execution OpsWorkflowExecutionDTO `json:"execution"`
}

type OpsWorkflowProcessBatchDTO struct {
	Processed  int                       `json:"processed"`
	Executions []OpsWorkflowExecutionDTO `json:"executions"`
}

func ProjectBusinessWorkflowProcess(process workflowmodel.WorkflowProcessInstance) BusinessWorkflowProcessDTO {
	return BusinessWorkflowProcessDTO{
		ID: process.ID, WorkflowKey: process.WorkflowKey, WorkflowName: process.WorkflowName,
		ObjectKey: process.ObjectKey, RecordID: process.RecordID, InitiatorID: process.InitiatorID,
		Status: process.Status, CurrentNodeNames: process.CurrentNodeNames, BusinessOutcome: process.BusinessOutcome,
		CreatedAt: process.CreatedAt, UpdatedAt: process.UpdatedAt, CompletedAt: process.CompletedAt,
	}
}

func ProjectBusinessWorkflowRun(result workflowmodel.WorkflowRunResult) BusinessWorkflowRunDTO {
	return BusinessWorkflowRunDTO{
		WorkflowKey: result.WorkflowKey, Name: result.Name, Status: result.Status, ProcessID: result.Execution.ProcessID,
	}
}

func projectBusinessWorkflowTask(task workflowmodel.WorkflowTask) BusinessWorkflowTaskDTO {
	return BusinessWorkflowTaskDTO{
		ID: task.ID, ProcessID: task.ProcessID, NodeInstanceID: task.NodeInstanceID, NodeID: task.NodeID,
		Title: task.Title, AssigneeUserID: task.AssigneeUserID, AssigneeName: task.AssigneeName,
		AssigneeRoleKey: task.AssigneeRoleKey, Sequence: task.Sequence, Status: task.Status,
		Decision: task.Decision, Comment: task.Comment, DueAt: task.DueAt,
		CompletedBy: task.CompletedBy, CompletedAt: task.CompletedAt,
		CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
}

func ProjectBusinessWorkflowProcessDetail(detail WorkflowProcessDetail) BusinessWorkflowProcessDetailDTO {
	projected := BusinessWorkflowProcessDetailDTO{
		Process: ProjectBusinessWorkflowProcess(detail.Process),
		Nodes:   make([]BusinessWorkflowNodeDTO, 0, len(detail.Nodes)),
		Tasks:   make([]BusinessWorkflowTaskDTO, 0, len(detail.Tasks)),
		Events:  make([]BusinessWorkflowEventDTO, 0, len(detail.Events)),
	}
	for _, node := range detail.Nodes {
		name, actionKey := node.NodeID, ""
		if detail.Process.DefinitionSnapshot.Graph != nil {
			for _, definitionNode := range detail.Process.DefinitionSnapshot.Graph.Nodes {
				if definitionNode.ID != node.NodeID {
					continue
				}
				if strings.TrimSpace(definitionNode.Name) != "" {
					name = definitionNode.Name
				}
				if definitionNode.Contract != nil && definitionNode.Contract.Action != nil {
					actionKey = definitionNode.Contract.Action.ActionKey
				}
				if actionKey == "" && definitionNode.Config != nil {
					actionKey, _ = definitionNode.Config["action_key"].(string)
				}
				break
			}
		}
		projected.Nodes = append(projected.Nodes, BusinessWorkflowNodeDTO{
			ID: node.ID, NodeID: node.NodeID, Name: name, NodeType: node.NodeType, ActionKey: actionKey, Iteration: node.Iteration,
			Status: node.Status, ErrorCode: node.ErrorCode, StartedAt: node.StartedAt, CompletedAt: node.CompletedAt,
		})
	}
	for _, task := range detail.Tasks {
		projected.Tasks = append(projected.Tasks, projectBusinessWorkflowTask(task))
	}
	for _, event := range detail.Events {
		projected.Events = append(projected.Events, BusinessWorkflowEventDTO{
			ID: event.ID, NodeID: event.NodeID, TaskID: event.TaskID, Event: event.Event,
			ActorID: event.ActorID, Summary: event.Summary, CreatedAt: event.CreatedAt,
		})
	}
	return projected
}

func ProjectOpsWorkflowExecution(execution workflowmodel.WorkflowExecution) OpsWorkflowExecutionDTO {
	return OpsWorkflowExecutionDTO{
		ID: execution.ID, WorkflowKey: execution.WorkflowKey, Status: execution.Status,
		ProcessID: execution.ProcessID, NodeID: execution.NodeID, Attempt: execution.Attempt,
		MaxAttempts: execution.MaxAttempts, NextRunAt: execution.NextRunAt, LastError: execution.LastError,
		LeaseOwner: execution.LeaseOwner, LeaseExpiresAt: execution.LeaseExpiresAt,
		FencingToken: execution.FencingToken, CreatedAt: execution.CreatedAt, UpdatedAt: execution.UpdatedAt,
	}
}

func ProjectOpsWorkflowProcess(process workflowmodel.WorkflowProcessInstance) OpsWorkflowProcessDTO {
	retryCount := process.WorkflowRetryCount
	if retryCount == 0 {
		retryCount = workflowpolicy.WorkflowRetryCount(process.Result["retry_count"])
	}
	return OpsWorkflowProcessDTO{
		ID: process.ID, WorkflowKey: process.WorkflowKey,
		DefinitionVersionID: process.DefinitionVersionID, DefinitionVersion: process.DefinitionVersion,
		DefinitionHash: process.DefinitionHash, Status: process.Status, CurrentNodeIDs: process.CurrentNodeIDs,
		FailedNodeNames: process.FailedNodeNames, ErrorCode: process.ErrorCode,
		RetryCount: retryCount, ObjectKey: process.ObjectKey, RecordID: process.RecordID,
		CreatedAt: process.CreatedAt, UpdatedAt: process.UpdatedAt, CompletedAt: process.CompletedAt,
	}
}

func (s *WorkflowApplicationService) BusinessWorkflowTasks(ctx context.Context, principal principalmodel.Principal, status string, limit int) ([]BusinessWorkflowTaskDTO, error) {
	tasks, err := s.MyWorkflowTasks(ctx, principal, status, limit)
	if err != nil {
		return nil, err
	}
	result := make([]BusinessWorkflowTaskDTO, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, projectBusinessWorkflowTask(task))
	}
	return result, nil
}

func (s *WorkflowApplicationService) BusinessTeamWorkflowTasks(ctx context.Context, principal principalmodel.Principal, status string, limit int) ([]BusinessWorkflowTaskDTO, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if status == "" {
		status = "open"
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	tasks, err := s.processRepo.ListTasks(ctx, principal.WorkspaceID, "", "", status, limit)
	if err != nil {
		return nil, internalError("list team workflow tasks", err)
	}
	tasks = s.workflowTasksWithAssigneeNames(ctx, tasks)
	result := make([]BusinessWorkflowTaskDTO, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, projectBusinessWorkflowTask(task))
	}
	return result, nil
}

func (s *WorkflowApplicationService) BusinessWorkflowProcesses(ctx context.Context, principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter) ([]BusinessWorkflowProcessDTO, error) {
	processes, err := s.WorkflowProcesses(ctx, principal, filter)
	if err != nil {
		return nil, err
	}
	result := make([]BusinessWorkflowProcessDTO, 0, len(processes))
	for _, process := range processes {
		result = append(result, ProjectBusinessWorkflowProcess(process))
	}
	return result, nil
}

func (s *WorkflowApplicationService) OpsWorkflowExecutions(ctx context.Context, principal principalmodel.Principal, objectKey, recordID string, limit int) ([]OpsWorkflowExecutionDTO, error) {
	executions, err := s.WorkflowExecutions(ctx, principal, objectKey, recordID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]OpsWorkflowExecutionDTO, 0, len(executions))
	for _, execution := range executions {
		result = append(result, ProjectOpsWorkflowExecution(execution))
	}
	return result, nil
}

func (s *WorkflowApplicationService) OpsWorkflowProcesses(ctx context.Context, principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter) ([]OpsWorkflowProcessDTO, error) {
	processes, err := s.workflowProcesses(ctx, principal, filter, false, true)
	if err != nil {
		return nil, err
	}
	result := make([]OpsWorkflowProcessDTO, 0, len(processes))
	for _, process := range processes {
		result = append(result, ProjectOpsWorkflowProcess(process))
	}
	return result, nil
}

func (s *WorkflowApplicationService) OpsWorkflowProcess(ctx context.Context, processID string, principal principalmodel.Principal) (OpsWorkflowProcessDetailDTO, error) {
	detail, err := s.workflowProcess(ctx, strings.TrimSpace(processID), principal, false, true)
	if err != nil {
		return OpsWorkflowProcessDetailDTO{}, err
	}
	detail.Process = s.enrichWorkflowProcessSummary(ctx, detail.Process)
	result := OpsWorkflowProcessDetailDTO{
		Process: ProjectOpsWorkflowProcess(detail.Process),
		Nodes:   make([]OpsWorkflowNodeDTO, 0, len(detail.Nodes)),
		Events:  make([]OpsWorkflowEventDTO, 0, len(detail.Events)),
	}
	for _, node := range detail.Nodes {
		result.Nodes = append(result.Nodes, OpsWorkflowNodeDTO{
			ID: node.ID, NodeID: node.NodeID, NodeType: node.NodeType, Status: node.Status,
			ErrorCode: node.ErrorCode, StartedAt: node.StartedAt, CompletedAt: node.CompletedAt,
		})
	}
	for _, event := range detail.Events {
		result.Events = append(result.Events, OpsWorkflowEventDTO{
			ID: event.ID, NodeID: event.NodeID, Event: event.Event, ActorID: event.ActorID,
			Summary: event.Summary, CreatedAt: event.CreatedAt,
		})
	}
	return result, nil
}

func (s *WorkflowApplicationService) RetryOpsWorkflowProcessWithKey(ctx context.Context, processID, key string, principal principalmodel.Principal) (OpsWorkflowProcessDTO, error) {
	process, err := s.RetryWorkflowProcessWithKey(ctx, processID, key, principal)
	if err != nil {
		return OpsWorkflowProcessDTO{}, err
	}
	return ProjectOpsWorkflowProcess(process), nil
}

func (s *WorkflowApplicationService) ResolveOpsWorkflowProcessFailure(ctx context.Context, processID, note string, principal principalmodel.Principal) (OpsWorkflowProcessDTO, error) {
	process, err := s.ResolveWorkflowProcessFailure(ctx, processID, note, principal)
	if err != nil {
		return OpsWorkflowProcessDTO{}, err
	}
	return ProjectOpsWorkflowProcess(process), nil
}
