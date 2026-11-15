package workflow

import (
	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"testing"
)

func TestFailedAgentTaskReachesErrorEdgeWithoutDeclaringErrorAsBusinessOutcome(t *testing.T) {
	for _, status := range []string{"failed", "dead_letter", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			service, repo, state, completion, _, _ := workflowEvidenceFixture(t)
			process := repo.processes[completion.ProcessID]
			process.DefinitionSnapshot.Graph.Nodes[0].Contract.AgentTask.AllowedOutcomes = []string{"success"}
			process.DefinitionSnapshot.Graph.Edges = []definitionmodel.WorkflowGraphEdge{{ID: "agent_error", Source: "decide", Target: "save", Branch: "error"}}
			repo.processes[completion.ProcessID] = process
			completion.Status = status
			completion.Outcome = "error"
			completion.ErrorCode = "agent.task.local_tool_failed"
			completion.Output = nil
			if err := service.CompleteAgentTask(t.Context(), completion); err != nil {
				t.Fatal(err)
			}
			if len(state.commits) != 1 || len(state.commit.UpdateNodes) != 1 || state.commit.UpdateNodes[0].Status != "error" || state.commit.UpdateNodes[0].ErrorCode != completion.ErrorCode || len(state.commit.InsertExecutions) != 1 {
				t.Fatalf("failure callback did not commit error continuation: %#v", state.commit)
			}
			result := state.commit.InsertExecutions[0].Result
			next, ok := result["resume_node_ids"].([]string)
			if !ok || len(next) != 1 || next[0] != "save" {
				t.Fatal("error edge not selected", result)
			}
			repo.nodes[completion.ProcessID] = state.commit.UpdateNodes
			repo.processes[completion.ProcessID] = *state.commit.Process
			if err := service.CompleteAgentTask(t.Context(), completion); err != nil || len(state.commits) != 1 {
				t.Fatal("failed completion callback replay duplicated continuation", err)
			}
		})
	}
}

func TestSuccessfulAgentTaskStillCannotInventAnUndeclaredOutcome(t *testing.T) {
	service, repo, state, completion, _, _ := workflowEvidenceFixture(t)
	process := repo.processes[completion.ProcessID]
	process.DefinitionSnapshot.Graph.Nodes[0].Contract.AgentTask.AllowedOutcomes = []string{"success"}
	repo.processes[completion.ProcessID] = process
	completion.Status = "succeeded"
	completion.Outcome = "error"
	if err := service.CompleteAgentTask(t.Context(), completion); apperror.CodeOf(err) != "backend.workflow.agent_task_outcome_invalid" || len(state.commits) != 0 {
		t.Fatal("undeclared business result bypassed policy", err)
	}
}
