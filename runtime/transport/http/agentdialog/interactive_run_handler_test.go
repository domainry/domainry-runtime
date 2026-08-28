package agentdialog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type interactiveExecutorFunc func(context.Context, agentruntime.AgentInteractiveExecutionRequest) (agentruntime.AgentInteractiveExecutionResult, error)

func (fn interactiveExecutorFunc) Execute(ctx context.Context, request agentruntime.AgentInteractiveExecutionRequest) (agentruntime.AgentInteractiveExecutionResult, error) {
	return fn(ctx, request)
}

type interactiveRunReaderFunc func(context.Context, string, principalmodel.Principal) (agentmodel.AgentInteractiveRun, bool, error)

func (fn interactiveRunReaderFunc) Get(ctx context.Context, runID string, principal principalmodel.Principal) (agentmodel.AgentInteractiveRun, bool, error) {
	return fn(ctx, runID, principal)
}

func typedInteractiveHandler(t *testing.T, execute AgentInteractiveExecutor, reader AgentInteractiveRunReader) (*AgentDialogHandler, *string) {
	t.Helper()
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1"}, SurfaceKey: "business_workspace"}, accessfixture.Bundle{Key: "operator"})
	errorCode := ""
	return NewAgentDialogHandler(AgentDialogDependencies{
		Principal: func(*http.Request) principalmodel.Principal { return principal }, Interactive: execute, InteractiveRuns: reader,
		ContextResolver: agentDialogContextResolverFunc(func(context.Context, agentruntime.GlobalAgentContextRequest) (agentmodel.GlobalAgentContext, error) {
			return agentmodel.GlobalAgentContext{ContextRevision: "context-1", EntrypointKey: "assistant.global", AgentKey: "customer-agent", Surface: principal.SurfaceKey, RouteKey: "workspace.customer", Principal: agentmodel.AgentPrincipalReference{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey}}, nil
		}),
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, target any) bool {
			return json.NewDecoder(r.Body).Decode(target) == nil
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			errorCode = code
			w.WriteHeader(status)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusUnprocessableEntity) },
	}), &errorCode
}

func TestAgentDialogBlockingRunUsesTypedInteractiveLifecycle(t *testing.T) {
	var received agentruntime.AgentInteractiveExecutionRequest
	handler, _ := typedInteractiveHandler(t, interactiveExecutorFunc(func(_ context.Context, request agentruntime.AgentInteractiveExecutionRequest) (agentruntime.AgentInteractiveExecutionResult, error) {
		received = request
		return agentruntime.AgentInteractiveExecutionResult{Run: agentmodel.AgentInteractiveRun{ID: "interactive_run_1", Status: agentmodel.AgentInteractiveRunCompleted}, Result: agentruntime.InteractiveAgentResult{RunID: "interactive_run_1", Status: "completed", Message: "done"}}, nil
	}), nil)
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/runs", strings.NewReader(`{"message":"review","external_session_id":"session-1","idempotency_key":"message-1","context":{"record_id":"forged"}}`))
	response := httptest.NewRecorder()
	handler.agentDialogRun(response, request)
	if response.Code != http.StatusOK || received.IdempotencyKey != "message-1" || received.SessionID != "session-1" || received.Context.ContextRevision != "context-1" || !strings.Contains(response.Body.String(), "interactive_run_1") {
		t.Fatalf("response=%d %s request=%#v", response.Code, response.Body.String(), received)
	}
}

func TestAgentDialogTypedInteractiveRequiresIdempotencyAndReadsLocalStatus(t *testing.T) {
	handler, code := typedInteractiveHandler(t, interactiveExecutorFunc(func(context.Context, agentruntime.AgentInteractiveExecutionRequest) (agentruntime.AgentInteractiveExecutionResult, error) {
		t.Fatal("executor must not run without idempotency key")
		return agentruntime.AgentInteractiveExecutionResult{}, nil
	}), interactiveRunReaderFunc(func(_ context.Context, runID string, _ principalmodel.Principal) (agentmodel.AgentInteractiveRun, bool, error) {
		return agentmodel.AgentInteractiveRun{ID: runID, Status: agentmodel.AgentInteractiveRunHandedOff, TaskRunID: "task-1"}, true, nil
	}))
	response := httptest.NewRecorder()
	handler.agentDialogRun(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/runs", strings.NewReader(`{"message":"review"}`)))
	if response.Code != http.StatusBadRequest || *code != "agent.interactive.idempotency_required" {
		t.Fatalf("status=%d code=%q", response.Code, *code)
	}
	statusRequest := httptest.NewRequest(http.MethodGet, "/agent-dialog/runs/interactive_run_1", nil)
	statusRequest.SetPathValue("runID", "interactive_run_1")
	status := httptest.NewRecorder()
	handler.agentDialogRunStatus(status, statusRequest)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"task_run_id":"task-1"`) {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
}

func TestAgentDialogStreamUsesSameTypedInteractiveLifecycleAndReturnsHandoffEvent(t *testing.T) {
	handler, _ := typedInteractiveHandler(t, interactiveExecutorFunc(func(_ context.Context, request agentruntime.AgentInteractiveExecutionRequest) (agentruntime.AgentInteractiveExecutionResult, error) {
		if request.IdempotencyKey != "stream-1" {
			t.Fatalf("request=%#v", request)
		}
		return agentruntime.AgentInteractiveExecutionResult{Run: agentmodel.AgentInteractiveRun{ID: "interactive_run_stream", Status: agentmodel.AgentInteractiveRunHandedOff, TaskRunID: "task-1"}, Result: agentruntime.InteractiveAgentResult{Status: "handed_off", Handoff: &agentmodel.InteractiveAgentHandoff{ContractVersion: agentmodel.InteractiveHandoffContractVersion, RouteType: agentmodel.AgentRouteTask, TargetKey: "customer.review", IdempotencyKey: "handoff-1", TaskRunID: "task-1"}}}, nil
	}), nil)
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/runs/stream", strings.NewReader(`{"message":"review","idempotency_key":"stream-1"}`))
	response := httptest.NewRecorder()
	handler.agentDialogRunStream(response, request)
	acceptedID := agentInteractiveStreamEventID("workspace-1", "user-1", "stream-1", "accepted")
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(response.Body.String(), "id: "+acceptedID) || !strings.Contains(response.Body.String(), "event: accepted") || !strings.Contains(response.Body.String(), "event: result") || !strings.Contains(response.Body.String(), `"task_run_id":"task-1"`) {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	resumeRequest := httptest.NewRequest(http.MethodPost, "/agent-dialog/runs/stream", strings.NewReader(`{"message":"review","idempotency_key":"stream-1"}`))
	resumeRequest.Header.Set("Last-Event-ID", acceptedID)
	resumed := httptest.NewRecorder()
	handler.agentDialogRunStream(resumed, resumeRequest)
	if resumed.Code != http.StatusOK || strings.Contains(resumed.Body.String(), "event: accepted") || !strings.Contains(resumed.Body.String(), "event: result") {
		t.Fatalf("resumed status=%d body=%s", resumed.Code, resumed.Body.String())
	}
	invalidRequest := httptest.NewRequest(http.MethodPost, "/agent-dialog/runs/stream", strings.NewReader(`{"message":"review","idempotency_key":"stream-1"}`))
	invalidRequest.Header.Set("Last-Event-ID", "forged")
	invalid := httptest.NewRecorder()
	handler.agentDialogRunStream(invalid, invalidRequest)
	if invalid.Code != http.StatusConflict {
		t.Fatalf("invalid cursor status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}
