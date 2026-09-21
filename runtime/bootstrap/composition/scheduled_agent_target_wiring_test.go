package composition

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	dispatchmodel "github.com/domainry/domainry-runtime/runtime/domain/dispatch/model"
	dispatchhttp "github.com/domainry/domainry-runtime/runtime/transport/http/dispatch"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	schedulergateway "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

type scheduledPrincipalResolver struct {
	resolution identitysdk.PrincipalResolution
	request    identitysdk.PrincipalResolutionRequest
	calls      int
}

func (r *scheduledPrincipalResolver) Resolve(_ context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	r.calls++
	r.request = request
	return r.resolution, nil
}

type scheduledTaskStarter struct {
	request    agentsdk.ScheduledConversationTaskRequest
	authorized bool
	calls      int
	err        error
}

type scheduledCallbackReceiptStore struct {
	receipt dispatchmodel.CallbackReceipt
	done    bool
}

func (s *scheduledCallbackReceiptStore) TryBeginCallback(_ context.Context, request dispatchmodel.CallbackClaimRequest) (dispatchmodel.CallbackClaimResult, error) {
	if s.done {
		return dispatchmodel.CallbackClaimResult{Decision: idempotency.DecisionReplay, Receipt: s.receipt}, nil
	}
	s.receipt = request.Receipt
	s.receipt.ID = "callback-receipt"
	s.receipt.LeaseOwner = request.LeaseOwner
	s.receipt.FencingToken = 1
	return dispatchmodel.CallbackClaimResult{Decision: idempotency.DecisionAcquired, Receipt: s.receipt}, nil
}

func (*scheduledCallbackReceiptStore) HeartbeatCallback(context.Context, dispatchmodel.CallbackHeartbeat) (bool, error) {
	return true, nil
}

func (s *scheduledCallbackReceiptStore) CompleteCallback(_ context.Context, completion dispatchmodel.CallbackCompletion) error {
	s.receipt.DownstreamID = completion.DownstreamID
	s.receipt.DownstreamOwner = completion.DownstreamOwner
	s.receipt.DownstreamStatus = completion.DownstreamStatus
	s.done = true
	return nil
}

func (*scheduledCallbackReceiptStore) FailCallbackRetryable(context.Context, dispatchmodel.CallbackFailure) error {
	return nil
}

func (s *scheduledTaskStarter) StartScheduledConversationTask(ctx context.Context, request agentsdk.ScheduledConversationTaskRequest) (agentsdk.ScheduledConversationTaskReceipt, error) {
	s.calls++
	s.request = request
	s.authorized = agentsdk.HasAuthorizedServiceAction(ctx, agentsdk.ActionAgentScheduledConversationTaskStart, agentsdk.AgentRuntimeServiceAudience)
	if s.err != nil {
		return agentsdk.ScheduledConversationTaskReceipt{}, s.err
	}
	return agentsdk.ScheduledConversationTaskReceipt{Task: agentsdk.ConversationTask{ID: "agent-task", Status: agentsdk.ConversationTaskStatusQueued}}, nil
}

