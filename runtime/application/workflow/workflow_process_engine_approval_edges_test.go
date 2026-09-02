package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowApprovalIdentityStub struct {
	workflowDirectoryTestStub
	users          map[string]identitysdk.User
	findErr        map[string]error
	directoryUsers []identitysdk.User
	roles          []identitysdk.Role
	assignments    map[string][]identitysdk.UserRoleAssignment
	usersErr       error
	rolesErr       error
	assignmentErr  error
}

func (s workflowApprovalIdentityStub) FindUser(_ context.Context, lookup identitysdk.UserLookup) (identitysdk.User, bool, error) {
	id := string(lookup.UserID)
	if err := s.findErr[id]; err != nil {
		return identitysdk.User{}, false, err
	}
	user, ok := s.users[id]
	return user, ok, nil
}
func (s workflowApprovalIdentityStub) ListUsers(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.User, error) {
	if s.usersErr != nil || len(s.directoryUsers) > 0 {
		return s.directoryUsers, s.usersErr
	}
	users := make([]identitysdk.User, 0, len(s.users))
	for id, user := range s.users {
		if user.ID == "" {
			user.ID = id
		}
		users = append(users, user)
	}
	return users, nil
}
func (s workflowApprovalIdentityStub) ListRoles(context.Context, identitysdk.DirectoryQuery) ([]identitysdk.Role, error) {
	return s.roles, s.rolesErr
}
func (s workflowApprovalIdentityStub) ListUserRoleAssignments(_ context.Context, query identitysdk.UserRoleAssignmentQuery) ([]identitysdk.UserRoleAssignment, error) {
	id := string(query.UserID)
	if id == "" {
		out := []identitysdk.UserRoleAssignment{}
		for userID, assignments := range s.assignments {
			for _, assignment := range assignments {
				if assignment.UserID == "" {
					assignment.UserID = userID
				}
				out = append(out, assignment)
			}
		}
		return out, s.assignmentErr
	}
	return s.assignments[id], s.assignmentErr
}
func workflowEngineEdge(store *workflowProcessStoreEdgeStub, identity identitysdk.Directory, invoke func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error)) *WorkflowProcessEngine {
	return NewWorkflowProcessEngine(WorkflowDependencies{Processes: store, Identity: identity, Schema: workflowSchemaProviderEdgeStub{}, InvokeAction: invoke})
}

func workflowEngineProcess(nodes []definitionmodel.WorkflowGraphNode, edges []definitionmodel.WorkflowGraphEdge) workflowmodel.WorkflowProcessInstance {
	return workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", WorkflowKey: "flow", ObjectKey: "order", RecordID: "record", InitiatorID: "initiator", Variables: map[string]any{"employee": "employee", "record_owner": "owner"}, DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow", Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: nodes, Edges: edges}}}
}

