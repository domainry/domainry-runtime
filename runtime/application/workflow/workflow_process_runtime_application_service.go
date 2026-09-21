package workflow

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
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
	if e.runtime.dependencies.StartAgentTask == nil {
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
	nodeInstanceID, runID := workflowAgentTaskCorrelation(process.ID, node.ID, instance.Iteration)
	instance.ID = nodeInstanceID
	input := renderWorkflowRecordData(ctx, contract.Input, process.Variables, principal)
	instance.Input = workflowpolicy.WorkflowCloneMap(input)
	instance.Input["agent_task_run_id"] = runID
	result, err := e.runtime.dependencies.StartAgentTask(ctx, WorkflowAgentTaskPreparation{
		RunID: runID, WorkspaceID: process.WorkspaceID, ProcessID: process.ID, NodeInstanceID: instance.ID,
		NodeID: node.ID, Iteration: instance.Iteration, DefinitionVersionID: process.DefinitionVersionID, DefinitionSnapshotHash: process.DefinitionHash,
		Contract: contract, Input: input, Initiator: principal, CorrelationID: principal.CorrelationID,
	})
	if err != nil {
		return "", false, err
	}
	if result.Status != agentsdk.ProviderRunAccepted && result.Status != agentsdk.ProviderRunRunning {
		return "", false, internalError("start Agent Task", fmt.Errorf("Agent capability returned status %q", result.Status))
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	event := workflowmodel.WorkflowProcessEvent{WorkspaceID: process.WorkspaceID, ID: workflowProcessID(ctx, "event"), ProcessID: process.ID, NodeID: node.ID, Event: "agent_task_waiting", ActorID: valueOrDefault(principal.UserID, "system"), Summary: node.Name, Metadata: map[string]any{"task_run_id": runID, "task_key": contract.TaskKey, "task_version": contract.TaskVersion, "agent_external_run_id": result.ExternalRunID}, CreatedAt: now}
	if err := stateStore.CommitWorkflowState(ctx, transactionmodel.WorkflowStateCommit{WorkspaceID: process.WorkspaceID, InsertNodes: []workflowmodel.WorkflowNodeInstance{instance}, Events: []workflowmodel.WorkflowProcessEvent{event}}); err != nil {
		return "", false, internalError("commit Agent Task", err)
	}
	if strings.TrimSpace(contract.OutputVariable) != "" {
		process.Variables["agent_task:"+node.ID+":output_variable"] = strings.TrimSpace(contract.OutputVariable)
	}
	return "waiting", true, nil
}

func workflowAgentTaskCorrelation(processID, nodeID string, iteration int) (string, string) {
	seed := fmt.Sprintf("%s:%s:%d", strings.TrimSpace(processID), strings.TrimSpace(nodeID), iteration)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(seed)))
	return "agent_node_" + hash[:24], "agent_task_" + hash[:24]
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
	actionPrincipal, err := NewWorkflowPrincipalResolver(e.runtime.dependencies.Principals, e.runtime.dependencies.WorkloadReleases).ResolveWorkflowPrincipalForExecution(ctx, process.DefinitionSnapshot, principal, workflowPrincipalExecutionForProcess(*process, process.ID+":"+node.ID))
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
	// Claim the process revision before completing the timer or running its
	// next node. Withdrawal uses the same row and cannot lose to a stale resume.
	revisionStore, ok := e.runtime.dependencies.Processes.(workflowcontract.WorkflowProcessRevisionStore)
	if !ok {
		return process, internalError("workflow timer revision store unavailable", nil)
	}
	expectedUpdatedAt := process.UpdatedAt
	process.Status, process.UpdatedAt = "running", time.Now().UTC().Format(time.RFC3339Nano)
	claimed, err := revisionStore.ClaimWorkflowTimer(ctx, workspaceID, process, *waiting, expectedUpdatedAt)
	if err != nil {
		return process, internalError("claim workflow timer process", err)
	}
	if !claimed {
		return process, conflict("backend.workflow.timer_node_not_waiting")
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

func (engine *WorkflowProcessEngine) ResolveAssignees(ctx context.Context, process workflowmodel.WorkflowProcessInstance, nodeID string, resolvers []definitionmodel.WorkflowAssigneeResolver, principal principalmodel.Principal) ([]ResolvedAssignee, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	return engine.resolveWorkflowAssignees(ctx, process, nodeID, resolvers, principal)
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