func scheduledAgentPayload(t *testing.T, owner schedulersdk.ScheduledPlanOwner) []byte {
	t.Helper()
	raw, err := json.Marshal(schedulersdk.ScheduledPlanDispatch{
		ContractVersion: schedulersdk.ScheduledPlanDispatchContractVersion,
		PlanID:          "plan-weekly", Owner: owner,
		Input:           json.RawMessage(`{"goal":"整理本周待办","include_status":"open","allowed_tools":["todo_list"]}`),
		AllowedActions:  []string{"agent.conversation_tools.todo_list"},
		ConversationRef: schedulersdk.ScheduledPlanConversationRef{ConversationID: "conversation-1", RunID: "source-run-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScheduledAgentTargetResolvesCurrentIdentityAndMapsOnlyPublicContracts(t *testing.T) {
	owner := schedulersdk.ScheduledPlanOwner{WorkspaceID: "workspace", UserID: "user", ProductKey: "product"}
	resolver := &scheduledPrincipalResolver{resolution: identitysdk.PrincipalResolution{
		Principal:    identitysdk.Principal{Known: true, WorkspaceID: owner.WorkspaceID, UserID: owner.UserID, User: identitysdk.User{ID: owner.UserID}},
		AccessBundle: identitysdk.AccessBundle{Subject: identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(owner.WorkspaceID), SubjectID: identitysdk.SubjectID(owner.UserID)}},
	}}
	starter := &scheduledTaskStarter{}
	runtime := &runtimeAssembly{templateID: owner.ProductKey}
	adapter := scheduledAgentTargetRuntimeAdapter{runtime: runtime, principals: resolver, tasks: starter}
	dueAt := time.Date(2026, time.September, 14, 9, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	receipt, err := adapter.ExecuteAgentTarget(t.Context(), dispatchapplication.AgentTargetRequest{
		ExecutionID: "scheduler-run-1", IdempotencyKey: "window-1", Operation: "conversation_task_start", ScheduledFor: dueAt,
		Payload: scheduledAgentPayload(t, owner),
	})
	if err != nil || receipt.ID != "agent-task" || receipt.Status != "accepted" || resolver.calls != 1 || starter.calls != 1 || !starter.authorized {
		t.Fatalf("receipt=%+v resolver=%d starter=%d authorized=%t err=%v", receipt, resolver.calls, starter.calls, starter.authorized, err)
	}
	request := starter.request
	if request.ContractVersion != agentsdk.ScheduledConversationTaskContractVersion || request.PlanID != "plan-weekly" || request.SchedulerRunID != "scheduler-run-1" || request.IdempotencyKey != "window-1" || !request.ScheduledFor.Equal(dueAt) || request.ConversationID != "conversation-1" || request.SourceRunID != "source-run-1" || request.Authority.RuntimeID != "" || request.Authority.RoleKey != "" || request.Authority.WorkspaceID != owner.WorkspaceID || request.Authority.UserID != owner.UserID || len(request.AllowedActions) != 1 || request.Input.AllowedTools[0] != "todo_list" || request.Input.Input != `{"goal":"整理本周待办","include_status":"open","allowed_tools":["todo_list"]}` {
		t.Fatalf("mapped request=%+v", request)
	}
	if resolver.request.SubjectID != identitysdk.SubjectID(owner.UserID) || resolver.request.RoleKey != "" || resolver.request.Workload != nil {
		t.Fatalf("principal request reused authored authority: %+v", resolver.request)
	}
	if _, found := reflect.TypeOf(agentsdk.ScheduledConversationTaskRequest{}).FieldByName("AccessToken"); found {
		t.Fatal("scheduled request contains a browser access token")
	}
}

func TestScheduledAgentTargetPreservesExplicitFollowUpScope(t *testing.T) {
	start, err := scheduledAgentConversationTaskStart(json.RawMessage(`{"goal":"跟进发布","allowed_tools":["todo_list"],"follow_up":{"completion_condition":"所有阻塞项完成"}}`))
	if err != nil || start.FollowUp == nil || start.FollowUp.CompletionCondition != "所有阻塞项完成" || start.Input != `{"goal":"跟进发布","allowed_tools":["todo_list"],"follow_up":{"completion_condition":"所有阻塞项完成"}}` {
		t.Fatalf("start=%+v err=%v", start, err)
	}
	if _, err = scheduledAgentConversationTaskStart(json.RawMessage(`{"goal":"跟进发布","follow_up":{"completion_condition":1}}`)); err == nil {
		t.Fatal("invalid follow-up scope was accepted")
	}
	if _, err = scheduledAgentConversationTaskStart(json.RawMessage(`{"goal":"跟进发布","follow_up":{"completion_condition":"完成","recipient":"other-user"}}`)); err == nil {
		t.Fatal("unknown follow-up authority field was accepted")
	}
}

func TestScheduledAgentTargetRejectsStaleOwnerBeforeAgent(t *testing.T) {
	owner := schedulersdk.ScheduledPlanOwner{WorkspaceID: "workspace", UserID: "user", ProductKey: "product"}
	valid := identitysdk.PrincipalResolution{
		Principal:    identitysdk.Principal{Known: true, WorkspaceID: owner.WorkspaceID, UserID: owner.UserID, User: identitysdk.User{ID: owner.UserID}},
		AccessBundle: identitysdk.AccessBundle{Subject: identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(owner.WorkspaceID), SubjectID: identitysdk.SubjectID(owner.UserID)}},
	}
	tests := []struct {
		name       string
		productKey string
		resolution identitysdk.PrincipalResolution
		want       string
	}{
		{name: "wrong product", productKey: "other", resolution: valid, want: "backend.dispatch.scheduled_agent_product_denied"},
		{name: "disabled user", productKey: owner.ProductKey, resolution: identitysdk.PrincipalResolution{}, want: "backend.dispatch.scheduled_agent_principal_denied"},
		{name: "moved workspace", productKey: owner.ProductKey, resolution: identitysdk.PrincipalResolution{Principal: identitysdk.Principal{Known: true, WorkspaceID: "other", UserID: owner.UserID, User: identitysdk.User{ID: owner.UserID}}, AccessBundle: valid.AccessBundle}, want: "backend.dispatch.scheduled_agent_principal_denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &scheduledPrincipalResolver{resolution: test.resolution}
			starter := &scheduledTaskStarter{}
			adapter := scheduledAgentTargetRuntimeAdapter{runtime: &runtimeAssembly{templateID: test.productKey}, principals: resolver, tasks: starter}
			_, err := adapter.ExecuteAgentTarget(t.Context(), dispatchapplication.AgentTargetRequest{ExecutionID: "run", IdempotencyKey: "window", Operation: "conversation_task_start", ScheduledFor: time.Now(), Payload: scheduledAgentPayload(t, owner)})
			if apperror.CodeOf(err) != test.want || starter.calls != 0 {
				t.Fatalf("code=%s calls=%d err=%v", apperror.CodeOf(err), starter.calls, err)
			}
		})
	}
}

func TestScheduledAgentTargetPropagatesCurrentAgentDenial(t *testing.T) {
	owner := schedulersdk.ScheduledPlanOwner{WorkspaceID: "workspace", UserID: "user", ProductKey: "product"}
	resolver := &scheduledPrincipalResolver{resolution: identitysdk.PrincipalResolution{
		Principal:    identitysdk.Principal{Known: true, WorkspaceID: owner.WorkspaceID, UserID: owner.UserID, User: identitysdk.User{ID: owner.UserID}},
		AccessBundle: identitysdk.AccessBundle{Subject: identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(owner.WorkspaceID), SubjectID: identitysdk.SubjectID(owner.UserID)}},
	}}
	denied := &agentsdk.Error{Class: "forbidden", Code: "agent.conversation.tool_access_denied"}
	starter := &scheduledTaskStarter{err: denied}
	adapter := scheduledAgentTargetRuntimeAdapter{runtime: &runtimeAssembly{templateID: owner.ProductKey}, principals: resolver, tasks: starter}
	_, err := adapter.ExecuteAgentTarget(t.Context(), dispatchapplication.AgentTargetRequest{ExecutionID: "run", IdempotencyKey: "window", Operation: "conversation_task_start", ScheduledFor: time.Now(), Payload: scheduledAgentPayload(t, owner)})
	if err != denied || starter.calls != 1 || !starter.authorized {
		t.Fatalf("denial=%v calls=%d authorized=%t", err, starter.calls, starter.authorized)
	}
}

