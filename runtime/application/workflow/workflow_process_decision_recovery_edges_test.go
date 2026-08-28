package workflow

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func workflowTerminalFixture(mode string, next *definitionmodel.WorkflowGraphNode) (workflowmodel.WorkflowProcessInstance, workflowmodel.WorkflowTask) {
	approval := definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: mode}}}
	nodes := []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}, approval}
	edges := []definitionmodel.WorkflowGraphEdge{}
	if next != nil {
		nodes = append(nodes, *next)
		edges = append(edges, definitionmodel.WorkflowGraphEdge{Source: "approval", Target: next.ID, Branch: "approved"})
	}
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace-1", WorkflowKey: "flow", WorkflowName: "Flow", InitiatorID: "admin", Status: "waiting", CurrentNodeIDs: []string{"approval"}, Variables: map[string]any{}, Result: map[string]any{}, DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow", Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: nodes, Edges: edges}}}
	task := workflowmodel.WorkflowTask{ID: "task", WorkspaceID: "workspace-1", ProcessID: "process", NodeID: "approval", AssigneeUserID: "admin", Status: "open", Sequence: 1}
	return process, task
}

func workflowTerminalRuntime(store *workflowProcessStoreEdgeStub, decisions *workflowStateDecisionEdgeStub, workers *workflowExecutionWorkerStub, identity identitysdk.Directory) *WorkflowProcessRuntime {
	dependencies := WorkflowDependencies{Processes: store, Identity: identity, Schema: workflowSchemaProviderEdgeStub{}}
	if decisions != nil {
		dependencies.Decisions = decisions
	}
	if workers != nil {
		dependencies.Workers = workers
	}
	return NewWorkflowProcessRuntime(dependencies)
}

func TestRecoverWorkflowDecisionStoreAndCommitOutcomes(t *testing.T) {
	process, task := workflowTerminalFixture("any", nil)
	store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	decisionErr := errors.New("decision")
	engine := workflowTerminalRuntime(store, nil, nil, nil).processEngine
	if _, err := engine.recoverWorkflowDecision(t.Context(), process, nil, []workflowmodel.WorkflowTask{task}, task, decisionErr, "actor", nil); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("missing state store=%v", err)
	}
	decisions := &workflowStateDecisionEdgeStub{stateErr: errors.New("state")}
	engine = workflowTerminalRuntime(store, decisions, nil, nil).processEngine
	if _, err := engine.recoverWorkflowDecision(t.Context(), process, nil, []workflowmodel.WorkflowTask{task}, task, decisionErr, "actor", nil); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("state error=%v", err)
	}
	decisions.stateErr = nil
	if recovered, err := engine.recoverWorkflowDecision(t.Context(), process, nil, []workflowmodel.WorkflowTask{task}, task, decisionErr, "actor", nil); !errors.Is(err, decisionErr) || recovered.ID != process.ID || len(decisions.states) == 0 {
		t.Fatalf("recovered=%+v states=%v err=%v", recovered, decisions.states, err)
	}
}

