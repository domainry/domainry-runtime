package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowAgentRetryWorker struct{ *workflowExecutionWorkerStub }

func (s *workflowAgentRetryWorker) UpdateExecutionWhere(ctx context.Context, workspace string, next workflowmodel.WorkflowExecution, expected map[string]any) (bool, error) {
	old, found := s.executions[next.ExecutionID]
	if !found || old.Status != expected["status"] || old.LeaseOwner != expected["lease_owner"] || old.FencingToken != expected["fencing_token"] {
		return false, nil
	}
	return true, s.UpdateExecution(ctx, workspace, next)
}

func workflowAgentRetryFixture(t *testing.T) (*WorkflowApplicationService, *workflowProcessStoreEdgeStub, *workflowAgentRetryWorker, *workflowPrincipalResolverTestStub, *[]WorkflowBusinessActionInvocation) {
	t.Helper()
	at := time.Now().UTC().Add(-time.Minute)
	proof := runtimeext.WorkflowAgentTaskEvidence{Available: true, TaskRunID: "owner-run", ProcessID: "process", NodeID: "decide", NodeInstanceID: "source-node", TaskKey: "decide-task", TaskVersion: "1", Outcome: "success", ExecutionUserID: "agent", ExecutionRoleKey: "agent_service", CompletedAt: at.UnixMilli()}
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace-1", ProcessID: "process", WorkflowKey: "flow", ObjectKey: "turn", RecordID: "turn-a", Status: "configuration_error", Variables: map[string]any{"record_id": "turn-a"}, DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow", Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
		{ID: "decide", Type: "agent_task", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: "decide-task", TaskVersion: "1"}}},
		{ID: "save", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "turn.save", ObjectKey: "turn", RecordID: "$workflow.record_id"}}},
	}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "decide-save", Source: "decide", Target: "save", Branch: "success"}}}}}
	nodes := []workflowmodel.WorkflowNodeInstance{{WorkspaceID: process.WorkspaceID, ProcessID: process.ProcessID, NodeID: "decide", NodeInstanceID: "source-node", NodeType: "agent_task", Iteration: 1, Status: "success", CompletedAt: at.Format(time.RFC3339Nano), Input: map[string]any{"agent_task_run_id": "owner-run", workflowAgentEvidenceKey: proof}}, {WorkspaceID: process.WorkspaceID, ProcessID: process.ProcessID, NodeID: "save", NodeInstanceID: "failed-node", NodeType: "action", Iteration: 1, Status: "failed", StartedAt: at.Add(time.Second).Format(time.RFC3339Nano)}}
	store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": nodes}}}
	execution := workflowmodel.WorkflowExecution{WorkspaceID: process.WorkspaceID, ProcessID: process.ProcessID, WorkflowKey: process.WorkflowKey, ObjectKey: process.ObjectKey, RecordID: process.RecordID, ExecutionID: "resume", Status: "dead_letter", Trigger: "agent_task_terminal", ActorID: "agent", Result: map[string]any{"resume_explicit": true, "resume_process_id": "process", "resume_node_ids": []string{"save"}, "agent_task_run_id": "owner-run", "execution_user_id": "agent", "execution_role_key": "agent_service"}}
	worker := &workflowAgentRetryWorker{&workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{"resume": execution}}}
	agent := workflowPrincipalWithPermissions(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: process.WorkspaceID, UserID: "agent", RoleKey: "agent_service"}}, "turn.save")
	resolver := &workflowPrincipalResolverTestStub{resolution: workflowPrincipalResolution(agent)}
	calls := []WorkflowBusinessActionInvocation{}
	service := NewWorkflowApplicationService(WorkflowDependencies{Processes: store, Workers: worker, Principals: resolver, Schema: workflowSchemaProviderEdgeStub{}, InvokeAction: func(_ context.Context, in WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		calls = append(calls, in)
		if in.Principal.UserID != "agent" || !in.Principal.HasPermission("turn.save") {
			return WorkflowBusinessActionInvocationResult{}, apperror.New(apperror.KindForbidden, "backend.action.permission_denied", nil, nil)
		}
		return WorkflowBusinessActionInvocationResult{Status: "succeeded"}, nil
	}})
	return service, store, worker, resolver, &calls
}

func TestWorkflowAgentRetryUsesLiveContinuationIdentityAndRetainsOperatorAudit(t *testing.T) {
	s, store, worker, resolver, calls := workflowAgentRetryFixture(t)
	out, err := s.RetryWorkflowProcessWithKey(t.Context(), "process", "operator-retry", workflowAdminPrincipal())
	if err != nil || out.Status != "completed" || len(*calls) != 1 || resolver.request.SubjectID != "agent" || resolver.request.RoleKey != "agent_service" {
		t.Fatal("operator replaced continuation identity", out.Status, err, *calls)
	}
	if worker.executions["resume"].Status != "completed" {
		t.Fatal("execution remains failed after process recovery")
	}
	auditFound := false
	for _, e := range store.events {
		if e.Event == "process_retry_started" {
			auditFound = true
			if e.ActorID != "admin" || e.Metadata["execution_user_id"] != "agent" {
				t.Fatal("operator or execution audit lost", e)
			}
		}
	}
	if !auditFound {
		t.Fatal("missing operator retry audit")
	}
	if _, err = s.RetryWorkflowProcessWithKey(t.Context(), "process", "operator-retry", workflowAdminPrincipal()); err != nil || len(*calls) != 1 {
		t.Fatal("retry replay repeated Action", err)
	}
}