func TestWorkflowProcessEngineStartAndRunFailureOutcomes(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	base := func() *workflowProcessStoreEdgeStub {
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	}
	engine := workflowEngineEdge(base(), nil, nil)
	if _, err := engine.Start(t.Context(), definitionmodel.WorkflowSchema{}, nil, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	if _, err := engine.Start(t.Context(), definitionmodel.WorkflowSchema{}, nil, principal); apperror.CodeOf(err) != "backend.workflow.process_graph_required" {
		t.Fatalf("graph required=%v", err)
	}
	if _, err := engine.Start(t.Context(), definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{}}, nil, principal); apperror.CodeOf(err) != "backend.workflow.process_graph_required" {
		t.Fatalf("empty graph required=%v", err)
	}
	invalid := definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "bad", Type: "unknown"}}}}
	if _, err := engine.Start(t.Context(), invalid, nil, principal); err == nil {
		t.Fatal("expected graph validation error")
	}
	valid := definitionmodel.WorkflowSchema{Key: "flow", Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "start", Type: "trigger"}}}}
	store := base()
	store.insertProcessErr = errors.New("insert")
	if _, err := workflowEngineEdge(store, nil, nil).Start(t.Context(), valid, nil, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("insert process=%v", err)
	}
	store = base()
	store.insertNodeErr = errors.New("node")
	if process, err := workflowEngineEdge(store, nil, nil).Start(t.Context(), valid, nil, principal); err == nil || process.Status != "configuration_error" {
		t.Fatalf("insert node process=%+v err=%v", process, err)
	}
	store = base()
	process, err := workflowEngineEdge(store, nil, nil).Start(t.Context(), valid, map[string]any{"object_key": "order", "record_id": "record"}, principal)
	if err != nil || process.Status != "completed" || process.ObjectKey != "order" || process.RecordID != "record" {
		t.Fatalf("success process=%+v err=%v", process, err)
	}
	valid.TriggerContract = &definitionmodel.WorkflowTriggerContract{Type: "manual", ObjectKey: "refund_request"}
	store = base()
	process, err = workflowEngineEdge(store, nil, nil).Start(t.Context(), valid, map[string]any{"record_id": "refund-1"}, principal)
	if err != nil || process.ObjectKey != "refund_request" || process.RecordID != "refund-1" ||
		process.Variables["object_key"] != "refund_request" || process.Variables["record_id"] != "refund-1" {
		t.Fatalf("manual target propagation process=%+v err=%v", process, err)
	}

	condition := definitionmodel.WorkflowGraphNode{ID: "condition", Type: "condition", Config: map[string]any{"expression": "true"}}
	runProcess := workflowEngineProcess([]definitionmodel.WorkflowGraphNode{condition}, nil)
	store = base()
	store.updateProcessErr = errors.New("update")
	if _, err := workflowEngineEdge(store, nil, nil).runWithContext(t.Context(), runProcess, []string{"condition", "condition"}, nil, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update error=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := workflowEngineEdge(base(), nil, nil).runWithContext(cancelled, runProcess, []string{"condition"}, nil, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	if failed, err := workflowEngineEdge(base(), nil, nil).runWithContext(t.Context(), runProcess, []string{"missing"}, nil, principal); err == nil || failed.Status != "configuration_error" {
		t.Fatalf("missing process=%+v err=%v", failed, err)
	}
}

func TestWorkflowProcessEngineNodeAndActionOutcomes(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	process := workflowEngineProcess(nil, nil)
	engine := workflowEngineEdge(store, nil, func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		return WorkflowBusinessActionInvocationResult{InvocationID: "invoke", Status: "success", Output: map[string]any{"data": map[string]any{"outreach_body": "candidate-safe"}}, OutboxIDs: []string{"outbox"}, Record: &WorkflowBusinessActionRecordResult{RecordID: "record"}}, nil
	})
	condition := definitionmodel.WorkflowGraphNode{ID: "condition", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}}
	if outcome, waiting, err := engine.executeNode(t.Context(), &process, condition, principal); err != nil || outcome != "true" || waiting {
		t.Fatalf("condition outcome=%s waiting=%v err=%v", outcome, waiting, err)
	}
	condition.Contract = nil
	condition.Config = map[string]any{"expression": "false"}
	if outcome, _, err := engine.executeNode(t.Context(), &process, condition, principal); err != nil || outcome != "false" {
		t.Fatalf("false condition outcome=%s err=%v", outcome, err)
	}
	contractWithoutCondition := definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{}, Config: map[string]any{"expression": "false"}}
	if workflowProcessConditionMatches(t.Context(), contractWithoutCondition, nil) {
		t.Fatal("empty condition contract matched")
	}
	condition.Contract = &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "field_equals", Field: "status", Value: "approved"}}
	if !workflowProcessConditionMatches(t.Context(), condition, map[string]any{"after": map[string]any{"status": "approved"}}) {
		t.Fatal("after condition did not match")
	}
	action := definitionmodel.WorkflowGraphNode{ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "update", Input: map[string]any{"value": "fixed"}, OutputVariable: "result"}}}
	if outcome, waiting, err := engine.executeNode(t.Context(), &process, action, principal); err != nil || outcome != "success" || waiting || process.Variables["result"] == nil {
		t.Fatalf("action outcome=%s waiting=%v variables=%v err=%v", outcome, waiting, process.Variables, err)
	}
	if result, ok := process.Variables["result"].(map[string]any); !ok || result["outreach_body"] != "candidate-safe" || result["action_key"] != "update" {
		t.Fatalf("action output was not projected into workflow variables: %#v", process.Variables["result"])
	}
	objectAction := definitionmodel.WorkflowGraphNode{ID: "object-action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "reconcile", ObjectKey: "message", RecordID: "$record.id", OutputVariable: "object_result"}}}
	objectEngine := workflowEngineEdge(store, nil, func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		return WorkflowBusinessActionInvocationResult{InvocationID: "object-invoke", Status: "success", Output: map[string]any{"data": map[string]any{"reconciled": true}}}, nil
	})
	process.Variables["record"] = map[string]any{"id": "message-1"}
	if outcome, _, err := objectEngine.executeNode(t.Context(), &process, objectAction, principal); err != nil || outcome != "success" {
		t.Fatalf("object action outcome=%s err=%v", outcome, err)
	}
	if result, ok := process.Variables["object_result"].(map[string]any); !ok || result["record_id"] != "record" || result["reconciled"] != true {
		t.Fatalf("object action output was not projected safely: %#v", process.Variables["object_result"])
	}
	var forwardedInput map[string]any
	strictInputEngine := workflowEngineEdge(store, nil, func(_ context.Context, invocation WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		forwardedInput = invocation.Input
		return WorkflowBusinessActionInvocationResult{Record: &WorkflowBusinessActionRecordResult{RecordID: "record"}}, nil
	})
	process.Variables["workflow_internal"] = "must-not-reach-action"
	if _, err := strictInputEngine.executeActionNode(t.Context(), &process, action, principal); err != nil {
		t.Fatalf("strict action input=%v", err)
	}
	if len(forwardedInput) != 1 || forwardedInput["value"] != "fixed" || forwardedInput["workflow_internal"] != nil {
		t.Fatalf("workflow variables leaked into action input: %#v", forwardedInput)
	}
	emptyRecordEngine := workflowEngineEdge(store, nil, func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		return WorkflowBusinessActionInvocationResult{Record: &WorkflowBusinessActionRecordResult{}, Output: map[string]any{"outreach_body": "top", "data": map[string]any{"outreach_body": "nested"}}}, nil
	})
	if output, err := emptyRecordEngine.executeActionNode(t.Context(), &process, action, principal); err != nil || output["record_id"] != "record" || output["outreach_body"] != "top" {
		t.Fatalf("empty record/colliding output=%#v err=%v", output, err)
	}
	originalCloneOutput := workflowCloneActionOutput
	workflowCloneActionOutput = func(map[string]any) map[string]any { return nil }
	t.Cleanup(func() { workflowCloneActionOutput = originalCloneOutput })
	if output, err := emptyRecordEngine.executeActionNode(t.Context(), &process, action, principal); err != nil || output == nil {
		t.Fatalf("nil cloned output=%#v err=%v", output, err)
	}
	for name, onError := range map[string]string{"continue": "continue", "branch": "error_branch", "fail": "fail"} {
		t.Run(name, func(t *testing.T) {
			failing := action
			failing.Contract.Action.OnError = onError
			failingEngine := workflowEngineEdge(store, nil, func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
				return WorkflowBusinessActionInvocationResult{}, errors.New("invoke")
			})
			outcome, _, err := failingEngine.executeNode(t.Context(), &process, failing, principal)
			if onError == "continue" && (err != nil || outcome != "success") {
				t.Fatalf("outcome=%s err=%v", outcome, err)
			}
			if onError == "error_branch" && (err != nil || outcome != "error") {
				t.Fatalf("outcome=%s err=%v", outcome, err)
			}
			if onError == "fail" && err == nil {
				t.Fatal("expected failure")
			}
		})
	}
	if _, _, err := engine.executeNode(t.Context(), &process, definitionmodel.WorkflowGraphNode{ID: "bad", Type: "bad"}, principal); apperror.CodeOf(err) != "backend.workflow.graph_node_type_invalid" {
		t.Fatalf("invalid node=%v", err)
	}
	if _, err := engine.executeActionNode(t.Context(), &process, definitionmodel.WorkflowGraphNode{ID: "missing", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{}}}, principal); apperror.CodeOf(err) != "backend.workflow.action_key_required" {
		t.Fatalf("missing action=%v", err)
	}
	if _, err := engine.executeActionNode(t.Context(), &process, definitionmodel.WorkflowGraphNode{ID: "nil-action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "<nil>"}}}, principal); apperror.CodeOf(err) != "backend.workflow.action_key_required" {
		t.Fatalf("nil action=%v", err)
	}
	fallback := action
	fallback.Contract = &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "update", ObjectKey: "<nil>", RecordID: "<nil>"}}
	if _, err := engine.executeActionNode(t.Context(), &process, fallback, principal); err != nil {
		t.Fatalf("nil fallback=%v", err)
	}
	explicitTarget := action
	explicitTarget.Contract = &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "update", ObjectKey: "order", RecordID: "record"}}
	if _, err := engine.executeActionNode(t.Context(), &process, explicitTarget, principal); err != nil {
		t.Fatalf("explicit target=%v", err)
	}
	noOutput := action
	noOutput.Contract = &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "update", Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 1}}}
	if _, err := engine.executeActionNode(t.Context(), &process, noOutput, principal); err != nil {
		t.Fatalf("no output=%v", err)
	}
	ctxNilProcess := process
	if _, err := engine.executeActionNode(nil, &ctxNilProcess, action, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("nil context=%v", err)
	}
	attempts := 0
	idempotencyKeys := []string{}
	retry := action
	retry.Contract.Action.Retry = &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 2}
	retry.Contract.Action.TimeoutSeconds = 1
	retryEngine := workflowEngineEdge(store, nil, func(_ context.Context, invocation WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		attempts++
		idempotencyKeys = append(idempotencyKeys, invocation.IdempotencyKey)
		if invocation.Source != WorkflowBusinessActionSourceWorkflow || invocation.ProcessID != process.ID || invocation.NodeID != retry.ID {
			t.Fatalf("invocation=%#v", invocation)
		}
		if attempts == 1 {
			return WorkflowBusinessActionInvocationResult{Retryable: true, ErrorCode: "temporary"}, errors.New("temporary")
		}
		return WorkflowBusinessActionInvocationResult{Record: &WorkflowBusinessActionRecordResult{RecordID: "done"}}, nil
	})
	if _, err := retryEngine.executeActionNode(t.Context(), &process, retry, principal); err != nil || attempts != 2 || len(idempotencyKeys) != 2 || idempotencyKeys[0] == "" || idempotencyKeys[0] != idempotencyKeys[1] {
		t.Fatalf("retry attempts=%d keys=%v err=%v", attempts, idempotencyKeys, err)
	}
	firstExecutionKey := idempotencyKeys[0]
	store.nodes[process.ID] = append(store.nodes[process.ID], workflowmodel.WorkflowNodeInstance{NodeID: retry.ID, Iteration: 99, Status: "failed"})
	idempotencyKeys = nil
	attempts = 1
	if _, err := retryEngine.executeActionNode(t.Context(), &process, retry, principal); err != nil || len(idempotencyKeys) != 1 || idempotencyKeys[0] == firstExecutionKey {
		t.Fatalf("recovered execution must advance idempotency key: first=%q recovered=%v err=%v", firstExecutionKey, idempotencyKeys, err)
	}
	attempts = 0
	failedRetryEngine := workflowEngineEdge(store, nil, func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		attempts++
		return WorkflowBusinessActionInvocationResult{Retryable: true}, errors.New("retry")
	})
	if _, err := failedRetryEngine.executeActionNode(t.Context(), &process, retry, principal); err == nil || attempts != 2 {
		t.Fatalf("exhausted attempts=%d err=%v", attempts, err)
	}
	store.listNodesErr = errors.New("nodes")
	instance := engine.newNodeInstance(t.Context(), process.WorkspaceID, process.ID, action, "running", nil, nil)
	if instance.CompletedAt != "" {
		t.Fatalf("running instance=%+v", instance)
	}
	store.listNodesErr = nil
	store.insertNodeErr = errors.New("node")
	if err := engine.recordCompletedNode(t.Context(), process, condition, nil, nil, principal.UserID); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("record error=%v", err)
	}
}

