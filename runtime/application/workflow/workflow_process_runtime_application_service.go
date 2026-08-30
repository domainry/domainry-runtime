package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/requestcontext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

var workflowProcessIDSequence atomic.Uint64

func workflowProcessID(ctx context.Context, prefix string) string {
	_ = ctx
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), workflowProcessIDSequence.Add(1))
}

func (e *WorkflowProcessEngine) executeAgentTaskNode(ctx context.Context, process *workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, principal principalmodel.Principal) (string, bool, error) {
	if e == nil {
		return "", false, internalError("prepare Agent Task", fmt.Errorf("Agent Task dispatcher is required"))
	}
	if e.runtime == nil {
		return "", false, internalError("prepare Agent Task", fmt.Errorf("Agent Task dispatcher is required"))
	}
	if e.runtime.dependencies.PrepareAgentTask == nil {
		return "", false, internalError("prepare Agent Task", fmt.Errorf("Agent Task dispatcher is required"))
	}
	stateStore, ok := e.runtime.dependencies.Decisions.(workflowcontract.WorkflowStateStore)
	if !ok {
		return "", false, internalError("commit Agent Task", fmt.Errorf("workflow state store is required"))
	}
	if node.Contract == nil {
		return "", false, badRequest("backend.workflow.agent_task_contract_required", "node", node.ID)
	}
	if node.Contract.AgentTask == nil {
		return "", false, badRequest("backend.workflow.agent_task_contract_required", "node", node.ID)
	}
	contract := *node.Contract.AgentTask
	instance := e.newNodeInstance(ctx, process.WorkspaceID, process.ID, node, "waiting", workflowpolicy.WorkflowCloneMap(process.Variables), nil)
	run, err := e.runtime.dependencies.PrepareAgentTask(ctx, WorkflowAgentTaskPreparation{
		RunID: workflowProcessID(ctx, "agent_task"), WorkspaceID: process.WorkspaceID, ProcessID: process.ID, NodeInstanceID: instance.ID,
		NodeID: node.ID, Iteration: instance.Iteration, DefinitionVersionID: process.DefinitionVersionID, DefinitionSnapshotHash: process.DefinitionHash,
		Contract: contract, Input: renderWorkflowRecordData(ctx, contract.Input, process.Variables, principal), Initiator: principal, CorrelationID: principal.CorrelationID,
	})
	if err != nil {
		return "", false, err
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return "", false, internalError("encode Agent Task", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	event := workflowmodel.WorkflowProcessEvent{WorkspaceID: process.WorkspaceID, ID: workflowProcessID(ctx, "event"), ProcessID: process.ID, NodeID: node.ID, Event: "agent_task_waiting", ActorID: valueOrDefault(principal.UserID, "system"), Summary: node.Name, Metadata: map[string]any{"task_run_id": run.ID, "task_key": run.TaskKey, "task_version": run.TaskVersion, "agent_key": run.Evidence.AgentKey, "identity_mode": run.Identity.Mode, "execution_user_id": run.Identity.Execution.UserID, "execution_role_key": run.Identity.Execution.RoleKey}, CreatedAt: now}
	commit := transactionmodel.WorkflowAgentTaskCommit{WorkspaceID: run.WorkspaceID, RunID: run.ID, IdempotencyKey: run.IdempotencyKey, TaskKey: run.TaskKey, ProcessID: run.ProcessID, Status: string(run.Status), LeaseOwner: run.Lease.Owner, FencingToken: run.Lease.FencingToken, Payload: payload, CreatedAtMillis: run.CreatedAt.UnixMilli(), UpdatedAtMillis: run.UpdatedAt.UnixMilli()}
	if err := stateStore.CommitWorkflowState(ctx, transactionmodel.WorkflowStateCommit{WorkspaceID: process.WorkspaceID, InsertNodes: []workflowmodel.WorkflowNodeInstance{instance}, InsertAgentTasks: []transactionmodel.WorkflowAgentTaskCommit{commit}, Events: []workflowmodel.WorkflowProcessEvent{event}}); err != nil {
		return "", false, internalError("commit Agent Task", err)
	}
	if e.runtime.dependencies.WakeAgentTask != nil {
		e.runtime.dependencies.WakeAgentTask(run.WorkspaceID, run.ID)
	}
	if strings.TrimSpace(contract.OutputVariable) != "" {
		process.Variables["agent_task:"+node.ID+":output_variable"] = strings.TrimSpace(contract.OutputVariable)
	}
	return "waiting", true, nil
}

func (s *WorkflowApplicationService) CommitAgentTaskTerminal(ctx context.Context, run agentmodel.AgentTaskRun, leaseOwner string, fencingToken int64) error {
	return s.commitAgentTaskTerminal(ctx, run, leaseOwner, fencingToken, "running")
}

func (s *WorkflowApplicationService) CommitAgentTaskApprovalTerminal(ctx context.Context, run agentmodel.AgentTaskRun) error {
	return s.commitAgentTaskTerminal(ctx, run, "", 0, string(agentmodel.AgentTaskRunWaitingApproval))
}

func (s *WorkflowApplicationService) commitAgentTaskTerminal(ctx context.Context, run agentmodel.AgentTaskRun, leaseOwner string, fencingToken int64, expectedStatus string) error {
	if s == nil {
		return internalError("commit Agent Task terminal", fmt.Errorf("workflow process runtime is required"))
	}
	if s.processEngine == nil {
		return internalError("commit Agent Task terminal", fmt.Errorf("workflow process runtime is required"))
	}
	if s.processRepo == nil {
		return internalError("commit Agent Task terminal", fmt.Errorf("workflow process runtime is required"))
	}
	stateStore, ok := s.processEngine.runtime.dependencies.Decisions.(workflowcontract.WorkflowStateStore)
	if !ok {
		return internalError("commit Agent Task terminal", fmt.Errorf("workflow state store is required"))
	}
	if !run.Status.Terminal() {
		return badRequest("backend.workflow.agent_task_terminal_invalid")
	}
	if strings.TrimSpace(run.ProcessID) == "" {
		return badRequest("backend.workflow.agent_task_terminal_invalid")
	}
	if strings.TrimSpace(run.NodeInstanceID) == "" {
		return badRequest("backend.workflow.agent_task_terminal_invalid")
	}
	process, found, err := s.processRepo.GetProcess(ctx, run.WorkspaceID, run.ProcessID)
	if err != nil || !found {
		return internalError("load Agent Task process", err)
	}
	nodes, err := s.processRepo.ListNodes(ctx, run.WorkspaceID, run.ProcessID)
	if err != nil {
		return internalError("load Agent Task node", err)
	}
	var instance *workflowmodel.WorkflowNodeInstance
	for index := range nodes {
		if nodes[index].ID != run.NodeInstanceID {
			continue
		}
		if nodes[index].Status == "waiting" {
			instance = &nodes[index]
			break
		}
	}
	if instance == nil {
		return conflict("backend.workflow.agent_task_node_not_waiting")
	}
	node, found := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, instance.NodeID)
	if !found {
		return badRequest("backend.workflow.agent_task_contract_required", "node", instance.NodeID)
	}
	if node.Contract == nil {
		return badRequest("backend.workflow.agent_task_contract_required", "node", instance.NodeID)
	}
	if node.Contract.AgentTask == nil {
		return badRequest("backend.workflow.agent_task_contract_required", "node", instance.NodeID)
	}
	outcome := agentTaskWorkflowOutcome(run)
	if !containsWorkflowString(node.Contract.AgentTask.AllowedOutcomes, outcome) {
		return badRequest("backend.workflow.agent_task_outcome_invalid", "outcome", outcome)
	}
	now := time.Now().UTC()
	instance.Status, instance.Output, instance.ErrorCode, instance.CompletedAt = outcome, workflowpolicy.WorkflowCloneMap(run.Output), run.LastErrorCode, now.Format(time.RFC3339Nano)
	if outputVariable := strings.TrimSpace(node.Contract.AgentTask.OutputVariable); outputVariable != "" {
		process.Variables[outputVariable] = workflowpolicy.WorkflowCloneMap(run.Output)
	}
	process.UpdatedAt = now.Format(time.RFC3339Nano)
	next := s.processEngine.nextNodeIDs(process.DefinitionSnapshot.Graph, instance.NodeID, outcome)
	resume := workflowmodel.WorkflowExecution{
		WorkspaceID: run.WorkspaceID, ID: "agent_task_resume_" + run.ID, WorkflowKey: process.WorkflowKey, Name: process.WorkflowName,
		Trigger: "agent_task_terminal", Status: "pending", ActionType: "workflow_graph", Action: workflowpolicy.WorkflowCloneMap(process.DefinitionSnapshot.Action),
		Payload: workflowpolicy.WorkflowCloneMap(process.Variables), Result: map[string]any{
			"resume_process_id": process.ID, "resume_node_ids": next, "resume_explicit": true,
			"agent_task_run_id": run.ID, "agent_task_outcome": outcome,
			"execution_user_id": run.Identity.Execution.UserID, "execution_role_key": run.Identity.Execution.RoleKey,
		},
		ProcessID: process.ID, NodeID: instance.NodeID, ObjectKey: process.ObjectKey, RecordID: process.RecordID, ActorID: run.Identity.Execution.UserID,
		IdempotencyKey: "agent-task-resume:" + run.ID, MaxAttempts: workflowpolicy.WorkflowMaxAttempts(process.DefinitionSnapshot), Message: "workflow.message.agentTaskCompleted",
		CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339),
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return internalError("encode Agent Task terminal", err)
	}
	taskCommit := transactionmodel.WorkflowAgentTaskCommit{WorkspaceID: run.WorkspaceID, RunID: run.ID, Status: string(run.Status), ExpectedStatus: expectedStatus, LeaseOwner: strings.TrimSpace(leaseOwner), FencingToken: fencingToken, Payload: payload, UpdatedAtMillis: run.UpdatedAt.UnixMilli()}
	event := workflowmodel.WorkflowProcessEvent{WorkspaceID: run.WorkspaceID, ID: workflowProcessID(ctx, "event"), ProcessID: process.ID, NodeID: instance.NodeID, Event: "agent_task_" + outcome, ActorID: valueOrDefault(run.Identity.Execution.UserID, "system"), Summary: "workflow.event.agentTask." + outcome, Metadata: map[string]any{"task_run_id": run.ID, "task_key": run.TaskKey, "task_version": run.TaskVersion, "agent_key": run.Evidence.AgentKey, "identity_mode": run.Identity.Mode, "execution_user_id": run.Identity.Execution.UserID, "execution_role_key": run.Identity.Execution.RoleKey, "attempt": run.Attempt, "outcome": outcome, "tool_invocation_refs": append([]string(nil), run.Evidence.ToolInvocationRefs...), "audit_refs": append([]string(nil), run.Evidence.AuditRefs...), "raw_evidence_ref": run.RawEvidenceRef}, CreatedAt: now.Format(time.RFC3339Nano)}
	if err := stateStore.CommitWorkflowState(ctx, transactionmodel.WorkflowStateCommit{WorkspaceID: run.WorkspaceID, Process: &process, UpdateNodes: []workflowmodel.WorkflowNodeInstance{*instance}, UpdateAgentTasks: []transactionmodel.WorkflowAgentTaskCommit{taskCommit}, Events: []workflowmodel.WorkflowProcessEvent{event}, InsertExecutions: []workflowmodel.WorkflowExecution{resume}}); err != nil {
		return err
	}
	s.wakeWorkflowExecution(resume)
	return nil
}

