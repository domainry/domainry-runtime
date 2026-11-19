package workflow

import (
	"encoding/json"
	"fmt"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"strings"
	"testing"
	"time"
)

func workflowEvidenceFixture(t *testing.T) (*WorkflowApplicationService, *workflowExecutionProcessStub, *workflowAgentTaskStateStub, WorkflowAgentTaskCompletion, WorkflowActionAgentEvidenceScope, runtimeext.WorkflowAgentTaskEvidenceRequest) {
	t.Helper()
	process := workflowmodel.WorkflowProcessInstance{ProcessID: "process", WorkspaceID: "workspace", WorkflowKey: "case.decide", ObjectKey: "turn", RecordID: "turn-a", Status: "waiting", Variables: map[string]any{}, DefinitionSnapshot: definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "decide", Type: "agent_task", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: "support.decide", TaskVersion: "1", AllowedOutcomes: []string{"success", "error"}}}}, {ID: "save", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "turn.save"}}}}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "decide", Target: "save", Branch: "success"}, {Source: "decide", Target: "save", Branch: "error"}}}}}
	node := workflowmodel.WorkflowNodeInstance{NodeInstanceID: "source-node", NodeID: "decide", NodeType: "agent_task", ProcessID: "process", WorkspaceID: "workspace", Iteration: 1, Status: "waiting", Input: map[string]any{"agent_task_run_id": "owner-run"}}
	repo := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {node}}}
	state := &workflowAgentTaskStateStub{}
	service := NewWorkflowApplicationService(WorkflowDependencies{Processes: repo, Decisions: state})
	at := time.UnixMilli(1794700000123).UTC()
	complete := WorkflowAgentTaskCompletion{WorkspaceID: "workspace", ProcessID: "process", NodeInstanceID: "source-node", TaskRunID: "owner-run", TaskKey: "support.decide", TaskVersion: "1", Status: "succeeded", Outcome: "success", Output: map[string]any{"provider_secret": "never-release-provider-payload"}, Identity: agentsdk.ExecutionIdentity{Execution: agentsdk.PrincipalReference{UserID: "agent", RoleKey: "agent_service"}}, Evidence: agentmodel.AgentTaskExecutionEvidence{AgentKey: "support.agent", Model: "deepseek", Usage: map[string]any{"private": "never-release-usage"}, ToolInvocations: []agentmodel.AgentTaskToolInvocationEvidence{{Ref: "tool-a", Tool: agentsdk.AgentToolGetRecord, Status: "executed", InputHash: "input-sha", OutputHash: "output-sha", StartedAt: at, FinishedAt: &at}}}}
	scope := WorkflowActionAgentEvidenceScope{WorkspaceID: "workspace", ProcessID: "process", CurrentNodeID: "save", CurrentActionKey: "turn.save", ObjectKey: "turn", RecordID: "turn-a"}
	request := runtimeext.WorkflowAgentTaskEvidenceRequest{WorkflowKey: "case.decide", NodeID: "decide", TaskKey: "support.decide", TaskVersion: "1"}
	return service, repo, state, complete, scope, request
}
func persistWorkflowEvidenceFixture(t *testing.T, s *WorkflowApplicationService, r *workflowExecutionProcessStub, st *workflowAgentTaskStateStub, c WorkflowAgentTaskCompletion) {
	t.Helper()
	if err := s.CompleteAgentTask(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	// Round-trip the owner-produced node through durable JSON before reading it.
	b, err := json.Marshal(st.commit.UpdateNodes[0])
	if err != nil {
		t.Fatal(err)
	}
	var n workflowmodel.WorkflowNodeInstance
	if err = json.Unmarshal(b, &n); err != nil {
		t.Fatal(err)
	}
	r.nodes[c.ProcessID] = []workflowmodel.WorkflowNodeInstance{n}
}
func TestWorkflowCompletionEvidenceRoundTripsNumericTimeWithoutProviderPayload(t *testing.T) {
	s, r, st, c, scope, request := workflowEvidenceFixture(t)
	persistWorkflowEvidenceFixture(t, s, r, st, c)
	proof, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope)
	if err != nil || !proof.Available || proof.TaskRunID != "owner-run" || len(proof.Tools) != 1 || proof.Tools[0].Ref != "tool-a" || proof.Tools[0].StartedAt != 1794700000123 || proof.CompletedAt <= 0 {
		t.Fatal(proof, err)
	}
	b, _ := json.Marshal(proof)
	if strings.Contains(string(b), "never-release") || strings.Contains(string(b), "provider_secret") || !strings.Contains(string(b), `"started_at":1794700000123`) {
		t.Fatal("unsafe evidence projection", string(b))
	}
	// Replayed callbacks cannot rewrite execution history with altered metadata.
	c.Evidence.Model = "forged"
	if err = s.CompleteAgentTask(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	again, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope)
	if err != nil || again.Model != "deepseek" {
		t.Fatal("replay rewrote evidence", again, err)
	}
}
func TestWorkflowEvidenceCannotReadOtherScopeOrOlderTaskIteration(t *testing.T) {
	for _, change := range []func(*WorkflowActionAgentEvidenceScope, *runtimeext.WorkflowAgentTaskEvidenceRequest){func(s *WorkflowActionAgentEvidenceScope, _ *runtimeext.WorkflowAgentTaskEvidenceRequest) {
		s.WorkspaceID = "foreign"
	}, func(s *WorkflowActionAgentEvidenceScope, _ *runtimeext.WorkflowAgentTaskEvidenceRequest) {
		s.RecordID = "other"
	}, func(s *WorkflowActionAgentEvidenceScope, _ *runtimeext.WorkflowAgentTaskEvidenceRequest) {
		s.ObjectKey = "other"
	}, func(s *WorkflowActionAgentEvidenceScope, _ *runtimeext.WorkflowAgentTaskEvidenceRequest) {
		s.CurrentActionKey = "other"
	}, func(_ *WorkflowActionAgentEvidenceScope, r *runtimeext.WorkflowAgentTaskEvidenceRequest) {
		r.WorkflowKey = "other"
	}, func(_ *WorkflowActionAgentEvidenceScope, r *runtimeext.WorkflowAgentTaskEvidenceRequest) {
		r.TaskVersion = "other"
	}, func(_ *WorkflowActionAgentEvidenceScope, r *runtimeext.WorkflowAgentTaskEvidenceRequest) {
		r.NodeID = "save"
	}} {
		s, r, st, c, scope, request := workflowEvidenceFixture(t)
		persistWorkflowEvidenceFixture(t, s, r, st, c)
		change(&scope, &request)
		if proof, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope); err == nil || proof.Available {
			t.Fatal("foreign execution evidence released", proof, err)
		}
	}
	s, r, st, c, scope, request := workflowEvidenceFixture(t)
	persistWorkflowEvidenceFixture(t, s, r, st, c)
	latest := r.nodes["process"][0]
	latest.Iteration = 2
	latest.Status = "waiting"
	latest.Input = map[string]any{"agent_task_run_id": "new-run"}
	latest.CompletedAt = ""
	r.nodes["process"] = append(r.nodes["process"], latest)
	if _, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope); apperror.CodeOf(err) != runtimeext.WorkflowAgentTaskEvidenceUnavailableErrorCode {
		t.Fatal("old iteration used as current proof", err)
	}
}
func TestWorkflowEvidenceMissingCompletionStaysUnavailable(t *testing.T) {
	s, r, _, _, scope, request := workflowEvidenceFixture(t)
	if proof, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope); err == nil || proof.Available {
		t.Fatal("waiting Task fabricated receipt", proof, err)
	}
	r.nodes["process"][0].Status = "configuration_error"
	if proof, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope); err == nil || proof.Available || proof.TaskRunID != "" || proof.PreparationFailure != nil {
		t.Fatal("incomplete preparation stop fabricated evidence", proof, err)
	}
}