func TestWorkflowProcessEngineApprovalCCAndWaitingRunOutcomes(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	identity := workflowApprovalIdentityStub{users: map[string]identitysdk.User{"approver": {ID: "approver", Name: "Approver"}}}
	base := func() *workflowProcessStoreEdgeStub {
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	}
	approval := definitionmodel.WorkflowGraphNode{
		ID: "approval", Type: "approval", Name: "Approval",
		Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
			Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"approver"}}},
		}},
	}
	process := workflowEngineProcess([]definitionmodel.WorkflowGraphNode{approval}, nil)
	store := base()
	engine := workflowEngineEdge(store, identity, nil)
	if outcome, waiting, err := engine.executeNode(t.Context(), &process, approval, principal); err != nil || outcome != "waiting" || !waiting || len(store.insertedTasks) != 1 {
		t.Fatalf("approval outcome=%s waiting=%v tasks=%v err=%v", outcome, waiting, store.insertedTasks, err)
	}
	store = base()
	store.insertNodeErr = errors.New("insert")
	if _, _, err := workflowEngineEdge(store, identity, nil).executeNode(t.Context(), &process, approval, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("approval insert=%v", err)
	}
	empty := approval
	empty.Contract = &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{EmptyAssigneePolicy: "skip", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users"}}}}
	store = base()
	if outcome, waiting, err := workflowEngineEdge(store, identity, nil).executeNode(t.Context(), &process, empty, principal); err != nil || outcome != "skipped" || waiting {
		t.Fatalf("skipped outcome=%s waiting=%v err=%v", outcome, waiting, err)
	}
	store = base()
	store.updateNodeErr = errors.New("update")
	if _, _, err := workflowEngineEdge(store, identity, nil).executeNode(t.Context(), &process, empty, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("skip update=%v", err)
	}
	invalid := approval
	invalid.Contract = &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "bad"}}}}
	store = base()
	if _, _, err := workflowEngineEdge(store, identity, nil).executeNode(t.Context(), &process, invalid, principal); apperror.CodeOf(err) != "backend.workflow.approval_resolver_invalid" || len(store.updatedNodes) != 1 {
		t.Fatalf("configuration err=%v nodes=%v", err, store.updatedNodes)
	}

	cc := definitionmodel.WorkflowGraphNode{
		ID: "cc", Type: "cc", Name: "Notify",
		Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{
			NotificationActionKey: "notify", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"recipient"}}},
		}},
	}
	store = base()
	ccEngine := workflowEngineEdge(store, identity, func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		return WorkflowBusinessActionInvocationResult{InvocationID: "invocation"}, nil
	})
	if outcome, waiting, err := ccEngine.executeNode(t.Context(), &process, cc, principal); err != nil || outcome != "success" || waiting {
		t.Fatalf("cc outcome=%s waiting=%v err=%v", outcome, waiting, err)
	}
	failingCC := workflowEngineEdge(store, identity, func(context.Context, WorkflowBusinessActionInvocation) (WorkflowBusinessActionInvocationResult, error) {
		return WorkflowBusinessActionInvocationResult{}, errors.New("notify")
	})
	if _, _, err := failingCC.executeNode(t.Context(), &process, cc, principal); err == nil {
		t.Fatal("expected cc failure")
	}
	store = base()
	waitingProcess, err := workflowEngineEdge(store, identity, nil).runWithContext(t.Context(), process, []string{"approval"}, []string{"existing"}, principal)
	if err != nil || waitingProcess.Status != "waiting" || len(waitingProcess.CurrentNodeIDs) != 2 {
		t.Fatalf("waiting process=%+v err=%v", waitingProcess, err)
	}
	nilContextProcess := workflowEngineProcess([]definitionmodel.WorkflowGraphNode{{ID: "condition", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}}}, nil)
	if actual, err := workflowEngineEdge(base(), identity, nil).runWithContext(nil, nilContextProcess, []string{"condition"}, nil, principal); err != nil || actual.Status != "completed" {
		t.Fatalf("nil context process=%+v err=%v", actual, err)
	}
	graph := &definitionmodel.WorkflowGraphSchema{Edges: []definitionmodel.WorkflowGraphEdge{{Source: "a", Target: "b"}, {Source: "a", Target: "b"}}}
	if len(engine.nextNodeIDs(nil, "a", "success")) != 0 || len(engine.nextNodeIDs(graph, "a", "success")) != 1 {
		t.Fatal("next-node edge filtering failed")
	}
}