func TestWorkflowAgentRetryRejectsRevokedOrForeignContinuationBeforeProcessMutation(t *testing.T) {
	for _, mode := range []string{"revoked", "directory error", "foreign process", "foreign workspace", "forged role", "wrong source run", "missing proof", "newer task iteration", "disconnected node", "approval barrier", "active execution", "held lease"} {
		t.Run(mode, func(t *testing.T) {
			s, store, worker, resolver, calls := workflowAgentRetryFixture(t)
			p := store.processes["process"]
			e := worker.executions["resume"]
			switch mode {
			case "active execution":
				e.Status = "running"
			case "held lease":
				e.LeaseOwner = "active-worker"
			case "revoked":
				resolver.resolution = identitysdk.PrincipalResolution{}
			case "directory error":
				resolver.err = errors.New("Identity unavailable")
			case "foreign process":
				e.ProcessID = "other"
			case "foreign workspace":
				e.WorkspaceID = "other"
			case "forged role":
				e.Result["execution_role_key"] = "admin"
			case "wrong source run":
				e.Result["agent_task_run_id"] = "other"
			case "missing proof":
				delete(store.nodes["process"][0].Input, workflowAgentEvidenceKey)
			case "newer task iteration":
				n := store.nodes["process"][0]
				n.Iteration = 2
				n.Status = "waiting"
				n.Input = map[string]any{"agent_task_run_id": "new-run"}
				store.nodes["process"] = append([]workflowmodel.WorkflowNodeInstance{n}, store.nodes["process"]...)
			case "disconnected node":
				e.Result["resume_node_ids"] = []string{"missing"}
			case "approval barrier":
				p.DefinitionSnapshot.Graph.Nodes = append(p.DefinitionSnapshot.Graph.Nodes, definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval"})
				e.Result["resume_node_ids"] = []string{"approval"}
				p.DefinitionSnapshot.Graph.Edges = append(p.DefinitionSnapshot.Graph.Edges, definitionmodel.WorkflowGraphEdge{ID: "approval-save", Source: "approval", Target: "save"})
			}
			worker.executions["resume"] = e
			store.processes["process"] = p
			if _, err := s.RetryWorkflowProcessWithKey(t.Context(), "process", "retry", workflowAdminPrincipal()); err == nil || len(*calls) != 0 || store.processes["process"].Status != "configuration_error" {
				t.Fatal("invalid continuation ran or mutated process", err, *calls)
			}
		})
	}
}

func TestWorkflowAgentRetryKeepsActionPermissionRevocationAndResumesErrorBranch(t *testing.T) {
	s, store, worker, resolver, calls := workflowAgentRetryFixture(t)
	// The identity is still active; removal of the business grant is enforced
	// by the Action boundary, never replaced with the operator's access bundle.
	resolved := workflowPrincipalWithPermissions(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "agent", RoleKey: "agent_service"}})
	resolver.resolution = workflowPrincipalResolution(resolved)
	if _, err := s.RetryWorkflowProcessWithKey(t.Context(), "process", "revoked-action", workflowAdminPrincipal()); err == nil || len(*calls) != 1 || (*calls)[0].Principal.UserID != "agent" || store.processes["process"].Status != "configuration_error" {
		t.Fatal("Action revocation bypassed", err)
	}
	if worker.executions["resume"].Status != "configuration_error" {
		t.Fatal("failed retry receipt lost")
	}
	s, store, _, _, calls = workflowAgentRetryFixture(t)
	p := store.processes["process"]
	p.DefinitionSnapshot.Graph.Nodes = append(p.DefinitionSnapshot.Graph.Nodes, definitionmodel.WorkflowGraphNode{ID: "fallback", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "turn.save", ObjectKey: "turn", RecordID: "$workflow.record_id"}}})
	p.DefinitionSnapshot.Graph.Edges = append(p.DefinitionSnapshot.Graph.Edges, definitionmodel.WorkflowGraphEdge{ID: "save-error", Source: "save", Target: "fallback", Branch: "error"})
	store.processes["process"] = p
	f := store.nodes["process"][1]
	f.NodeID = "fallback"
	store.nodes["process"] = append(store.nodes["process"], f)
	if out, err := s.RetryWorkflowProcessWithKey(t.Context(), "process", "error-branch", workflowAdminPrincipal()); err != nil || out.Status != "completed" || len(*calls) != 1 || (*calls)[0].NodeID != "fallback" {
		t.Fatal("error branch could not resume", err, *calls)
	}
}