func TestFailedWorkflowCompletionCommitDoesNotPublishEvidence(t *testing.T) {
	s, r, st, c, scope, request := workflowEvidenceFixture(t)
	st.err = fmt.Errorf("commit unavailable")
	if err := s.CompleteAgentTask(t.Context(), c); err == nil {
		t.Fatal("commit failure hidden")
	}
	if _, ok := r.nodes["process"][0].Input[workflowAgentEvidenceKey]; ok {
		t.Fatal("uncommitted metadata mutated repository state")
	}
	if proof, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope); err == nil || proof.Available {
		t.Fatal("uncommitted execution released", proof, err)
	}
}

func TestWorkflowEvidenceRetainsOnlyTrustedKnowledgeReferenceReceipts(t *testing.T) {
	at := time.Now().UTC()
	receipt := &agentmodel.AgentTaskKnowledgeEvidence{LibraryID: "library-a", Operation: "search", ScopeSHA256: strings.Repeat("a", 64), Citations: []agentmodel.AgentTaskKnowledgeCitationEvidence{{ID: "citation-a", Provider: "knowledge", DocumentID: "doc-a", DocumentVersion: 4, ChunkID: "chunk-a"}}}
	completion := WorkflowAgentTaskCompletion{Evidence: agentmodel.AgentTaskExecutionEvidence{ToolInvocations: []agentmodel.AgentTaskToolInvocationEvidence{{Ref: "tool-a", Tool: agentsdk.AgentToolKnowledgeSearch, Status: "executed", InputHash: "input-hash", OutputHash: "output-hash", Knowledge: receipt}}}}
	proof := projectWorkflowAgentTaskEvidence(completion, "analyze", "knowledge", "1", at)
	if len(proof.Tools) != 1 || proof.Tools[0].Knowledge == nil || proof.Tools[0].Knowledge.Citations[0].DocumentVersion != 4 {
		t.Fatal("trusted receipt missing", proof)
	}
	receipt.Citations[0].DocumentID = "changed"
	if proof.Tools[0].Knowledge.Citations[0].DocumentID != "doc-a" {
		t.Fatal("completion aliases projected receipt")
	}
	for _, test := range []struct{ tool, status string }{{agentsdk.AgentToolGetRecord, "executed"}, {agentsdk.AgentToolKnowledgeSearch, "failed"}, {agentsdk.AgentToolKnowledgeRead, "executed"}} {
		completion.Evidence.ToolInvocations[0].Tool = test.tool
		completion.Evidence.ToolInvocations[0].Status = test.status
		projected := projectWorkflowAgentTaskEvidence(completion, "analyze", "knowledge", "1", at)
		if projected.Tools[0].Knowledge != nil {
			t.Fatal("invalid receipt promoted", projected)
		}
	}
}

