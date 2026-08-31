package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

// WorkflowAgentTaskCompletion is Runtime's local projection of the asynchronous
// Agent capability result. It contains no Agent lease, repository or worker
// state; Runtime owns only the workflow correlation and continuation.
type WorkflowAgentTaskCompletion struct {
	WorkspaceID, TaskRunID, ProcessID, NodeInstanceID string
	TaskKey, TaskVersion                              string
	Identity                                          agentsdk.ExecutionIdentity
	Status                                            string
	Outcome                                           string
	Output                                            map[string]any
	ErrorCode                                         string
	Evidence                                          agentmodel.AgentTaskExecutionEvidence
}

func (s *WorkflowApplicationService) CompleteAgentTask(ctx context.Context, completion WorkflowAgentTaskCompletion) error {
	if s == nil || s.processEngine == nil || s.processRepo == nil {
		return internalError("complete Agent capability task", fmt.Errorf("workflow process runtime is required"))
	}
	return s.completeAgentTask(ctx, completion)
}

func (s *WorkflowApplicationService) completeAgentTask(ctx context.Context, completion WorkflowAgentTaskCompletion) error {
	stateStore, ok := s.processEngine.runtime.dependencies.Decisions.(workflowcontract.WorkflowStateStore)
	if !ok {
		return internalError("complete Agent capability task", fmt.Errorf("workflow state store is required"))
	}
	if strings.TrimSpace(completion.WorkspaceID) == "" || strings.TrimSpace(completion.TaskRunID) == "" || strings.TrimSpace(completion.ProcessID) == "" || strings.TrimSpace(completion.NodeInstanceID) == "" {
		return badRequest("backend.workflow.agent_task_terminal_invalid")
	}
	outcome := workflowAgentCapabilityOutcome(completion.Status, completion.Outcome)
	process, found, err := s.processRepo.GetProcess(ctx, completion.WorkspaceID, completion.ProcessID)
	if err != nil || !found {
		return internalError("load Agent capability process", err)
	}
	nodes, err := s.processRepo.ListNodes(ctx, completion.WorkspaceID, completion.ProcessID)
	if err != nil {
		return internalError("load Agent capability node", err)
	}
	var instance *workflowmodel.WorkflowNodeInstance
	for index := range nodes {
		if nodes[index].ID != completion.NodeInstanceID {
			continue
		}
		if nodes[index].Status != "waiting" {
			if strings.TrimSpace(fmt.Sprint(nodes[index].Input["agent_task_run_id"])) == completion.TaskRunID {
				return nil
			}
			return conflict("backend.workflow.agent_task_node_not_waiting")
		}
		instance = &nodes[index]
		break
	}
	if instance == nil {
		return conflict("backend.workflow.agent_task_node_not_waiting")
	}
	if strings.TrimSpace(fmt.Sprint(instance.Input["agent_task_run_id"])) != strings.TrimSpace(completion.TaskRunID) {
		return conflict("backend.workflow.agent_task_correlation_mismatch")
	}
	node, found := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, instance.NodeID)
	if !found || node.Contract == nil || node.Contract.AgentTask == nil {
		return badRequest("backend.workflow.agent_task_contract_required", "node", instance.NodeID)
	}
	if !containsWorkflowString(node.Contract.AgentTask.AllowedOutcomes, outcome) {
		return badRequest("backend.workflow.agent_task_outcome_invalid", "outcome", outcome)
	}
	now := time.Now().UTC()
	instance.Status, instance.Output, instance.ErrorCode, instance.CompletedAt = outcome, workflowpolicy.WorkflowCloneMap(completion.Output), strings.TrimSpace(completion.ErrorCode), now.Format(time.RFC3339Nano)
	if outputVariable := strings.TrimSpace(node.Contract.AgentTask.OutputVariable); outputVariable != "" {
		process.Variables[outputVariable] = workflowpolicy.WorkflowCloneMap(completion.Output)
	}
	process.UpdatedAt = now.Format(time.RFC3339Nano)
	next := s.processEngine.nextNodeIDs(process.DefinitionSnapshot.Graph, instance.NodeID, outcome)
	resume := workflowmodel.WorkflowExecution{
		WorkspaceID: completion.WorkspaceID, ID: "agent_task_resume_" + completion.TaskRunID, WorkflowKey: process.WorkflowKey, Name: process.WorkflowName,
		Trigger: "agent_task_terminal", Status: "pending", ActionType: "workflow_graph", Action: workflowpolicy.WorkflowCloneMap(process.DefinitionSnapshot.Action),
		Payload: workflowpolicy.WorkflowCloneMap(process.Variables), Result: map[string]any{
			"resume_process_id": process.ID, "resume_node_ids": next, "resume_explicit": true,
			"agent_task_run_id": completion.TaskRunID, "agent_task_outcome": outcome,
			"execution_user_id": completion.Identity.Execution.UserID, "execution_role_key": completion.Identity.Execution.RoleKey,
		},
		ProcessID: process.ID, NodeID: instance.NodeID, ObjectKey: process.ObjectKey, RecordID: process.RecordID, ActorID: completion.Identity.Execution.UserID,
		IdempotencyKey: "agent-task-resume:" + completion.TaskRunID, MaxAttempts: workflowpolicy.WorkflowMaxAttempts(process.DefinitionSnapshot), Message: "workflow.message.agentTaskCompleted",
		CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339),
	}
	event := workflowmodel.WorkflowProcessEvent{
		WorkspaceID: completion.WorkspaceID, ID: workflowProcessID(ctx, "event"), ProcessID: process.ID, NodeID: instance.NodeID,
		Event: "agent_task_" + outcome, ActorID: valueOrDefault(completion.Identity.Execution.UserID, "system"), Summary: "workflow.event.agentTask." + outcome,
		Metadata: map[string]any{
			"task_run_id": completion.TaskRunID, "task_key": completion.TaskKey, "task_version": completion.TaskVersion,
			"agent_key": completion.Evidence.AgentKey, "identity_mode": completion.Identity.Mode,
			"execution_user_id": completion.Identity.Execution.UserID, "execution_role_key": completion.Identity.Execution.RoleKey,
			"outcome": outcome, "tool_invocation_refs": append([]string(nil), completion.Evidence.ToolInvocationRefs...),
			"audit_refs": append([]string(nil), completion.Evidence.AuditRefs...),
		},
		CreatedAt: now.Format(time.RFC3339Nano),
	}
	if err := stateStore.CommitWorkflowState(ctx, transactionmodel.WorkflowStateCommit{
		WorkspaceID: completion.WorkspaceID, Process: &process, UpdateNodes: []workflowmodel.WorkflowNodeInstance{*instance},
		Events: []workflowmodel.WorkflowProcessEvent{event}, InsertExecutions: []workflowmodel.WorkflowExecution{resume},
	}); err != nil {
		return err
	}
	s.wakeWorkflowExecution(resume)
	return nil
}

func workflowAgentCapabilityOutcome(status, outcome string) string {
	switch strings.TrimSpace(status) {
	case "failed", "cancelled", "dead_letter":
		return "error"
	}
	if value := strings.TrimSpace(outcome); value != "" {
		return value
	}
	switch strings.TrimSpace(status) {
	case "succeeded":
		return "success"
	case "manual_review":
		return "manual_review"
	case "rejected":
		return "rejected"
	case "no_result":
		return "no_result"
	default:
		return "error"
	}
}
