package workflow

import (
	"context"
	"encoding/json"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

const workflowAgentEvidenceKey = "_runtime_agent_task_evidence"

// Constructed only from an already-authorized Workflow Action invocation.
type WorkflowActionAgentEvidenceScope struct {
	WorkspaceID, ProcessID, CurrentNodeID, CurrentActionKey, ObjectKey, RecordID, ExecutionUserID, ExecutionRoleKey string
}

func (s *WorkflowApplicationService) ReadActionAgentTaskEvidence(ctx context.Context, request runtimeext.WorkflowAgentTaskEvidenceRequest, scope WorkflowActionAgentEvidenceScope) (runtimeext.WorkflowAgentTaskEvidence, error) {
	denied := func() (runtimeext.WorkflowAgentTaskEvidence, error) {
		return runtimeext.WorkflowAgentTaskEvidence{}, apperror.New(apperror.KindForbidden, runtimeext.WorkflowAgentTaskEvidenceDeniedErrorCode, nil, nil)
	}
	unavailable := func() (runtimeext.WorkflowAgentTaskEvidence, error) {
		return runtimeext.WorkflowAgentTaskEvidence{}, apperror.New(apperror.KindUnavailable, runtimeext.WorkflowAgentTaskEvidenceUnavailableErrorCode, nil, nil)
	}
	for _, v := range []string{request.WorkflowKey, request.NodeID, request.TaskKey, request.TaskVersion, scope.WorkspaceID, scope.ProcessID, scope.CurrentNodeID, scope.CurrentActionKey, scope.ObjectKey, scope.RecordID, scope.ExecutionUserID, scope.ExecutionRoleKey} {
		if strings.TrimSpace(v) == "" || len(v) > 255 {
			return denied()
		}
	}
	if s == nil || s.processRepo == nil {
		return unavailable()
	}
	process, found, err := s.processRepo.GetProcess(ctx, scope.WorkspaceID, scope.ProcessID)
	if err != nil {
		return runtimeext.WorkflowAgentTaskEvidence{}, err
	}
	if !found || process.WorkspaceID != scope.WorkspaceID || process.WorkflowKey != request.WorkflowKey || process.ObjectKey != scope.ObjectKey || process.RecordID != scope.RecordID || process.Status == "cancelled" {
		return denied()
	}
	current, found := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, scope.CurrentNodeID)
	if !found || current.Type != "action" || current.Contract == nil || current.Contract.Action == nil || current.Contract.Action.ActionKey != scope.CurrentActionKey {
		return denied()
	}
	source, found := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, request.NodeID)
	if !found || source.Type != "agent_task" || source.Contract == nil || source.Contract.AgentTask == nil || source.Contract.AgentTask.TaskKey != request.TaskKey || source.Contract.AgentTask.TaskVersion != request.TaskVersion {
		return denied()
	}
	nodes, err := s.processRepo.ListNodes(ctx, scope.WorkspaceID, scope.ProcessID)
	if err != nil {
		return runtimeext.WorkflowAgentTaskEvidence{}, err
	}
	if len(nodes) > 2000 {
		return unavailable()
	}
	var selected *workflowmodel.WorkflowNodeInstance
	for i := range nodes {
		n := &nodes[i]
		if n.NodeID != request.NodeID {
			continue
		}
		if n.WorkspaceID != scope.WorkspaceID || n.ProcessID != scope.ProcessID {
			return denied()
		}
		if selected == nil || n.Iteration > selected.Iteration {
			selected = n
		}
	}
	if selected == nil {
		return unavailable()
	}
	raw, present := selected.Input[workflowAgentEvidenceKey]
	if !present {
		if selected.Status == "configuration_error" || selected.Status == "error" {
			return runtimeext.WorkflowAgentTaskEvidence{}, nil
		}
		return unavailable()
	}
	data, err := json.Marshal(raw)
	if err != nil || len(data) > 256*1024 {
		return unavailable()
	}
	var evidence runtimeext.WorkflowAgentTaskEvidence
	if err = json.Unmarshal(data, &evidence); err != nil {
		return unavailable()
	}
	runID, _ := selected.Input["agent_task_run_id"].(string)
	if !evidence.Available || evidence.TaskRunID == "" || evidence.TaskRunID != runID || evidence.ProcessID != scope.ProcessID || evidence.NodeID != request.NodeID || evidence.NodeInstanceID != selected.NodeInstanceID || evidence.TaskKey != request.TaskKey || evidence.TaskVersion != request.TaskVersion || evidence.ExecutionUserID != scope.ExecutionUserID || evidence.ExecutionRoleKey != scope.ExecutionRoleKey || evidence.CompletedAt <= 0 || len(evidence.Tools) > 128 || selected.CompletedAt == "" || selected.Status == "waiting" {
		return denied()
	}
	for _, tool := range evidence.Tools {
		if tool.Knowledge != nil && (tool.Knowledge.Validate() != nil || tool.Status != "executed" || tool.InputSHA256 == "" || tool.OutputSHA256 == "" || !((tool.Tool == agentsdk.AgentToolKnowledgeSearch && tool.Knowledge.Operation == "search") || (tool.Tool == agentsdk.AgentToolKnowledgeRead && tool.Knowledge.Operation == "fetch"))) {
			return denied()
		}
	}
	return evidence, nil
}

func projectWorkflowAgentTaskEvidence(completion WorkflowAgentTaskCompletion, nodeID, taskKey, version string, completed time.Time) runtimeext.WorkflowAgentTaskEvidence {
	out := runtimeext.WorkflowAgentTaskEvidence{Available: true, TaskRunID: completion.TaskRunID, ProcessID: completion.ProcessID, NodeID: nodeID, NodeInstanceID: completion.NodeInstanceID, TaskKey: taskKey, TaskVersion: version, AgentKey: completion.Evidence.AgentKey, Model: completion.Evidence.Model, Status: completion.Status, Outcome: workflowAgentCapabilityOutcome(completion.Status, completion.Outcome), ErrorCode: completion.ErrorCode, ExecutionUserID: completion.Identity.Execution.UserID, ExecutionRoleKey: completion.Identity.Execution.RoleKey, CompletedAt: completed.UTC().UnixMilli()}
	for _, tool := range completion.Evidence.ToolInvocations {
		t := runtimeext.WorkflowAgentToolEvidence{Ref: tool.Ref, Tool: tool.Tool, Status: tool.Status, InputSHA256: tool.InputHash, OutputSHA256: tool.OutputHash, ErrorCode: tool.ErrorCode, DurationMillis: tool.DurationMilliseconds}
		if tool.Knowledge != nil && tool.Status == "executed" && tool.Knowledge.Validate() == nil && ((tool.Tool == agentsdk.AgentToolKnowledgeSearch && tool.Knowledge.Operation == "search") || (tool.Tool == agentsdk.AgentToolKnowledgeRead && tool.Knowledge.Operation == "fetch")) {
			t.Knowledge = tool.Knowledge.Clone()
		}
		if !tool.StartedAt.IsZero() {
			t.StartedAt = tool.StartedAt.UTC().UnixMilli()
		}
		if tool.FinishedAt != nil && !tool.FinishedAt.IsZero() {
			t.FinishedAt = tool.FinishedAt.UTC().UnixMilli()
		}
		out.Tools = append(out.Tools, t)
	}
	return out
}