func TestDecideTerminalWorkflowTaskValidationAndLookupOutcomes(t *testing.T) {
	principal := workflowAdminPrincipal()
	process, task := workflowTerminalFixture("any", nil)
	base := func() (*workflowProcessStoreEdgeStub, *workflowStateDecisionEdgeStub) {
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "approval", Status: "waiting"}}}}, getTaskValue: task, getTaskFound: true, tasks: []workflowmodel.WorkflowTask{task}}, &workflowStateDecisionEdgeStub{committed: true}
	}
	store, decisions := base()
	runtime := workflowTerminalRuntime(store, decisions, nil, nil)
	if _, _, err := runtime.DecideTerminalTask(t.Context(), "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	if _, handled, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "bad"}, principal); !handled || apperror.CodeOf(err) != "backend.workflow.task_decision_invalid" {
		t.Fatalf("invalid handled=%v err=%v", handled, err)
	}
	runtime.dependencies.Decisions = nil
	if _, handled, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principal); handled || err != nil {
		t.Fatalf("nil decisions handled=%v err=%v", handled, err)
	}
	runtime.dependencies.Decisions = decisions
	unknown := principal
	unknown.Known = false
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, unknown); apperror.CodeOf(err) != "backend.role.unknown" {
		t.Fatalf("unknown=%v", err)
	}
	store.getTaskErr = errors.New("task")
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("task error=%v", err)
	}
	store.getTaskErr = nil
	store.getTaskFound = false
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principal); apperror.CodeOf(err) != "backend.workflow.task_not_found" {
		t.Fatalf("not found=%v", err)
	}
	store.getTaskFound = true
	store.getTaskValue.AssigneeUserID = "other"
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principal); apperror.CodeOf(err) != "backend.workflow.task_assignee_required" {
		t.Fatalf("assignee=%v", err)
	}
	store.getTaskValue = task
	store.getTaskValue.Status = "approved"
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principal); apperror.CodeOf(err) != "backend.workflow.task_already_decided" {
		t.Fatalf("status=%v", err)
	}
	store.getTaskValue = task
	denied := principal
	denied = workflowPrincipalWithPermissions(denied)
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, denied); apperror.CodeOf(err) != "backend.workflow.task.act_permission_required" {
		t.Fatalf("permission=%v", err)
	}
	store.getProcessErr = errors.New("process")
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("process=%v", err)
	}
	store.getProcessErr = nil
	delete(store.processes, "process")
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("process missing=%v", err)
	}
	store.processes["process"] = process
	store.listTasksErr = errors.New("tasks")
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("tasks=%v", err)
	}
	store.listTasksErr = nil
	store.listNodesErr = errors.New("nodes")
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("nodes=%v", err)
	}
}