func agentTaskWorkflowOutcome(run agentmodel.AgentTaskRun) string {
	switch run.Status {
	case agentmodel.AgentTaskRunCancelled, agentmodel.AgentTaskRunDeadLetter, agentmodel.AgentTaskRunFailed:
		return "error"
	}
	if outcome := strings.TrimSpace(run.Outcome); outcome != "" {
		return outcome
	}
	switch run.Status {
	case agentmodel.AgentTaskRunSucceeded:
		return "success"
	case agentmodel.AgentTaskRunManualReview:
		return "manual_review"
	case agentmodel.AgentTaskRunRejected:
		return "rejected"
	case agentmodel.AgentTaskRunNoResult:
		return "no_result"
	default:
		return "error"
	}
}
func (e *WorkflowProcessEngine) executeActionNode(ctx context.Context, process *workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, principal principalmodel.Principal) (map[string]any, error) {
	contract := workflowpolicy.WorkflowBusinessActionNodeContract(node)
	actionKey := strings.TrimSpace(contract.ActionKey)
	if actionKey == "" || actionKey == "<nil>" {
		return nil, badRequest("backend.workflow.action_key_required", "node", node.ID)
	}
	objectKey := strings.TrimSpace(contract.ObjectKey)
	if objectKey == "" || objectKey == "<nil>" {
		objectKey = process.ObjectKey
	}
	recordID := workflowRenderedString(ctx, contract.RecordID, process.Variables, principal)
	if recordID == "" || recordID == "<nil>" {
		recordID = process.RecordID
	}
	actionPrincipal, err := NewWorkflowPrincipalResolver(e.runtime.dependencies.Principals).ResolveWorkflowPrincipal(ctx, process.DefinitionSnapshot, principal)
	if err != nil {
		return nil, err
	}
	input := renderWorkflowRecordData(ctx, contract.Input, process.Variables, actionPrincipal)
	maxAttempts := 1
	if contract.Retry != nil && contract.Retry.MaxAttempts > maxAttempts {
		maxAttempts = contract.Retry.MaxAttempts
	}
	// Keep transport retries for one node execution on the same idempotency
	// key, but advance the key after a durable failed node is recorded. This
	// allows an operator to retry a recovered approval after fixing the cause
	// of a terminal Action failure without replaying that failed receipt.
	nodeIteration := e.nextNodeIteration(ctx, process.WorkspaceID, process.ID, node.ID)
	actionIdempotencyKey := fmt.Sprintf("%s:%s:%d:%s", process.ID, node.ID, nodeIteration, valueOrDefault(process.DefinitionHash, fmt.Sprint(process.DefinitionVersion)))
	var invocation WorkflowBusinessActionInvocationResult
	for attempt := 1; ; attempt++ {
		execCtx := ctx
		if execCtx == nil {
			return nil, context.Canceled
		}
		var cancel context.CancelFunc
		if contract.TimeoutSeconds > 0 {
			execCtx, cancel = context.WithTimeout(execCtx, time.Duration(contract.TimeoutSeconds)*time.Second)
		}
		invocation, err = e.runtime.dependencies.InvokeAction(execCtx, WorkflowBusinessActionInvocation{
			ActionKey: actionKey, ObjectKey: objectKey, RecordID: recordID, Input: input, Principal: actionPrincipal,
			Source: WorkflowBusinessActionSourceWorkflow, ProcessID: process.ID, NodeID: node.ID,
			IdempotencyKey: actionIdempotencyKey,
		})
		if cancel != nil {
			cancel()
		}
		if err == nil || !invocation.Retryable || attempt == maxAttempts {
			break
		}
		e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, "", "business_action_retry_scheduled", actionPrincipal.UserID, node.Name, map[string]any{"attempt": attempt, "max_attempts": maxAttempts, "error_code": invocation.ErrorCode})
	}
	if err != nil {
		return nil, err
	}
	resolvedRecordID := recordID
	if invocation.Record != nil && strings.TrimSpace(invocation.Record.RecordID) != "" {
		resolvedRecordID = invocation.Record.RecordID
	}
	output := workflowCloneActionOutput(invocation.Output)
	if output == nil {
		output = map[string]any{}
	}
	if data, ok := output["data"].(map[string]any); ok {
		for key, value := range workflowpolicy.WorkflowCloneMap(data) {
			if _, reserved := output[key]; !reserved {
				output[key] = value
			}
		}
	}
	output["action_key"] = actionKey
	output["record_id"] = resolvedRecordID
	output["invocation_id"] = invocation.InvocationID
	output["status"] = invocation.Status
	output["outbox_ids"] = invocation.OutboxIDs
	if contract.OutputVariable != "" {
		process.Variables[contract.OutputVariable] = output
	}
	return output, nil
}