func TestSignedSchedulerCallbackReauthorizesAndStartsAgentTask(t *testing.T) {
	now := time.Date(2026, time.September, 14, 1, 0, 0, 0, time.UTC)
	owner := schedulersdk.ScheduledPlanOwner{WorkspaceID: "workspace", UserID: "user", ProductKey: "product"}
	resolver := &scheduledPrincipalResolver{resolution: identitysdk.PrincipalResolution{
		Principal:    identitysdk.Principal{Known: true, WorkspaceID: owner.WorkspaceID, UserID: owner.UserID, User: identitysdk.User{ID: owner.UserID}},
		AccessBundle: identitysdk.AccessBundle{Subject: identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(owner.WorkspaceID), SubjectID: identitysdk.SubjectID(owner.UserID)}},
	}}
	starter := &scheduledTaskStarter{}
	executions := dispatchapplication.NewTargetExecutionApplicationService(nil)
	executions.UseAgentTargetRuntime(scheduledAgentTargetRuntimeAdapter{runtime: &runtimeAssembly{templateID: owner.ProductKey}, principals: resolver, tasks: starter})
	dispatcher := NewTargetExecutionDispatcher(executions, nil, nil)
	store := &scheduledCallbackReceiptStore{}
	secret := []byte("runtime-signing-secret")
	handler := dispatchhttp.NewExecutionHandler(dispatchhttp.TargetExecutionDependencies{
		Executor: dispatcher, Receipts: store, RuntimeID: "runtime-a", SigningSecret: secret, Now: func() time.Time { return now },
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	body, err := json.Marshal(map[string]any{
		"runtime_id": "runtime-a", "execution_id": "scheduler-run-1", "definition_key": "scheduled-plan:weekly", "idempotency_key": "plan-weekly:2026-09-14T01:00:00Z", "due_at": now,
		"target": map[string]any{"type": "runtime_operation", "owner": "agent", "operation": "conversation_task_start", "payload": json.RawMessage(scheduledAgentPayload(t, owner))},
	})
	if err != nil {
		t.Fatal(err)
	}
	send := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, dispatchhttp.RuntimeExecutionPath, bytes.NewReader(body))
		request.Header.Set(schedulergateway.RuntimeIDHeader, "runtime-a")
		timestamp := strconv.FormatInt(now.Unix(), 10)
		signature, signErr := schedulergateway.SignRequest(body, schedulergateway.SignedRequest{
			Method: http.MethodPost, Path: dispatchhttp.RuntimeExecutionPath, RuntimeID: "runtime-a", IdempotencyKey: "plan-weekly:2026-09-14T01:00:00Z",
		}, schedulergateway.SchedulerClientID, timestamp, secret)
		if signErr != nil {
			t.Fatal(signErr)
		}
		request.Header.Set(schedulergateway.SignatureVersionHeader, schedulergateway.CallbackSignatureContractVersion)
		request.Header.Set(schedulergateway.ClientIDHeader, schedulergateway.SchedulerClientID)
		request.Header.Set(schedulergateway.TimestampHeader, timestamp)
		request.Header.Set(schedulergateway.SignatureHeader, signature)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}

	first := send()
	if first.Code != http.StatusOK || resolver.calls != 1 || starter.calls != 1 || !starter.authorized || !store.done {
		t.Fatalf("status=%d body=%s resolver=%d starter=%d authorized=%t completed=%t", first.Code, first.Body.String(), resolver.calls, starter.calls, starter.authorized, store.done)
	}
	var receipt struct {
		ID     string `json:"id"`
		Owner  string `json:"owner"`
		Status string `json:"status"`
		Replay bool   `json:"replay"`
	}
	if json.Unmarshal(first.Body.Bytes(), &receipt) != nil || receipt.ID != "agent-task" || receipt.Owner != "agent" || receipt.Status != "accepted" || receipt.Replay {
		t.Fatalf("first receipt=%+v", receipt)
	}
	second := send()
	if json.Unmarshal(second.Body.Bytes(), &receipt) != nil || second.Code != http.StatusOK || !receipt.Replay || resolver.calls != 1 || starter.calls != 1 {
		t.Fatalf("replay status=%d receipt=%+v resolver=%d starter=%d", second.Code, receipt, resolver.calls, starter.calls)
	}
}
