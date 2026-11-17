package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type preparationFailureStore struct {
	*workflowExecutionProcessStub
	err error
}

func (s *preparationFailureStore) UpdateNode(_ context.Context, _ string, node workflowmodel.WorkflowNodeInstance) error {
	if s.err != nil {
		return s.err
	}
	s.nodes[node.ProcessID] = append(s.nodes[node.ProcessID], node)
	return nil
}

func TestPreparedTaskFailureUsesOnlyDeclaredErrorBranchAfterDurableFailure(t *testing.T) {
	for _, policy := range []string{"error_branch", "fail"} {
		for _, variant := range []string{"prepare_error", "invalid_start", "failure_storage"} {
			t.Run(policy+"/"+variant, func(t *testing.T) {
				store := &preparationFailureStore{workflowExecutionProcessStub: &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
				if variant == "failure_storage" {
					store.err = errors.New("storage down")
				}
				calls := 0
				state := &workflowAgentTaskStateStub{}
				engine := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, Decisions: state, StartAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
					calls++
					if variant == "invalid_start" {
						return agentsdk.TaskResult{Status: agentsdk.ProviderRunCompleted}, nil
					}
					return agentsdk.TaskResult{}, badRequest("agent.task.model_not_ready")
				}}).ProcessEngine()
				p := workflowProcessQueryPrincipal()
				process := workflowmodel.WorkflowProcessInstance{ProcessID: "process", WorkspaceID: p.WorkspaceID, Variables: map[string]any{}}
				node := definitionmodel.WorkflowGraphNode{ID: "analyze", Type: "agent_task", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: "task", TaskVersion: "1", OnError: policy}}}
				outcome, waiting, err := engine.executeAgentTaskNode(t.Context(), &process, node, p)
				if calls != 1 || waiting || len(state.commits) != 1 {
					t.Fatal("preparation restarted or created waiting receipt", calls, waiting, state.commits, err)
				}
				if policy == "error_branch" && variant != "failure_storage" {
					if err != nil || outcome != "error" || len(store.nodes["process"]) != 1 {
						t.Fatal(outcome, err, store.nodes)
					}
				} else if err == nil || outcome != "" {
					t.Fatal("missing durable failure was routed", outcome, err)
				}
				if variant != "failure_storage" {
					n := store.nodes["process"][0]
					if n.Status != "configuration_error" || n.ErrorCode == "" || n.CompletedAt == "" || n.Input[workflowAgentEvidenceKey] != nil {
						t.Fatal("failure masquerades as native completion", n)
					}
				}
			})
		}
	}
}

func TestPreparationFailureProjectionIsNotANativeTaskReceipt(t *testing.T) {
	s, r, _, _, scope, request := workflowEvidenceFixture(t)
	p := r.processes[scope.ProcessID]
	p.DefinitionSnapshot.Graph.Nodes[0].Contract.AgentTask.OnError = "error_branch"
	p.DefinitionSnapshot.Graph.Edges = []definitionmodel.WorkflowGraphEdge{{Source: request.NodeID, Target: scope.CurrentNodeID, Branch: "error"}}
	r.processes[scope.ProcessID] = p
	n := &r.nodes[scope.ProcessID][0]
	n.Status = "configuration_error"
	n.ErrorCode = "agent.task.model_not_ready"
	n.CompletedAt = "2026-11-15T00:00:00.123Z"
	proof, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope)
	if err != nil || proof.Available || proof.TaskRunID != "" || len(proof.Tools) != 0 || proof.PreparationFailure == nil {
		t.Fatal(proof, err)
	}
	f := proof.PreparationFailure
	if f.WorkflowKey != request.WorkflowKey || f.TaskKey != request.TaskKey || f.TaskVersion != request.TaskVersion || f.ProcessID != scope.ProcessID || f.NodeID != request.NodeID || f.NodeInstanceID != n.NodeInstanceID || f.ErrorCode != n.ErrorCode || f.StoppedAt < 1_000_000_000_000 {
		t.Fatal(f)
	}
	raw, _ := json.Marshal(proof)
	if strings.Contains(string(raw), "owner-run") || strings.Contains(string(raw), "task_run_id") || strings.Contains(string(raw), "tools") {
		t.Fatal("planned Task identity released as execution", string(raw))
	}
	// A successful continuation, malformed durable error and absent stop time
	// cannot fabricate preparation-failure authority.
	for _, variant := range []string{"success_edge", "no_error_policy", "raw_error", "missing_time", "active_node"} {
		t.Run(variant, func(t *testing.T) {
			original := r.processes[scope.ProcessID]
			changed := original
			graphRaw, _ := json.Marshal(original.DefinitionSnapshot.Graph)
			var graph definitionmodel.WorkflowGraphSchema
			_ = json.Unmarshal(graphRaw, &graph)
			changed.DefinitionSnapshot.Graph = &graph
			originalNode := r.nodes[scope.ProcessID][0]
			changedNode := originalNode
			switch variant {
			case "success_edge":
				graph.Edges[0].Branch = "success"
			case "no_error_policy":
				graph.Nodes[0].Contract.AgentTask.OnError = "fail"
			case "raw_error":
				changedNode.ErrorCode = "PRIVATE model output"
			case "missing_time":
				changedNode.CompletedAt = ""
			case "active_node":
				changedNode.Status = "waiting"
			}
			r.processes[scope.ProcessID] = changed
			r.nodes[scope.ProcessID][0] = changedNode
			got, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope)
			if err == nil || got.PreparationFailure != nil || got.Available {
				t.Fatal("invalid authority accepted", variant, got, apperror.CodeOf(err))
			}
			r.processes[scope.ProcessID] = original
			r.nodes[scope.ProcessID][0] = originalNode
		})
	}
}