func (e *WorkflowProcessEngine) appendEvent(ctx context.Context, workspaceID, processID, nodeID, taskID, event, actorID, summary string, metadata map[string]any) {
	_ = e.runtime.dependencies.Processes.InsertEvent(ctx, workspaceID, workflowmodel.WorkflowProcessEvent{WorkspaceID: workspaceID, ID: workflowProcessID(ctx, "event"), ProcessID: processID, NodeID: nodeID, TaskID: taskID, Event: event, ActorID: valueOrDefault(actorID, "system"), Summary: summary, Metadata: workflowpolicy.WorkflowCloneMap(metadata), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
}

func (e *WorkflowProcessEngine) failProcess(ctx context.Context, process workflowmodel.WorkflowProcessInstance, code, actorID string) (workflowmodel.WorkflowProcessInstance, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	process.Status = "configuration_error"
	process.ErrorCode = valueOrDefault(strings.TrimSpace(code), "backend.workflow.process_failed")
	process.UpdatedAt = now
	process.CompletedAt = now
	_ = e.runtime.dependencies.Processes.UpdateProcess(ctx, process.WorkspaceID, process)
	e.appendEvent(ctx, process.WorkspaceID, process.ID, "", "", "process_failed", actorID, process.ErrorCode, nil)
	return process, badRequest(process.ErrorCode)
}

type WorkflowProcessRuntime struct {
	dependencies  WorkflowDependencies
	processEngine *WorkflowProcessEngine
}

func NewWorkflowProcessRuntime(dependencies WorkflowDependencies) *WorkflowProcessRuntime {
	runtime := &WorkflowProcessRuntime{dependencies: dependencies}
	runtime.processEngine = &WorkflowProcessEngine{runtime: runtime}
	return runtime
}

func (runtime *WorkflowProcessRuntime) ProcessEngine() *WorkflowProcessEngine {
	return runtime.processEngine
}

func (engine *WorkflowProcessEngine) DecisionRuntime() workflowcontract.WorkflowDecisionRuntime {
	return engine.runtime
}

func (e *WorkflowProcessEngine) ResumeTimerNode(ctx context.Context, workspaceID, processID, nodeID string, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	ctx = requestcontext.WithWorkspaceID(ctx, workspaceID)
	process, found, err := e.runtime.dependencies.Processes.GetProcess(ctx, workspaceID, strings.TrimSpace(processID))
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("get timer workflow process", err)
	}
	if !found {
		return workflowmodel.WorkflowProcessInstance{}, notFound("backend.workflow.process_not_found")
	}
	nodes, err := e.runtime.dependencies.Processes.ListNodes(ctx, workspaceID, process.ID)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("list timer workflow nodes", err)
	}
	var waiting *workflowmodel.WorkflowNodeInstance
	alreadyCompleted := false
	for index := range nodes {
		if nodes[index].NodeID == nodeID && nodes[index].Status == "waiting" {
			candidate := nodes[index]
			waiting = &candidate
		}
		if nodes[index].NodeID == nodeID && nodes[index].Status == "success" {
			alreadyCompleted = true
		}
	}
	if alreadyCompleted {
		return process, nil
	}
	if process.Status != "waiting" || !containsWorkflowString(process.CurrentNodeIDs, nodeID) {
		return process, conflict("backend.workflow.timer_node_not_waiting")
	}
	if waiting == nil {
		return process, conflict("backend.workflow.timer_node_not_waiting")
	}
	waiting.Status = "success"
	waiting.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if waiting.Output == nil {
		waiting.Output = map[string]any{}
	}
	waiting.Output["resumed"] = true
	if err := e.runtime.dependencies.Processes.UpdateNode(ctx, workspaceID, *waiting); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("complete timer workflow node", err)
	}
	e.appendEvent(ctx, workspaceID, process.ID, nodeID, "", "timer_fired", principal.UserID, "workflow.event.timer.fired", nil)
	waitingNodes := workflowpolicy.WorkflowRemoveString(process.CurrentNodeIDs, nodeID)
	next := e.nextNodeIDs(process.DefinitionSnapshot.Graph, nodeID, "success")
	return e.runWithContext(ctx, process, next, waitingNodes, principal)
}