func TestKnowledgeReceiptSurvivesWorkflowCommitAndCannotBeReplacedByCallbackReplay(t *testing.T) {
	s, repo, state, completion, scope, request := workflowEvidenceFixture(t)
	tool := &completion.Evidence.ToolInvocations[0]
	tool.Tool = agentsdk.AgentToolKnowledgeRead
	tool.Knowledge = &agentmodel.AgentTaskKnowledgeEvidence{LibraryID: "library-a", Operation: "fetch", ScopeSHA256: strings.Repeat("a", 64), Citations: []agentmodel.AgentTaskKnowledgeCitationEvidence{{ID: "citation-a", Provider: "knowledge", DocumentID: "doc-a", DocumentVersion: 7}}}
	persistWorkflowEvidenceFixture(t, s, repo, state, completion)
	proof, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope)
	if err != nil || proof.Tools[0].Knowledge == nil || proof.Tools[0].Knowledge.Citations[0].DocumentVersion != 7 {
		t.Fatal("durable receipt missing", proof, err)
	}
	tool.Knowledge.Citations[0].DocumentID = "other"
	if err = s.CompleteAgentTask(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	replay, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope)
	if err != nil || replay.Tools[0].Knowledge.Citations[0].DocumentID != "doc-a" {
		t.Fatal("callback replaced citation receipt", replay, err)
	}
}

func TestWorkflowEvidenceRequiresDeclaredOutcomeContinuationInsteadOfSharedIdentity(t *testing.T) {
	s, repo, state, completion, scope, request := workflowEvidenceFixture(t)
	persistWorkflowEvidenceFixture(t, s, repo, state, completion)

	proof, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope)
	if err != nil || proof.ExecutionUserID != "agent" || proof.ExecutionRoleKey != "agent_service" {
		t.Fatal("successor could not read separately-owned Agent receipt", proof, err)
	}
	process := repo.processes[scope.ProcessID]
	process.DefinitionSnapshot.Graph.Nodes = append(process.DefinitionSnapshot.Graph.Nodes, definitionmodel.WorkflowGraphNode{ID: "dispatch", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "turn.dispatch"}}})
	process.DefinitionSnapshot.Graph.Edges = append(process.DefinitionSnapshot.Graph.Edges, definitionmodel.WorkflowGraphEdge{Source: "save", Target: "dispatch", Branch: "success"})
	repo.processes[scope.ProcessID] = process
	scope.CurrentNodeID = "dispatch"
	scope.CurrentActionKey = "turn.dispatch"
	proof, err = s.ReadActionAgentTaskEvidence(t.Context(), request, scope)
	if err != nil || !proof.Available {
		t.Fatal("downstream continuation could not read Agent receipt", proof, err)
	}

	process = repo.processes[scope.ProcessID]
	process.DefinitionSnapshot.Graph.Nodes = append(process.DefinitionSnapshot.Graph.Nodes, definitionmodel.WorkflowGraphNode{ID: "other", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "turn.other"}}})
	repo.processes[scope.ProcessID] = process
	scope.CurrentNodeID = "other"
	scope.CurrentActionKey = "turn.other"
	if got, readErr := s.ReadActionAgentTaskEvidence(t.Context(), request, scope); readErr == nil || got.Available {
		t.Fatal("unrelated Action read Agent receipt", got, readErr)
	}

	scope.CurrentNodeID = "save"
	scope.CurrentActionKey = "turn.save"
	process = repo.processes[scope.ProcessID]
	process.DefinitionSnapshot.Graph.Edges[0].Branch = "manual_review"
	repo.processes[scope.ProcessID] = process
	if got, readErr := s.ReadActionAgentTaskEvidence(t.Context(), request, scope); readErr == nil || got.Available {
		t.Fatal("mismatched outcome branch read Agent receipt", got, readErr)
	}
}

func TestWorkflowEvidenceRejectsMissingExecutionIdentity(t *testing.T) {
	s, repo, state, completion, scope, request := workflowEvidenceFixture(t)
	persistWorkflowEvidenceFixture(t, s, repo, state, completion)
	raw, ok := repo.nodes[scope.ProcessID][0].Input[workflowAgentEvidenceKey].(map[string]any)
	if !ok {
		t.Fatal("durable evidence is not an object")
	}
	raw["execution_user_id"] = ""
	if got, err := s.ReadActionAgentTaskEvidence(t.Context(), request, scope); err == nil || got.Available {
		t.Fatal("receipt without execution identity accepted", got, err)
	}
}