func TestDecideTerminalWorkflowTaskCommitPaths(t *testing.T) {
	principal := workflowAdminPrincipal()
	request := workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved", Comment: " ok ", IdempotencyKey: "key"}
	makeRuntime := func(process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask, tasks []workflowmodel.WorkflowTask, decisions *workflowStateDecisionEdgeStub, workers *workflowExecutionWorkerStub, identity identitysdk.Directory) (*WorkflowProcessRuntime, *workflowProcessStoreEdgeStub) {
		store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "approval", Status: "waiting"}}}}, getTaskValue: task, getTaskFound: true, tasks: tasks}
		return workflowTerminalRuntime(store, decisions, workers, identity), store
	}

	process, task := workflowTerminalFixture("all", nil)
	other := workflowmodel.WorkflowTask{ID: "other", ProcessID: "process", NodeID: "approval", Status: "open", Sequence: 2}
	decisions := &workflowStateDecisionEdgeStub{committed: true}
	runtime, _ := makeRuntime(process, task, []workflowmodel.WorkflowTask{task, other}, decisions, nil, nil)
	if actual, handled, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", request, principal); err != nil || !handled || actual.Status != "waiting" || len(decisions.commits) != 1 {
		t.Fatalf("nonterminal actual=%+v handled=%v commits=%v err=%v", actual, handled, decisions.commits, err)
	}

	process, task = workflowTerminalFixture("any", nil)
	decisions = &workflowStateDecisionEdgeStub{committed: true}
	workers := &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{"process": {ID: "process", Result: map[string]any{"node_id": "approval"}}}}
	sibling := workflowmodel.WorkflowTask{ID: "sibling", ProcessID: "process", NodeID: "approval", Status: "pending"}
	otherNode := workflowmodel.WorkflowTask{ID: "other-node", ProcessID: "process", NodeID: "other", Status: "open"}
	completedSibling := workflowmodel.WorkflowTask{ID: "completed-sibling", ProcessID: "process", NodeID: "approval", Status: "approved"}
	openSibling := workflowmodel.WorkflowTask{ID: "open-sibling", ProcessID: "process", NodeID: "approval", Status: "open"}
	runtime, _ = makeRuntime(process, task, []workflowmodel.WorkflowTask{task, sibling, otherNode, completedSibling, openSibling}, decisions, workers, nil)
	actual, handled, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", request, principal)
	if err != nil || !handled || actual.Status != "completed" || actual.Variables["approval_decision"] != "approved" || actual.Variables["approval_comment"] != "ok" || decisions.commits[0].WorkflowExecution == nil {
		t.Fatalf("terminal actual=%+v handled=%v commit=%+v err=%v", actual, handled, decisions.commits[0], err)
	}
	process, task = workflowTerminalFixture("any", nil)
	decisions = &workflowStateDecisionEdgeStub{committed: true}
	storeWithSkippedNode := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "approval", Status: "completed"}, {NodeID: "approval", Status: "waiting"}}}}, getTaskValue: task, getTaskFound: true, tasks: []workflowmodel.WorkflowTask{task}}
	if actual, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(storeWithSkippedNode, decisions, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, nil), "task", request, principal); err != nil || actual.Status != "completed" {
		t.Fatalf("skipped node terminal=%+v err=%v", actual, err)
	}
	for _, decision := range []string{"rejected", "returned"} {
		process, task = workflowTerminalFixture("any", nil)
		decisions = &workflowStateDecisionEdgeStub{committed: true}
		runtime, _ = makeRuntime(process, task, []workflowmodel.WorkflowTask{task}, decisions, nil, nil)
		if actual, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: decision}, principal); err != nil || actual.Status != "rejected" {
			t.Fatalf("decision=%s actual=%+v err=%v", decision, actual, err)
		}
	}

	nextApproval := definitionmodel.WorkflowGraphNode{
		ID: "next", Type: "approval", Name: "Next",
		Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
			Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"next-user"}}},
		}},
	}
	process, task = workflowTerminalFixture("any", &nextApproval)
	decisions = &workflowStateDecisionEdgeStub{committed: true}
	runtime, _ = makeRuntime(process, task, []workflowmodel.WorkflowTask{task}, decisions, &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, workflowApprovalIdentityStub{users: map[string]identitysdk.User{}})
	actual, _, err = decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", request, principal)
	if err != nil || actual.Status != "waiting" || len(decisions.commits[0].InsertTasks) != 1 || len(decisions.commits[0].InsertExecutions) != 1 {
		t.Fatalf("next approval actual=%+v commit=%+v err=%v", actual, decisions.commits[0], err)
	}

	nextCondition := definitionmodel.WorkflowGraphNode{ID: "next", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}}
	process, task = workflowTerminalFixture("any", &nextCondition)
	decisions = &workflowStateDecisionEdgeStub{committed: true}
	runtime, _ = makeRuntime(process, task, []workflowmodel.WorkflowTask{task}, decisions, nil, nil)
	actual, _, err = decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", request, principal)
	if err != nil || actual.Status != "running" || len(decisions.commits[0].InsertExecutions) != 1 {
		t.Fatalf("queued actual=%+v commit=%+v err=%v", actual, decisions.commits[0], err)
	}

	process, task = workflowTerminalFixture("any", nil)
	decisions = &workflowStateDecisionEdgeStub{decisionErr: errors.New("commit")}
	runtime, _ = makeRuntime(process, task, []workflowmodel.WorkflowTask{task}, decisions, nil, nil)
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", request, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("commit error=%v", err)
	}
	decisions.decisionErr = nil
	request.IdempotencyKey = ""
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", request, principal); apperror.CodeOf(err) != "backend.workflow.task_already_decided" {
		t.Fatalf("lost commit=%v", err)
	}
}

