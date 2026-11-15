package workflow

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

// Recovery is an operator command, not a change to an Agent continuation's
// execution identity. Only an exact, durable continuation may select that
// identity; process variables and retry request data are never authority.
func (s *WorkflowApplicationService) workflowAgentRetryPrincipal(ctx context.Context, p workflowmodel.WorkflowProcessInstance, nodes []workflowmodel.WorkflowNodeInstance, failedID string, operator principalmodel.Principal) (principalmodel.Principal, *workflowmodel.WorkflowExecution, error) {
	denied := func() (principalmodel.Principal, *workflowmodel.WorkflowExecution, error) {
		return principalmodel.Principal{}, nil, apperror.New(apperror.KindForbidden, "backend.workflow.agent_retry_binding_invalid", nil, nil)
	}
	graph := p.DefinitionSnapshot.Graph
	if graph == nil || !slices.ContainsFunc(graph.Nodes, func(n definitionmodel.WorkflowGraphNode) bool { return n.Type == "agent_task" }) {
		return operator, nil, nil
	}
	if s.workerRepo == nil || len(nodes) > 2000 || len(graph.Nodes) > 2000 || len(graph.Edges) > 4000 {
		return denied()
	}
	e, found, err := s.workerRepo.GetExecutionByProcessID(ctx, p.WorkspaceID, p.ProcessID)
	if err != nil {
		return principalmodel.Principal{}, nil, err
	}
	if !found {
		if slices.ContainsFunc(nodes, func(n workflowmodel.WorkflowNodeInstance) bool {
			return n.NodeType == "agent_task" && n.Input[workflowAgentEvidenceKey] != nil
		}) {
			return denied()
		}
		return operator, nil, nil
	}
	if e.Trigger != "agent_task_terminal" {
		return operator, nil, nil
	}
	if !slices.Contains([]string{"failed", "dead_letter", "configuration_error"}, e.Status) || e.LeaseOwner != "" {
		return denied()
	}
	if e.WorkspaceID != p.WorkspaceID || e.ProcessID != p.ProcessID || e.WorkflowKey != p.WorkflowKey || e.ObjectKey != p.ObjectKey || e.RecordID != p.RecordID || e.Result["resume_process_id"] != p.ProcessID || e.Result["resume_explicit"] != true {
		return denied()
	}
	runID, _ := e.Result["agent_task_run_id"].(string)
	if runID == "" {
		return denied()
	}
	var source, failed *workflowmodel.WorkflowNodeInstance
	for i := range nodes {
		n := &nodes[i]
		if n.NodeID == failedID && (failed == nil || n.Iteration > failed.Iteration) {
			failed = n
		}
		if n.Input["agent_task_run_id"] == runID && n.NodeType == "agent_task" {
			if source != nil {
				return denied()
			}
			source = n
		}
	}
	if source == nil || failed == nil || source.WorkspaceID != p.WorkspaceID || source.ProcessID != p.ProcessID || failed.WorkspaceID != p.WorkspaceID || failed.ProcessID != p.ProcessID {
		return denied()
	}
	for _, n := range nodes {
		if n.NodeID == source.NodeID && n.Iteration > source.Iteration {
			return denied()
		}
	}
	contract, exists := workflowpolicy.WorkflowGraphNode(graph, source.NodeID)
	if !exists || contract.Type != "agent_task" || contract.Contract == nil || contract.Contract.AgentTask == nil {
		return denied()
	}
	raw, err := json.Marshal(source.Input[workflowAgentEvidenceKey])
	if err != nil || len(raw) > 256*1024 {
		return denied()
	}
	var proof runtimeext.WorkflowAgentTaskEvidence
	if json.Unmarshal(raw, &proof) != nil || !proof.Available || proof.TaskRunID != runID || proof.ProcessID != p.ProcessID || proof.NodeID != source.NodeID || proof.NodeInstanceID != source.NodeInstanceID || proof.TaskKey != contract.Contract.AgentTask.TaskKey || proof.TaskVersion != contract.Contract.AgentTask.TaskVersion || proof.ExecutionUserID == "" || proof.ExecutionRoleKey == "" || e.Result["execution_user_id"] != proof.ExecutionUserID || e.Result["execution_role_key"] != proof.ExecutionRoleKey || e.ActorID != proof.ExecutionUserID || source.Status != proof.Outcome {
		return denied()
	}
	completed, err := time.Parse(time.RFC3339Nano, source.CompletedAt)
	if err != nil || proof.CompletedAt != completed.UnixMilli() || proof.CompletedAt <= 0 {
		return denied()
	}
	started, err := time.Parse(time.RFC3339Nano, failed.StartedAt)
	if err != nil || started.Before(completed) {
		return denied()
	}
	resume := workflowNodeIDsFromAny(e.Result["resume_node_ids"])
	expected := s.processEngine.nextNodeIDs(graph, source.NodeID, source.Status)
	if len(resume) == 0 || !slices.Equal(uniqueSortedStrings(resume), uniqueSortedStrings(expected)) || !workflowAutomaticRetryReachable(p, resume, failedID) {
		return denied()
	}
	principal, err := s.agentTaskContinuationPrincipal(ctx, e, p, operator)
	return principal, &e, err
}

// Never carry an Agent's authority across a human approval, a new Agent task,
// or another suspension boundary. Such a continuation has its own authority.
func workflowAutomaticRetryReachable(p workflowmodel.WorkflowProcessInstance, starts []string, target string) bool {
	seen := map[string]bool{}
	queue := append([]string(nil), starts...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		n, found := workflowpolicy.WorkflowGraphNode(p.DefinitionSnapshot.Graph, id)
		if !found || !slices.Contains([]string{"action", "condition", "cc", "trigger"}, n.Type) {
			continue
		}
		if id == target {
			return true
		}
		for _, edge := range p.DefinitionSnapshot.Graph.Edges {
			if edge.Source == id {
				queue = append(queue, edge.Target)
			}
		}
	}
	return false
}

func (s *WorkflowApplicationService) syncRecoveredAgentContinuation(ctx context.Context, execution workflowmodel.WorkflowExecution, process workflowmodel.WorkflowProcessInstance, runErr error) error {
	expected := map[string]any{"status": execution.Status, "lease_owner": execution.LeaseOwner, "fencing_token": execution.FencingToken}
	execution.Status, execution.UpdatedAt, execution.OperationID = process.Status, process.UpdatedAt, process.OperationID
	execution.Result = workflowpolicy.WorkflowCloneMap(execution.Result)
	execution.Result["process_status"], execution.Result["current_node_ids"] = process.Status, process.CurrentNodeIDs
	if runErr != nil {
		execution.LastError = serviceErrorCode(runErr)
	} else {
		execution.LastError = ""
	}
	execution.NextRunAt = ""
	for _, k := range []string{"dead_lettered", "retry_scheduled", "retry_after", "retry_delay_seconds", "last_error", "last_error_code"} {
		delete(execution.Result, k)
	}
	updated, err := s.workerRepo.UpdateExecutionWhere(ctx, process.WorkspaceID, execution, expected)
	if err != nil {
		return err
	}
	if !updated {
		return conflict("backend.workflow.execution_lease_lost")
	}
	return nil
}