func TestWorkflowApprovalResolverTaskAndAggregationOutcomes(t *testing.T) {
	identity := workflowApprovalIdentityStub{
		users: map[string]identitysdk.User{
			"initiator": {ID: "initiator", ManagerUserID: "manager", Status: identitysdk.UserStatusActive},
			"employee":  {ID: "employee", ManagerUserID: "manager", Status: identitysdk.UserStatusActive},
			"manager":   {ID: "manager", Name: "Manager", Status: identitysdk.UserStatusActive},
		},
		directoryUsers: []identitysdk.User{{ID: "role-user", Status: identitysdk.UserStatusActive}, {ID: "disabled", Status: "disabled"}},
		roles:          []identitysdk.Role{{ID: "role-id", Key: "approver"}}, assignments: map[string][]identitysdk.UserRoleAssignment{"role-user": {{UserID: "role-user", RoleID: "role-id"}}, "disabled": {{UserID: "disabled", RoleID: "role-id"}}},
	}
	store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	engine := workflowEngineEdge(store, identity, nil)
	process := workflowEngineProcess(nil, nil)
	principal := workflowProcessQueryPrincipal()
	strategies := []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"direct", "direct"}}, {Type: "manager", UserField: "employee"}, {Type: "manager_of", UserField: "employee"}, {Type: "initiator_manager"}, {Type: "record_field", Field: "record_owner"}, {Type: "role", RoleKey: "approver"}}
	for _, strategy := range strategies {
		users, _, err := engine.resolveApprovalAssigneeStrategy(t.Context(), process, strategy, principal)
		if err != nil || len(users) == 0 {
			t.Fatalf("strategy=%s users=%v err=%v", strategy.Type, users, err)
		}
	}
	if _, _, err := engine.resolveApprovalAssigneeStrategy(t.Context(), process, definitionmodel.WorkflowAssigneeResolver{Type: "bad"}, principal); apperror.CodeOf(err) != "backend.workflow.approval_resolver_invalid" {
		t.Fatalf("invalid resolver=%v", err)
	}
	if users, _, err := engine.resolveApprovalAssigneeStrategy(t.Context(), process, definitionmodel.WorkflowAssigneeResolver{Type: "manager_of"}, principal); err != nil || len(users) != 0 {
		t.Fatalf("manager_of without a user field must not fall back to initiator: users=%v err=%v", users, err)
	}
	if _, _, err := workflowEngineEdge(store, nil, nil).resolveApprovalAssignees(t.Context(), process, definitionmodel.WorkflowGraphNode{}, principal); apperror.CodeOf(err) != "backend.workflow.approval_identity_unavailable" {
		t.Fatalf("missing identity=%v", err)
	}
	node := definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Name: "Approve", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "sequential", Resolvers: strategies[:1]}}}
	instance := workflowmodel.WorkflowNodeInstance{ID: "node"}
	count, err := engine.createApprovalTasks(t.Context(), process, node, instance, principal)
	if err != nil || count != 1 || len(store.insertedTasks) != 1 || store.insertedTasks[0].AssigneeName != "" {
		t.Fatalf("count=%d tasks=%+v err=%v", count, store.insertedTasks, err)
	}
	empty := node
	empty.Contract.Approval.Resolvers = []definitionmodel.WorkflowAssigneeResolver{{Type: "users"}}
	if _, err := engine.createApprovalTasks(t.Context(), process, empty, instance, principal); apperror.CodeOf(err) != "backend.workflow.approval_assignee_not_found" {
		t.Fatalf("empty=%v", err)
	}
	empty.Contract.Approval.EmptyAssigneePolicy = "skip"
	if count, err := engine.createApprovalTasks(t.Context(), process, empty, instance, principal); err != nil || count != 0 {
		t.Fatalf("skip count=%d err=%v", count, err)
	}
	multiple := node
	multiple.Contract = &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "sequential", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"direct", "manager"}}}}}
	store.insertedTasks = nil
	if count, err := engine.createApprovalTasks(t.Context(), process, multiple, instance, principal); err != nil || count != 2 || store.insertedTasks[1].Status != "pending" {
		t.Fatalf("sequential count=%d tasks=%v err=%v", count, store.insertedTasks, err)
	}
	nilTitle := multiple
	nilTitle.Contract = &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Title: "<nil>", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"direct"}}}}}
	if _, err := engine.createApprovalTasks(t.Context(), process, nilTitle, instance, principal); err != nil {
		t.Fatal(err)
	}
	explicitTitle := multiple
	explicitTitle.Contract = &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Title: "Review", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"direct"}}}}}
	if _, err := engine.createApprovalTasks(t.Context(), process, explicitTitle, instance, principal); err != nil {
		t.Fatal(err)
	}
	errorIdentity := identity
	errorIdentity.findErr = map[string]error{"direct": errors.New("name")}
	if _, err := workflowEngineEdge(store, errorIdentity, nil).createApprovalTasks(t.Context(), process, multiple, instance, principal); err != nil {
		t.Fatalf("ignored name lookup=%v", err)
	}
	store.insertTaskErr = errors.New("task")
	if _, err := engine.createApprovalTasks(t.Context(), process, multiple, instance, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("insert task=%v", err)
	}
	store.insertTaskErr = nil
	roleFirst := definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
		ResolverMode: "first_match",
		Resolvers:    []definitionmodel.WorkflowAssigneeResolver{{Type: "role", RoleKey: "approver"}, {Type: "users", UserIDs: []string{"ignored"}}},
	}}}
	if users, role, err := engine.resolveApprovalAssignees(t.Context(), process, roleFirst, principal); err != nil || role != "approver" || len(users) != 1 || users[0] != "role-user" {
		t.Fatalf("first match users=%v role=%s err=%v", users, role, err)
	}
	firstEmpty := definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{
		ResolverMode: "first_match", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users"}, {Type: "users", UserIDs: []string{"direct"}}},
	}}}
	if users, _, err := engine.resolveApprovalAssignees(t.Context(), process, firstEmpty, principal); err != nil || len(users) != 1 {
		t.Fatalf("first empty users=%v err=%v", users, err)
	}

	approvalProcess := workflowEngineProcess([]definitionmodel.WorkflowGraphNode{node}, nil)
	store.tasks = []workflowmodel.WorkflowTask{{ID: "a", NodeID: "approval", Status: "approved", Sequence: 1}, {ID: "b", NodeID: "approval", Status: "open", Sequence: 2}}
	if outcome, complete, err := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); err != nil || outcome != "" || complete {
		t.Fatalf("sequential outcome=%s complete=%v err=%v", outcome, complete, err)
	}
	store.tasks[1].Status = "approved"
	if outcome, complete, err := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); err != nil || outcome != "approved" || !complete {
		t.Fatalf("complete outcome=%s complete=%v err=%v", outcome, complete, err)
	}
	store.tasks[0].Status = "returned"
	if outcome, complete, _ := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); outcome != "returned" || !complete {
		t.Fatalf("returned outcome=%s complete=%v", outcome, complete)
	}
	store.tasks[0].Status = "rejected"
	if outcome, complete, _ := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); outcome != "rejected" || !complete {
		t.Fatalf("rejected outcome=%s complete=%v", outcome, complete)
	}
	store.tasks = nil
	if _, _, err := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); apperror.CodeOf(err) != "backend.workflow.approval_tasks_missing" {
		t.Fatalf("missing tasks=%v", err)
	}
	store.tasks = []workflowmodel.WorkflowTask{{ID: "unrelated", NodeID: "other", Status: "approved"}, {ID: "open", NodeID: "approval", Status: "open"}}
	anyProcess := approvalProcess
	anyProcess.DefinitionSnapshot.Graph = &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "any"}}}}}
	if outcome, complete, err := engine.aggregateApproval(t.Context(), anyProcess, "approval", principal); err != nil || outcome != "" || complete {
		t.Fatalf("unapproved outcome=%s complete=%v err=%v", outcome, complete, err)
	}
}