func containsWorkflowString(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == strings.TrimSpace(expected) {
			return true
		}
	}
	return false
}

func workflowDecisionExecution(process workflowmodel.WorkflowProcessInstance, principal principalmodel.Principal, now string) workflowmodel.WorkflowExecution {
	return workflowmodel.WorkflowExecution{WorkspaceID: process.WorkspaceID, ID: process.ID, WorkflowKey: process.WorkflowKey, Name: process.WorkflowName, Trigger: "workflow_decision_continuation", Status: process.Status, ActionType: "workflow_graph", Action: workflowpolicy.WorkflowCloneMap(process.DefinitionSnapshot.Action), Payload: workflowpolicy.WorkflowCloneMap(process.Variables), Result: map[string]any{}, ProcessID: process.ID, ObjectKey: process.ObjectKey, RecordID: process.RecordID, ActorID: principal.UserID, RunAs: workflowpolicy.WorkflowRunAs(process.DefinitionSnapshot), MaxAttempts: workflowpolicy.WorkflowMaxAttempts(process.DefinitionSnapshot), Message: "workflow.message.decisionCommitted", CreatedAt: process.CreatedAt, UpdatedAt: now}
}

func workflowDecisionEvent(ctx context.Context, processID, nodeID, taskID, event, actorID, summary string, metadata map[string]any, createdAt string) workflowmodel.WorkflowProcessEvent {
	return workflowmodel.WorkflowProcessEvent{ID: workflowProcessID(ctx, "event"), ProcessID: processID, NodeID: nodeID, TaskID: taskID, Event: event, ActorID: valueOrDefault(actorID, "system"), Summary: summary, Metadata: workflowpolicy.WorkflowCloneMap(metadata), CreatedAt: createdAt}
}