func TestLegacyWorkflowProcessEngineDecideTaskSuccessAndFailures(t *testing.T) {
	principal := workflowAdminPrincipal()
	process, task := workflowTerminalFixture("any", nil)
	base := func() *workflowProcessStoreEdgeStub {
		decided := task
		decided.Status, decided.Decision, decided.Comment = "approved", "approved", "ok"
		return &workflowProcessStoreEdgeStub{
			workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "approval", Status: "waiting"}}}},
			getTaskValue:                 task, getTaskFound: true, tasks: []workflowmodel.WorkflowTask{decided}, decideTaskValue: decided, decidedTask: true,
		}
	}
	request := workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved", Comment: " ok ", IdempotencyKey: "key"}
	store := base()
	actual, err := workflowEngineEdge(store, nil, nil).DecideTask(t.Context(), " task ", request, principal)
	if err != nil || actual.Status != "completed" || actual.Variables["approval_decision"] != "approved" || actual.Variables["approval_comment"] != "ok" {
		t.Fatalf("actual=%+v err=%v", actual, err)
	}
	if _, err := workflowEngineEdge(base(), nil, nil).DecideTask(t.Context(), "task", request, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	if _, err := workflowEngineEdge(base(), nil, nil).DecideTask(t.Context(), "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "bad"}, principal); apperror.CodeOf(err) != "backend.workflow.task_decision_invalid" {
		t.Fatalf("invalid=%v", err)
	}
	returnedStore := base()
	returnedStore.decideTaskValue.Status, returnedStore.decideTaskValue.Decision = "returned", "returned"
	returnedStore.tasks = []workflowmodel.WorkflowTask{returnedStore.decideTaskValue}
	if actual, err := workflowEngineEdge(returnedStore, nil, nil).DecideTask(t.Context(), "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "returned"}, principal); err != nil || actual.Status != "rejected" {
		t.Fatalf("returned=%+v err=%v", actual, err)
	}
	returnedProcess, returnedTask := workflowTerminalFixture("any", &definitionmodel.WorkflowGraphNode{ID: "next", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}})
	returnedProcess.DefinitionSnapshot.Graph.Edges[0].Branch = "returned"
	returnedDecided := returnedTask
	returnedDecided.Status, returnedDecided.Decision = "returned", "returned"
	returnedEdgeStore := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": returnedProcess}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "approval", Status: "waiting"}}}}, getTaskValue: returnedTask, getTaskFound: true, tasks: []workflowmodel.WorkflowTask{returnedDecided}, decideTaskValue: returnedDecided, decidedTask: true}
	if actual, err := workflowEngineEdge(returnedEdgeStore, nil, nil).DecideTask(t.Context(), "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "returned"}, principal); err != nil || actual.Status != "completed" {
		t.Fatalf("returned edge=%+v err=%v", actual, err)
	}
	store = base()
	store.decidedTask = false
	if _, err := workflowEngineEdge(store, nil, nil).DecideTask(t.Context(), "task", request, principal); apperror.CodeOf(err) != "backend.workflow.task_already_decided" {
		t.Fatalf("already decided=%v", err)
	}
	store = base()
	store.listTasksErr = errors.New("tasks")
	if _, err := workflowEngineEdge(store, nil, nil).DecideTask(t.Context(), "task", request, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("snapshot tasks=%v", err)
	}
	store = base()
	store.decideTaskValue.Decision, store.decideTaskValue.Status = "rejected", "rejected"
	store.tasks = []workflowmodel.WorkflowTask{store.decideTaskValue}
	actual, err = workflowEngineEdge(store, nil, nil).DecideTask(t.Context(), "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "rejected"}, principal)
	if err != nil || actual.Status != "rejected" {
		t.Fatalf("rejected=%+v err=%v", actual, err)
	}
	for name, mutate := range map[string]func(*workflowProcessStoreEdgeStub, *principalmodel.Principal){
		"task error": func(store *workflowProcessStoreEdgeStub, _ *principalmodel.Principal) {
			store.getTaskErr = errors.New("task")
		},
		"task missing": func(store *workflowProcessStoreEdgeStub, _ *principalmodel.Principal) { store.getTaskFound = false },
		"wrong assignee": func(store *workflowProcessStoreEdgeStub, _ *principalmodel.Principal) {
			store.getTaskValue.AssigneeUserID = "other"
		},
		"permission": func(_ *workflowProcessStoreEdgeStub, principal *principalmodel.Principal) {
			*principal = workflowPrincipalWithPermissions(*principal)
		},
		"process error": func(store *workflowProcessStoreEdgeStub, _ *principalmodel.Principal) {
			store.getProcessErr = errors.New("process")
		},
		"process missing": func(store *workflowProcessStoreEdgeStub, _ *principalmodel.Principal) {
			delete(store.processes, "process")
		},
		"node snapshot": func(store *workflowProcessStoreEdgeStub, _ *principalmodel.Principal) {
			store.listNodesErr = errors.New("nodes")
		},
		"decision error": func(store *workflowProcessStoreEdgeStub, _ *principalmodel.Principal) {
			store.decideTaskErr = errors.New("decision")
		},
		"complete approval": func(store *workflowProcessStoreEdgeStub, _ *principalmodel.Principal) {
			store.updateNodeErr = errors.New("node")
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidateStore := base()
			candidatePrincipal := principal
			mutate(candidateStore, &candidatePrincipal)
			if _, err := workflowEngineEdge(candidateStore, nil, nil).DecideTask(t.Context(), "task", request, candidatePrincipal); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	allProcess, allTask := workflowTerminalFixture("all", nil)
	allDecided := allTask
	allDecided.Status, allDecided.Decision = "approved", "approved"
	other := workflowmodel.WorkflowTask{ID: "other", ProcessID: "process", NodeID: "approval", Status: "open", Sequence: 2}
	allStore := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": allProcess}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "approval", Status: "waiting"}}}}, getTaskValue: allTask, getTaskFound: true, tasks: []workflowmodel.WorkflowTask{allDecided, other}, decideTaskValue: allDecided, decidedTask: true}
	if waiting, err := workflowEngineEdge(allStore, nil, nil).DecideTask(t.Context(), "task", request, principal); err != nil || waiting.Status != "waiting" {
		t.Fatalf("nonterminal=%+v err=%v", waiting, err)
	}
	if waiting, err := workflowEngineEdge(allStore, nil, nil).DecideTask(t.Context(), "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principal); err != nil || waiting.Status != "waiting" {
		t.Fatalf("nonterminal without key=%+v err=%v", waiting, err)
	}
	allStore.updateProcessErr = errors.New("persist")
	if _, err := workflowEngineEdge(allStore, nil, nil).DecideTask(t.Context(), "task", request, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("idempotency persist=%v", err)
	}
	rejectProcess, rejectTask := workflowTerminalFixture("any", nil)
	rejected := rejectTask
	rejected.Status, rejected.Decision = "rejected", "rejected"
	rejectStore := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": rejectProcess}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "approval", Status: "waiting"}}}}, getTaskValue: rejectTask, getTaskFound: true, tasks: []workflowmodel.WorkflowTask{rejected}, decideTaskValue: rejected, decidedTask: true, updateProcessErr: errors.New("reject")}
	if _, err := workflowEngineEdge(rejectStore, nil, nil).DecideTask(t.Context(), "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "rejected"}, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("reject persist=%v", err)
	}
	sequentialProcess, sequentialTask := workflowTerminalFixture("sequential", nil)
	sequentialDecided := sequentialTask
	sequentialDecided.Status, sequentialDecided.Decision = "approved", "approved"
	pending := workflowmodel.WorkflowTask{ID: "pending", ProcessID: "process", NodeID: "approval", Status: "pending", Sequence: 2}
	sequentialStore := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": sequentialProcess}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "approval", Status: "waiting"}}}}, getTaskValue: sequentialTask, getTaskFound: true, tasks: []workflowmodel.WorkflowTask{sequentialDecided, pending}, decideTaskValue: sequentialDecided, decidedTask: true, updateTaskErr: errors.New("open")}
	state := &workflowStateDecisionEdgeStub{}
	engine := NewWorkflowProcessEngine(WorkflowDependencies{Processes: sequentialStore, Decisions: state, Schema: workflowSchemaProviderEdgeStub{}})
	if _, err := engine.DecideTask(t.Context(), "task", request, principal); err == nil || len(state.states) != 1 {
		t.Fatalf("aggregate recovery states=%v err=%v", state.states, err)
	}
	nextMissingProcess, nextTask := workflowTerminalFixture("any", &definitionmodel.WorkflowGraphNode{ID: "next", Type: "bad"})
	nextDecided := nextTask
	nextDecided.Status, nextDecided.Decision = "approved", "approved"
	nextStore := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": nextMissingProcess}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "approval", Status: "waiting"}}}}, getTaskValue: nextTask, getTaskFound: true, tasks: []workflowmodel.WorkflowTask{nextDecided}, decideTaskValue: nextDecided, decidedTask: true}
	state = &workflowStateDecisionEdgeStub{}
	engine = NewWorkflowProcessEngine(WorkflowDependencies{Processes: nextStore, Decisions: state, Schema: workflowSchemaProviderEdgeStub{}})
	if _, err := engine.DecideTask(t.Context(), "task", request, principal); err == nil || len(state.states) != 1 {
		t.Fatalf("run recovery states=%v err=%v", state.states, err)
	}
}

func TestLegacyWorkflowDecisionRendersPostApprovalVariablesIntoReachableAction(t *testing.T) {
	principal := workflowAdminPrincipal()
	action := definitionmodel.WorkflowGraphNode{
		ID: "record-decision", Type: "action",
		Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{
			ActionKey: "document.record_decision",
			Input: map[string]any{
				"comment":  "$workflow.approval_comment",
				"decision": "$workflow.approval_decision",
			},
		}},
	}
	process, task := workflowTerminalFixture("any", &action)
	process.DefinitionSnapshot.Graph.Edges[0].Branch = "rejected"
	decided := task
	decided.Status, decided.Decision, decided.Comment = "rejected", "rejected", "needs more evidence"
	store := &workflowProcessStoreEdgeStub{
		workflowExecutionProcessStub: workflowExecutionProcessStub{
			processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process},
			nodes:     map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "approval", Status: "waiting"}}},
		},
		getTaskValue: task, getTaskFound: true, tasks: []workflowmodel.WorkflowTask{decided}, decideTaskValue: decided, decidedTask: true,
	}
	var input map[string]any
	engine := workflowEngineEdge(store, nil, func(_ context.Context, invocation WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		input = invocation.Input
		return WorkflowBusinessActionInvocationResult{}, nil
	})
	actual, err := engine.DecideTask(t.Context(), "task", workflowmodel.WorkflowTaskDecisionRequest{Decision: "rejected", Comment: "needs more evidence"}, principal)
	if err != nil || actual.Status != "completed" {
		t.Fatalf("decision process=%+v err=%v", actual, err)
	}
	if input["comment"] != "needs more evidence" || input["decision"] != "rejected" {
		t.Fatalf("post-approval Action input=%#v", input)
	}
}