func TestWorkflowApprovalFailureRoleCompletionAndPreparationOutcomes(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	process := workflowEngineProcess(nil, nil)
	baseIdentity := workflowApprovalIdentityStub{users: map[string]identitysdk.User{}, assignments: map[string][]identitysdk.UserRoleAssignment{}}
	store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	engine := workflowEngineEdge(store, baseIdentity, nil)
	managerResolver := definitionmodel.WorkflowAssigneeResolver{Type: "manager", UserField: "employee"}
	if users, _, err := engine.resolveApprovalAssigneeStrategy(t.Context(), process, managerResolver, principal); err != nil || len(users) != 0 {
		t.Fatalf("missing employee users=%v err=%v", users, err)
	}
	employeeLookupError := baseIdentity
	employeeLookupError.findErr = map[string]error{"employee": errors.New("employee")}
	if _, _, err := workflowEngineEdge(store, employeeLookupError, nil).resolveApprovalAssigneeStrategy(t.Context(), process, managerResolver, principal); err == nil {
		t.Fatal("expected employee lookup error")
	}
	managerIdentity := baseIdentity
	managerIdentity.users = map[string]identitysdk.User{"employee": {ID: "employee", ManagerUserID: "manager", Status: identitysdk.UserStatusActive}}
	if users, _, err := workflowEngineEdge(store, managerIdentity, nil).resolveApprovalAssigneeStrategy(t.Context(), process, managerResolver, principal); err != nil || len(users) != 0 {
		t.Fatalf("missing manager users=%v err=%v", users, err)
	}
	managerIdentity.findErr = map[string]error{"manager": errors.New("manager")}
	if _, _, err := workflowEngineEdge(store, managerIdentity, nil).resolveApprovalAssigneeStrategy(t.Context(), process, managerResolver, principal); err == nil {
		t.Fatal("expected manager lookup error")
	}
	managerIdentity.findErr = nil
	managerIdentity.users["manager"] = identitysdk.User{ID: "manager", Status: "disabled"}
	if users, _, err := workflowEngineEdge(store, managerIdentity, nil).resolveApprovalAssigneeStrategy(t.Context(), process, managerResolver, principal); err != nil || len(users) != 0 {
		t.Fatalf("disabled manager users=%v err=%v", users, err)
	}
	emptyRecord := process
	emptyRecord.Variables = map[string]any{}
	if users, _, err := engine.resolveApprovalAssigneeStrategy(t.Context(), emptyRecord, definitionmodel.WorkflowAssigneeResolver{Type: "record_field", Field: "owner"}, principal); err != nil || len(users) != 0 {
		t.Fatalf("empty record users=%v err=%v", users, err)
	}
	for name, identity := range map[string]workflowApprovalIdentityStub{
		"roles":        {rolesErr: errors.New("roles")},
		"missing role": {roles: []identitysdk.Role{}},
		"users":        {roles: []identitysdk.Role{{ID: "role", Key: "approver"}}, usersErr: errors.New("users")},
		"assignments":  {roles: []identitysdk.Role{{ID: "role", Key: "approver"}}, directoryUsers: []identitysdk.User{{ID: "user", Status: identitysdk.UserStatusActive}}, assignmentErr: errors.New("assignments")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := workflowEngineEdge(store, identity, nil).usersForApprovalRole(t.Context(), "approver"); err == nil {
				t.Fatal("expected role resolution error")
			}
		})
	}
	roleByID := workflowApprovalIdentityStub{roles: []identitysdk.Role{{ID: "irrelevant", Key: "irrelevant"}, {ID: "role-id", Key: "approver"}}, directoryUsers: []identitysdk.User{{ID: "matched", Status: identitysdk.UserStatusActive}, {ID: "unmatched", Status: identitysdk.UserStatusActive}}, assignments: map[string][]identitysdk.UserRoleAssignment{"matched": {{RoleID: "role-id"}}, "unmatched": {{RoleID: "other"}}}}
	if users, err := workflowEngineEdge(store, roleByID, nil).usersForApprovalRole(t.Context(), "role-id"); err != nil || len(users) != 1 {
		t.Fatalf("role by id users=%v err=%v", users, err)
	}
	adminIdentity := workflowApprovalIdentityStub{roles: []identitysdk.Role{{ID: "admin-role", Key: "admin"}}, directoryUsers: []identitysdk.User{{ID: "admin", Status: identitysdk.UserStatusActive}}, assignments: map[string][]identitysdk.UserRoleAssignment{"admin": {{RoleID: "admin-role"}}}}
	adminNode := definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{EmptyAssigneePolicy: "admin"}}}
	if users, role, err := workflowEngineEdge(store, adminIdentity, nil).resolveApprovalAssignees(t.Context(), process, adminNode, principal); err != nil || role != "admin" || len(users) != 1 {
		t.Fatalf("admin fallback users=%v role=%s err=%v", users, role, err)
	}

	approvalNode := definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "any"}}}
	approvalProcess := workflowEngineProcess([]definitionmodel.WorkflowGraphNode{approvalNode}, nil)
	store.listTasksErr = errors.New("tasks")
	if _, _, err := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("list tasks=%v", err)
	}
	store.listTasksErr = nil
	store.tasks = []workflowmodel.WorkflowTask{{ID: "approved", NodeID: "approval", Status: "approved"}, {ID: "open", NodeID: "approval", Status: "open", WorkspaceID: "workspace", ProcessID: "process"}}
	if outcome, complete, err := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); err != nil || outcome != "approved" || !complete {
		t.Fatalf("any outcome=%s complete=%v err=%v", outcome, complete, err)
	}
	approvalNode.Contract.Approval.Mode = "all"
	approvalProcess.DefinitionSnapshot.Graph.Nodes[0] = approvalNode
	if outcome, complete, err := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); err != nil || outcome != "approved" || complete {
		t.Fatalf("all outcome=%s complete=%v err=%v", outcome, complete, err)
	}
	store.tasks[1].Status = "approved"
	if _, complete, _ := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); !complete {
		t.Fatal("all approval should complete")
	}
	approvalNode.Contract.Approval.Mode = "sequential"
	approvalProcess.DefinitionSnapshot.Graph.Nodes[0] = approvalNode
	store.tasks = []workflowmodel.WorkflowTask{{ID: "approved", NodeID: "approval", Status: "approved", Sequence: 1}, {ID: "pending", NodeID: "approval", Status: "pending", Sequence: 2}}
	store.updateTaskErr = errors.New("open")
	if _, _, err := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("sequential update=%v", err)
	}
	store.updateTaskErr = nil
	if outcome, complete, err := engine.aggregateApproval(t.Context(), approvalProcess, "approval", principal); err != nil || outcome != "" || complete {
		t.Fatalf("sequential open outcome=%s complete=%v err=%v", outcome, complete, err)
	}
	engine.cancelUnfinishedApprovalTasks(t.Context(), []workflowmodel.WorkflowTask{{ID: "pending", Status: "pending", WorkspaceID: "workspace", ProcessID: "process"}, {ID: "done", Status: "done"}}, "", principal.UserID)

	store.listNodesErr = errors.New("nodes")
	if err := engine.completeApprovalNode(t.Context(), "workspace", "process", "approval", "approved", principal.UserID); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("list nodes=%v", err)
	}
	store.listNodesErr = nil
	store.nodes["process"] = []workflowmodel.WorkflowNodeInstance{{NodeID: "other", Status: "waiting"}}
	if err := engine.completeApprovalNode(t.Context(), "workspace", "process", "approval", "approved", principal.UserID); apperror.CodeOf(err) != "backend.workflow.approval_node_instance_not_found" {
		t.Fatalf("missing node=%v", err)
	}
	store.nodes["process"] = []workflowmodel.WorkflowNodeInstance{{NodeID: "approval", Status: "completed"}}
	if err := engine.completeApprovalNode(t.Context(), "workspace", "process", "approval", "approved", principal.UserID); apperror.CodeOf(err) != "backend.workflow.approval_node_instance_not_found" {
		t.Fatalf("completed node=%v", err)
	}
	store.nodes["process"] = append(store.nodes["process"], workflowmodel.WorkflowNodeInstance{NodeID: "approval", Status: "waiting"})
	store.updateNodeErr = errors.New("node")
	if err := engine.completeApprovalNode(t.Context(), "workspace", "process", "approval", "approved", principal.UserID); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update node=%v", err)
	}
	store.updateNodeErr = nil
	if err := engine.completeApprovalNode(t.Context(), "workspace", "process", "approval", "approved", principal.UserID); err != nil {
		t.Fatal(err)
	}

	invalidProcess := workflowEngineProcess([]definitionmodel.WorkflowGraphNode{{ID: "condition", Type: "condition"}}, nil)
	commit := transactionmodel.WorkflowDecisionCommit{}
	if prepared, err := prepareNextApprovalNodes(t.Context(), engine.runtime, &commit, invalidProcess, []string{"condition"}, principal, "now"); err != nil || prepared {
		t.Fatalf("invalid preparation prepared=%v err=%v", prepared, err)
	}
	if prepared, err := prepareNextApprovalNodes(t.Context(), engine.runtime, &commit, invalidProcess, []string{"missing"}, principal, "now"); err != nil || prepared {
		t.Fatalf("missing preparation prepared=%v err=%v", prepared, err)
	}
	approvalProcess = workflowEngineProcess([]definitionmodel.WorkflowGraphNode{{ID: "approval", Type: "approval", Name: "Approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"user"}}}}}}}, nil)
	commit = transactionmodel.WorkflowDecisionCommit{}
	if prepared, err := prepareNextApprovalNodes(t.Context(), workflowEngineEdge(store, baseIdentity, nil).runtime, &commit, approvalProcess, []string{"approval"}, principal, "now"); err != nil || !prepared || len(commit.InsertNodes) != 1 || len(commit.InsertTasks) != 1 {
		t.Fatalf("prepared=%v commit=%+v err=%v", prepared, commit, err)
	}
	sequentialApproval := approvalProcess
	sequentialApproval.DefinitionSnapshot.Graph.Nodes[0].Contract.Approval.Mode = "sequential"
	sequentialApproval.DefinitionSnapshot.Graph.Nodes[0].Contract.Approval.Resolvers[0].UserIDs = []string{"one", "two"}
	commit = transactionmodel.WorkflowDecisionCommit{}
	preparedEngine := workflowEngineEdge(store, workflowApprovalIdentityStub{users: map[string]identitysdk.User{"one": {Name: "One"}}}, nil)
	if prepared, err := prepareNextApprovalNodes(t.Context(), preparedEngine.runtime, &commit, sequentialApproval, []string{"approval"}, principal, "now"); err != nil || !prepared || len(commit.InsertTasks) != 2 || commit.InsertTasks[1].Status != "pending" || commit.InsertTasks[0].AssigneeName != "One" {
		t.Fatalf("sequential prepared=%v commit=%+v err=%v", prepared, commit, err)
	}
	errorNameEngine := workflowEngineEdge(store, workflowApprovalIdentityStub{findErr: map[string]error{"one": errors.New("name")}}, nil)
	if prepared, err := prepareNextApprovalNodes(t.Context(), errorNameEngine.runtime, &transactionmodel.WorkflowDecisionCommit{}, sequentialApproval, []string{"approval"}, principal, "now"); err != nil || !prepared {
		t.Fatalf("ignored prepared name error=%v prepared=%v", err, prepared)
	}
	emptyApproval := approvalProcess
	emptyApproval.DefinitionSnapshot.Graph.Nodes[0].Contract.Approval.Resolvers[0].UserIDs = nil
	emptyApproval.DefinitionSnapshot.Graph.Nodes[0].Contract.Approval.EmptyAssigneePolicy = "skip"
	if prepared, err := prepareNextApprovalNodes(t.Context(), preparedEngine.runtime, &transactionmodel.WorkflowDecisionCommit{}, emptyApproval, []string{"approval"}, principal, "now"); err != nil || prepared {
		t.Fatalf("empty skip prepared=%v err=%v", prepared, err)
	}
	emptyApproval.DefinitionSnapshot.Graph.Nodes[0].Contract.Approval.EmptyAssigneePolicy = "fail"
	if _, err := prepareNextApprovalNodes(t.Context(), preparedEngine.runtime, &transactionmodel.WorkflowDecisionCommit{}, emptyApproval, []string{"approval"}, principal, "now"); apperror.CodeOf(err) != "backend.workflow.approval_assignee_not_found" {
		t.Fatalf("empty fail=%v", err)
	}
	badApproval := workflowEngineProcess([]definitionmodel.WorkflowGraphNode{{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "bad"}}}}}}, nil)
	if _, err := prepareNextApprovalNodes(t.Context(), preparedEngine.runtime, &transactionmodel.WorkflowDecisionCommit{}, badApproval, []string{"approval"}, principal, "now"); apperror.CodeOf(err) != "backend.workflow.approval_resolver_invalid" {
		t.Fatalf("resolver error=%v", err)
	}
	_ = workflowDecisionExecution(approvalProcess, principal, "now")
	_ = workflowDecisionEvent(t.Context(), "process", "node", "task", "event", "", "summary", map[string]any{"x": 1}, "now")
}