func (engine *WorkflowProcessEngine) RunWithContext(ctx context.Context, process workflowmodel.WorkflowProcessInstance, queue, waiting []string, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	return engine.runWithContext(ctx, process, queue, waiting, principal)
}

func (engine *WorkflowProcessEngine) ResolveRecipients(ctx context.Context, process workflowmodel.WorkflowProcessInstance, resolvers []definitionmodel.WorkflowAssigneeResolver, principal principalmodel.Principal) ([]string, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	return engine.resolveWorkflowRecipients(ctx, process, resolvers, principal)
}

func WorkflowDefinitionHash(workflow definitionmodel.WorkflowSchema) string {
	return workflowpolicy.WorkflowDefinitionHash(workflow)
}

func WorkflowGraphNode(graph *definitionmodel.WorkflowGraphSchema, nodeID string) (definitionmodel.WorkflowGraphNode, bool) {
	return workflowpolicy.WorkflowGraphNode(graph, nodeID)
}

func WorkflowProcessID(ctx context.Context, prefix string) string {
	return workflowProcessID(ctx, prefix)
}

func (e *WorkflowProcessEngine) recordFailedNode(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, input map[string]any, nodeErr error, actorID string) {
	instance := e.newNodeInstance(ctx, process.WorkspaceID, process.ID, node, "failed", input, nil)
	instance.ErrorCode = serviceErrorCode(nodeErr)
	instance.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := e.runtime.dependencies.Processes.InsertNode(ctx, process.WorkspaceID, instance); err != nil {
		return
	}
	e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, "", "node_failed", actorID, instance.ErrorCode, map[string]any{"node_type": node.Type})
}