func TestDecideTerminalWorkflowTaskSequentialPreparationWorkerAndConflictOutcomes(t *testing.T) {
	principal := workflowAdminPrincipal()
	request := workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved", Comment: "ok"}
	makeStore := func(process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask, tasks []workflowmodel.WorkflowTask, nodes []workflowmodel.WorkflowNodeInstance) *workflowProcessStoreEdgeStub {
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": nodes}}, getTaskValue: task, getTaskFound: true, tasks: tasks}
	}

	process, task := workflowTerminalFixture("sequential", nil)
	pending := workflowmodel.WorkflowTask{ID: "pending", ProcessID: "process", NodeID: "approval", Status: "pending", Sequence: 2}
	later := workflowmodel.WorkflowTask{ID: "later", ProcessID: "process", NodeID: "approval", Status: "pending", Sequence: 3}
	unrelatedPending := workflowmodel.WorkflowTask{ID: "unrelated", ProcessID: "process", NodeID: "other", Status: "pending"}
	store := makeStore(process, task, []workflowmodel.WorkflowTask{task, unrelatedPending, later, pending}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
	decisions := &workflowStateDecisionEdgeStub{committed: true}
	runtime := workflowTerminalRuntime(store, decisions, nil, nil)
	actual, handled, err := runtime.DecideTerminalTask(t.Context(), "task", request, principal)
	if err != nil || !handled || actual.Status != "waiting" || len(decisions.commits[0].UpdateTasks) != 1 {
		t.Fatalf("sequential actual=%+v handled=%v commit=%+v err=%v", actual, handled, decisions.commits[0], err)
	}
	for name, decision := range map[string]*workflowStateDecisionEdgeStub{
		"error":    {decisionErr: errors.New("commit")},
		"conflict": {},
	} {
		t.Run(name, func(t *testing.T) {
			candidateStore := makeStore(process, task, []workflowmodel.WorkflowTask{task, pending}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
			_, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(candidateStore, decision, nil, nil), "task", request, principal)
			if apperror.CodeOf(err) != map[string]string{"error": "backend.internal", "conflict": "backend.workflow.task_already_decided"}[name] {
				t.Fatalf("error=%v", err)
			}
		})
	}
	replayRequest := request
	replayRequest.IdempotencyKey = "replay"
	store = makeStore(process, task, []workflowmodel.WorkflowTask{task, pending}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
	if replay, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(store, &workflowStateDecisionEdgeStub{}, nil, nil), "task", replayRequest, principal); err != nil || replay.ID != process.ID {
		t.Fatalf("nonterminal replay=%+v err=%v", replay, err)
	}

	process, task = workflowTerminalFixture("any", nil)
	store = makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "other", Status: "waiting"}})
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(store, &workflowStateDecisionEdgeStub{committed: true}, nil, nil), "task", request, principal); apperror.CodeOf(err) != "backend.workflow.approval_node_instance_not_found" {
		t.Fatalf("missing approval node=%v", err)
	}

	nextBad := definitionmodel.WorkflowGraphNode{ID: "next", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
		Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "bad"}},
	}}}
	process, task = workflowTerminalFixture("any", &nextBad)
	store = makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "ignored"}, {NodeID: "approval", Status: "waiting"}})
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(store, &workflowStateDecisionEdgeStub{committed: true}, nil, workflowApprovalIdentityStub{}), "task", request, principal); apperror.CodeOf(err) != "backend.workflow.approval_resolver_invalid" {
		t.Fatalf("prepare error=%v", err)
	}

	nextApproval := definitionmodel.WorkflowGraphNode{ID: "next", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
		Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"user"}}},
	}}}
	process, task = workflowTerminalFixture("any", &nextApproval)
	for name, worker := range map[string]*workflowExecutionWorkerStub{
		"load error": {executions: map[string]workflowmodel.WorkflowExecution{}, getErr: errors.New("load")},
		"found":      {executions: map[string]workflowmodel.WorkflowExecution{"process": {ID: "process", Result: map[string]any{}}}},
	} {
		t.Run(name, func(t *testing.T) {
			decision := &workflowStateDecisionEdgeStub{committed: true}
			candidateStore := makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
			actual, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(candidateStore, decision, worker, workflowApprovalIdentityStub{}), "task", request, principal)
			if name == "load error" && apperror.CodeOf(err) != "backend.internal" {
				t.Fatalf("error=%v", err)
			}
			if name == "found" && (err != nil || actual.Status != "waiting" || decision.commits[0].WorkflowExecution == nil) {
				t.Fatalf("actual=%+v commit=%+v err=%v", actual, decision.commits[0], err)
			}
		})
	}
	for name, decision := range map[string]*workflowStateDecisionEdgeStub{"error": {decisionErr: errors.New("commit")}, "conflict": {}} {
		t.Run("approval "+name, func(t *testing.T) {
			candidateStore := makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
			_, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(candidateStore, decision, nil, workflowApprovalIdentityStub{}), "task", request, principal)
			if apperror.CodeOf(err) != map[string]string{"error": "backend.internal", "conflict": "backend.workflow.task_already_decided"}[name] {
				t.Fatalf("error=%v", err)
			}
		})
	}
	store = makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
	if replay, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(store, &workflowStateDecisionEdgeStub{}, nil, workflowApprovalIdentityStub{}), "task", replayRequest, principal); err != nil || replay.ID != process.ID {
		t.Fatalf("approval replay=%+v err=%v", replay, err)
	}

	nextCondition := definitionmodel.WorkflowGraphNode{ID: "next", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}}
	process, task = workflowTerminalFixture("any", &nextCondition)
	for name, worker := range map[string]*workflowExecutionWorkerStub{
		"load error": {executions: map[string]workflowmodel.WorkflowExecution{}, getErr: errors.New("load")},
		"found":      {executions: map[string]workflowmodel.WorkflowExecution{"process": {ID: "process", Result: map[string]any{}}}},
	} {
		t.Run("queue "+name, func(t *testing.T) {
			decision := &workflowStateDecisionEdgeStub{committed: name != "found"}
			candidateStore := makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
			actual, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(candidateStore, decision, worker, nil), "task", request, principal)
			if name == "load error" && apperror.CodeOf(err) != "backend.internal" {
				t.Fatalf("error=%v", err)
			}
			if name == "found" && (apperror.CodeOf(err) != "backend.workflow.task_already_decided" || actual.Status != "" || decision.commits[0].WorkflowExecution == nil) {
				t.Fatalf("actual=%+v commit=%+v err=%v", actual, decision.commits[0], err)
			}
		})
	}
	for name, decision := range map[string]*workflowStateDecisionEdgeStub{"error": {decisionErr: errors.New("commit")}, "conflict": {}} {
		t.Run("queue "+name, func(t *testing.T) {
			candidateStore := makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
			_, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(candidateStore, decision, nil, nil), "task", request, principal)
			if apperror.CodeOf(err) != map[string]string{"error": "backend.internal", "conflict": "backend.workflow.task_already_decided"}[name] {
				t.Fatalf("error=%v", err)
			}
		})
	}
	store = makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
	if replay, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(store, &workflowStateDecisionEdgeStub{}, nil, nil), "task", replayRequest, principal); err != nil || replay.ID != process.ID {
		t.Fatalf("queue replay=%+v err=%v", replay, err)
	}
	for name, failingContinuation := range map[string]bool{"claimed": false, "resume error": true, "not claimed": false} {
		t.Run("continuation "+name, func(t *testing.T) {
			candidateStore := makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
			continuation := process
			if failingContinuation {
				continuation.DefinitionSnapshot.Graph = &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{ID: "next", Type: "bad"}}}
			}
			worker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, whereResults: []bool{name != "not claimed"}, continuation: continuation, continuationFound: true}
			decision := &workflowStateDecisionEdgeStub{committed: true}
			runtime := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: candidateStore, Workers: worker, Decisions: decision, WorkflowRegistry: &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{}}, Schema: workflowSchemaProviderEdgeStub{}})
			actual, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), runtime, "task", request, principal)
			if name == "claimed" && (err != nil || actual.Status != "completed") {
				t.Fatalf("actual=%+v err=%v", actual, err)
			}
			if name == "resume error" && err == nil {
				t.Fatal("expected resume error")
			}
			if name == "not claimed" && (err != nil || actual.Status != "running") {
				t.Fatalf("actual=%+v err=%v", actual, err)
			}
		})
	}

	process, task = workflowTerminalFixture("any", nil)
	worker := &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}, getErr: errors.New("load")}
	store = makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
	if _, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(store, &workflowStateDecisionEdgeStub{committed: true}, worker, nil), "task", request, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("terminal execution load=%v", err)
	}
	store = makeStore(process, task, []workflowmodel.WorkflowTask{task}, []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "waiting"}})
	if replay, _, err := decideTerminalWorkflowTaskWithContext(t.Context(), workflowTerminalRuntime(store, &workflowStateDecisionEdgeStub{}, nil, nil), "task", replayRequest, principal); err != nil || replay.ID != process.ID {
		t.Fatalf("terminal replay=%+v err=%v", replay, err)
	}
}
