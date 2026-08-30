package agentdialog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type agentTaskHTTPRuns struct {
	runs       []agentmodel.AgentTaskRun
	run        agentmodel.AgentTaskRun
	found      bool
	listErr    error
	getErr     error
	operateErr error
}

func (f *agentTaskHTTPRuns) List(context.Context, string, agentrepository.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	return f.runs, f.listErr
}
func (f *agentTaskHTTPRuns) Get(context.Context, string, string) (agentmodel.AgentTaskRun, bool, error) {
	return f.run, f.found, f.getErr
}
func (f *agentTaskHTTPRuns) RequestCancel(context.Context, string, string, string) (agentmodel.AgentTaskRun, bool, error) {
	return f.run, false, f.operateErr
}
func (f *agentTaskHTTPRuns) Operate(context.Context, string, string, string, string, string, principalmodel.Principal) (agentmodel.AgentTaskRun, bool, error) {
	return f.run, false, f.operateErr
}

type agentTaskHTTPTools struct {
	result agentruntime.AgentToolInvocationResult
	err    error
}

func (f agentTaskHTTPTools) Invoke(context.Context, agentruntime.AgentToolInvocationRequest) (agentruntime.AgentToolInvocationResult, error) {
	return f.result, f.err
}

func agentTaskHTTPHandler(principal principalmodel.Principal, runs agentTaskRunService, tools agentTaskToolService) *AgentDialogHandler {
	return &AgentDialogHandler{
		principal: func(*http.Request) principalmodel.Principal { return principal },
		taskRuns:  runs,
		taskTools: tools,
		writeJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		writeError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			w.Header().Set("X-Code", code)
			w.WriteHeader(status)
		},
		writeServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			w.Header().Set("X-Service-Error", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
		},
		decodeJSON: func(w http.ResponseWriter, r *http.Request, value any) bool {
			if err := json.NewDecoder(r.Body).Decode(value); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
		securityAudit: func(*http.Request, string, string, map[string]any) {},
	}
}

func taskPrincipal(permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "default"}}, accessfixture.Bundle{Key: "role", Permissions: permissions})
}

func TestAgentTaskListAndGetHTTPBranches(t *testing.T) {
	run := agentmodel.AgentTaskRun{ID: "run", WorkspaceID: "default", Status: agentmodel.AgentTaskRunPending}
	wantErr := errors.New("repository failure")
	tests := []struct {
		name   string
		method func(*AgentDialogHandler, http.ResponseWriter, *http.Request)
		h      *AgentDialogHandler
		url    string
		status int
	}{
		{"list permission", (*AgentDialogHandler).listAgentTaskRuns, agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{}, nil), "/operations/agent/tasks", http.StatusForbidden},
		{"list unavailable", (*AgentDialogHandler).listAgentTaskRuns, agentTaskHTTPHandler(taskPrincipal("agent.task.read"), nil, nil), "/operations/agent/tasks", http.StatusServiceUnavailable},
		{"list error", (*AgentDialogHandler).listAgentTaskRuns, agentTaskHTTPHandler(taskPrincipal("agent.task.read"), &agentTaskHTTPRuns{listErr: wantErr}, nil), "/operations/agent/tasks", http.StatusInternalServerError},
		{"list success", (*AgentDialogHandler).listAgentTaskRuns, agentTaskHTTPHandler(taskPrincipal("agent.task.read"), &agentTaskHTTPRuns{runs: []agentmodel.AgentTaskRun{run}}, nil), "/operations/agent/tasks?status=&status=pending&limit=1&process_id=p&task_key=t", http.StatusOK},
		{"get permission", (*AgentDialogHandler).getAgentTaskRun, agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{}, nil), "/operations/agent/tasks/run", http.StatusForbidden},
		{"get unavailable", (*AgentDialogHandler).getAgentTaskRun, agentTaskHTTPHandler(taskPrincipal("agent.task.read"), nil, nil), "/operations/agent/tasks/run", http.StatusServiceUnavailable},
		{"get error", (*AgentDialogHandler).getAgentTaskRun, agentTaskHTTPHandler(taskPrincipal("agent.task.read"), &agentTaskHTTPRuns{getErr: wantErr}, nil), "/operations/agent/tasks/run", http.StatusInternalServerError},
		{"get missing", (*AgentDialogHandler).getAgentTaskRun, agentTaskHTTPHandler(taskPrincipal("agent.task.read"), &agentTaskHTTPRuns{}, nil), "/operations/agent/tasks/run", http.StatusNotFound},
		{"get success", (*AgentDialogHandler).getAgentTaskRun, agentTaskHTTPHandler(taskPrincipal("agent.task.read"), &agentTaskHTTPRuns{run: run, found: true}, nil), "/operations/agent/tasks/run", http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.url, nil)
			req.SetPathValue("taskRunID", "run")
			response := httptest.NewRecorder()
			test.method(test.h, response, req)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAgentTaskOperationHTTPBranches(t *testing.T) {
	run := agentmodel.AgentTaskRun{ID: "run", WorkspaceID: "default", Status: agentmodel.AgentTaskRunFailed}
	wantErr := errors.New("operation failure")
	test := func(name string, handler *AgentDialogHandler, header, body string, call func(*AgentDialogHandler, http.ResponseWriter, *http.Request), want int) {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/operations/agent/tasks/run", strings.NewReader(body))
			req.SetPathValue("taskRunID", "run")
			req.Header.Set("Idempotency-Key", header)
			response := httptest.NewRecorder()
			call(handler, response, req)
			if response.Code != want {
				t.Fatalf("status=%d code=%s", response.Code, response.Header().Get("X-Code"))
			}
		})
	}
	test("permission", agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{}, nil), "key", `{"reason":"why"}`, (*AgentDialogHandler).retryAgentTaskRun, http.StatusForbidden)
	test("unavailable", agentTaskHTTPHandler(taskPrincipal("agent.task.operate"), nil, nil), "key", `{"reason":"why"}`, (*AgentDialogHandler).retryAgentTaskRun, http.StatusServiceUnavailable)
	test("key", agentTaskHTTPHandler(taskPrincipal("agent.task.operate"), &agentTaskHTTPRuns{}, nil), "", `{"reason":"why"}`, (*AgentDialogHandler).retryAgentTaskRun, http.StatusBadRequest)
	test("decode", agentTaskHTTPHandler(taskPrincipal("agent.task.operate"), &agentTaskHTTPRuns{}, nil), "key", `{`, (*AgentDialogHandler).retryAgentTaskRun, http.StatusBadRequest)
	test("reason", agentTaskHTTPHandler(taskPrincipal("agent.task.operate"), &agentTaskHTTPRuns{}, nil), "key", `{}`, (*AgentDialogHandler).retryAgentTaskRun, http.StatusBadRequest)
	test("empty body", agentTaskHTTPHandler(taskPrincipal("agent.task.operate"), &agentTaskHTTPRuns{}, nil), "key", "", (*AgentDialogHandler).retryAgentTaskRun, http.StatusBadRequest)
	test("service error", agentTaskHTTPHandler(taskPrincipal("agent.task.operate"), &agentTaskHTTPRuns{run: run, operateErr: wantErr}, nil), "key", `{"reason":"why"}`, (*AgentDialogHandler).resolveAgentTaskRun, http.StatusInternalServerError)
	h := agentTaskHTTPHandler(taskPrincipal("agent.task.operate"), &agentTaskHTTPRuns{run: run}, nil)
	test("retry", h, "key", `{"reason":"why"}`, (*AgentDialogHandler).retryAgentTaskRun, http.StatusOK)
	test("resolve", h, "key", `{"reason":"why"}`, (*AgentDialogHandler).resolveAgentTaskRun, http.StatusOK)
	test("reconcile", h, "key", `{"reason":"why"}`, (*AgentDialogHandler).reconcileAgentTaskRun, http.StatusOK)
	test("cancel", h, "key", `{"reason":"why"}`, (*AgentDialogHandler).cancelAgentTaskRun, http.StatusOK)
}

func TestAgentTaskToolInvokeHTTPBranches(t *testing.T) {
	now := time.Now().UTC()
	valid := agentmodel.AgentTaskRun{ID: "run", WorkspaceID: "default", ProcessID: "process", TaskKey: "task", TaskVersion: "1", Status: agentmodel.AgentTaskRunRunning, Lease: agentmodel.AgentTaskLease{Owner: "worker", FencingToken: 2, ExpiresAt: now.Add(time.Minute)}, Identity: agentmodel.AgentExecutionIdentity{Initiator: agentmodel.AgentPrincipalReference{UserID: "user", WorkspaceID: "default"}}}
	body := `{"workspace_id":"default","task_run_id":"run","tool":"query_records","input":{},"idempotency_key":"key"}`
	wantErr := errors.New("tool denied")
	tests := []struct {
		name   string
		h      *AgentDialogHandler
		body   string
		status int
	}{
		{"decode", agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{}, agentTaskHTTPTools{}), `{`, http.StatusBadRequest},
		{"unavailable runs", agentTaskHTTPHandler(taskPrincipal(), nil, agentTaskHTTPTools{}), body, http.StatusServiceUnavailable},
		{"unavailable tools", agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{}, nil), body, http.StatusServiceUnavailable},
		{"get error", agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{getErr: wantErr}, agentTaskHTTPTools{}), body, http.StatusInternalServerError},
		{"missing", agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{}, agentTaskHTTPTools{}), body, http.StatusForbidden},
		{"status", agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{run: withHTTPTaskStatus(valid, agentmodel.AgentTaskRunPending), found: true}, agentTaskHTTPTools{}), body, http.StatusForbidden},
		{"owner", agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{run: withHTTPTaskOwner(valid, ""), found: true}, agentTaskHTTPTools{}), body, http.StatusForbidden},
		{"token", agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{run: withHTTPTaskToken(valid, 0), found: true}, agentTaskHTTPTools{}), body, http.StatusForbidden},
		{"invoke error", agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{run: valid, found: true}, agentTaskHTTPTools{err: wantErr}), body, http.StatusInternalServerError},
		{"success", agentTaskHTTPHandler(taskPrincipal(), &agentTaskHTTPRuns{run: withHTTPAuthorization(valid), found: true}, agentTaskHTTPTools{result: agentruntime.AgentToolInvocationResult{Tool: "query_records"}}), body, http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.h.agentTaskToolInvoke(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/task-tools/invoke", strings.NewReader(test.body)))
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func withHTTPTaskStatus(run agentmodel.AgentTaskRun, status agentmodel.AgentTaskRunStatus) agentmodel.AgentTaskRun {
	run.Status = status
	return run
}
func withHTTPTaskOwner(run agentmodel.AgentTaskRun, owner string) agentmodel.AgentTaskRun {
	run.Lease.Owner = owner
	return run
}
func withHTTPTaskToken(run agentmodel.AgentTaskRun, token int64) agentmodel.AgentTaskRun {
	run.Lease.FencingToken = token
	return run
}
func withHTTPAuthorization(run agentmodel.AgentTaskRun) agentmodel.AgentTaskRun {
	run.Evidence.Authorization = []agentmodel.AgentAuthorizationEvidence{{AllowedObjects: []string{"customer"}}}
	return run
}

func TestTypedInteractiveHTTPFailureBranches(t *testing.T) {
	wantErr := errors.New("interactive failure")
	principal := taskPrincipal("agent.task.read")
	newHandler := func(resolveErr, executeErr error, result agentruntime.AgentInteractiveExecutionResult) *AgentDialogHandler {
		h := agentTaskHTTPHandler(principal, nil, nil)
		h.contextResolver = agentDialogContextResolverFunc(func(context.Context, agentruntime.GlobalAgentContextRequest) (agentmodel.GlobalAgentContext, error) {
			return agentmodel.GlobalAgentContext{EntrypointKey: "assistant", ContextRevision: "context"}, resolveErr
		})
		h.interactive = interactiveExecutorFunc(func(context.Context, agentruntime.AgentInteractiveExecutionRequest) (agentruntime.AgentInteractiveExecutionResult, error) {
			return result, executeErr
		})
		h.securityAuditForPrincipal = func(*http.Request, principalmodel.Principal, string, string, map[string]any) {}
		return h
	}
	call := func(name string, stream bool, h *AgentDialogHandler, body string, want int) {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/agent-dialog/runs", strings.NewReader(body))
			if stream {
				h.runTypedInteractiveAgentStream(response, request, agentDialogRunRequest{Message: "hello", IdempotencyKey: body})
			} else {
				h.runTypedInteractiveAgent(response, request, agentDialogRunRequest{Message: "hello", IdempotencyKey: body})
			}
			if response.Code != want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	call("blocking context", false, newHandler(wantErr, nil, agentruntime.AgentInteractiveExecutionResult{}), "key", http.StatusInternalServerError)
	call("blocking execute", false, newHandler(nil, wantErr, agentruntime.AgentInteractiveExecutionResult{}), "key", http.StatusInternalServerError)
	withoutAudit := newHandler(nil, wantErr, agentruntime.AgentInteractiveExecutionResult{})
	withoutAudit.securityAuditForPrincipal = nil
	call("blocking execute without audit", false, withoutAudit, "key", http.StatusInternalServerError)
	call("stream context", true, newHandler(wantErr, nil, agentruntime.AgentInteractiveExecutionResult{}), "key", http.StatusInternalServerError)
	call("stream key", true, newHandler(nil, nil, agentruntime.AgentInteractiveExecutionResult{}), "", http.StatusBadRequest)
	call("stream execute", true, newHandler(nil, wantErr, agentruntime.AgentInteractiveExecutionResult{}), "key", http.StatusOK)
	streamWithoutAudit := newHandler(nil, wantErr, agentruntime.AgentInteractiveExecutionResult{})
	streamWithoutAudit.securityAuditForPrincipal = nil
	call("stream execute without audit", true, streamWithoutAudit, "key", http.StatusOK)
	invalid := agentruntime.AgentInteractiveExecutionResult{Result: agentruntime.InteractiveAgentResult{Structured: map[string]any{"bad": make(chan int)}}}
	call("stream marshal", true, newHandler(nil, nil, invalid), "key", http.StatusOK)
	flushAgentDialogEvent(nonFlushingResponseWriter{ResponseWriter: httptest.NewRecorder()})
}

func TestAgentDialogTypedSelectionAndOwnerOperationConditionEdges(t *testing.T) {
	interactive := interactiveExecutorFunc(func(context.Context, agentruntime.AgentInteractiveExecutionRequest) (agentruntime.AgentInteractiveExecutionResult, error) {
		return agentruntime.AgentInteractiveExecutionResult{}, nil
	})
	h := agentTaskHTTPHandler(taskPrincipal(), nil, nil)
	h.interactive = interactive
	for _, payload := range []string{`{"message":"hello"}`, `{"message":"hello","response_mode":"streaming"}`} {
		w := httptest.NewRecorder()
		h.agentDialogRun(w, httptest.NewRequest(http.MethodPost, "/agent-dialog/runs", strings.NewReader(payload)))
	}
	h.contextResolver = agentDialogContextResolverFunc(func(context.Context, agentruntime.GlobalAgentContextRequest) (agentmodel.GlobalAgentContext, error) {
		return agentmodel.GlobalAgentContext{}, nil
	})
	w := httptest.NewRecorder()
	h.agentDialogRun(w, httptest.NewRequest(http.MethodPost, "/agent-dialog/runs", strings.NewReader(`{"message":"hello","response_mode":"streaming"}`)))
	h.contextResolver = nil
	w = httptest.NewRecorder()
	h.agentDialogRunStream(w, httptest.NewRequest(http.MethodPost, "/agent-dialog/runs/stream", strings.NewReader(`{"message":"hello"}`)))
	h.interactiveRuns = interactiveRunReaderFunc(func(context.Context, string, principalmodel.Principal) (agentmodel.AgentInteractiveRun, bool, error) {
		return agentmodel.AgentInteractiveRun{}, false, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/agent-dialog/runs/external", nil)
	req.SetPathValue("runID", "external")
	h.agentDialogRunStatus(httptest.NewRecorder(), req)
	h.operations = &operationsapplication.OperationsApplicationService{}
	_, _ = h.executeAgentOwnerOperation(httptest.NewRequest(http.MethodPost, "/", nil), "retry", "run", nil, func(context.Context) (any, error) { return nil, nil })
}

type nonFlushingResponseWriter struct{ http.ResponseWriter }

func TestAgentDialogLocalStatusAndResolverFailureBranches(t *testing.T) {
	wantErr := errors.New("status failure")
	principal := taskPrincipal("agent.task.read")
	base := agentTaskHTTPHandler(principal, nil, nil)
	base.interactive = interactiveExecutorFunc(func(context.Context, agentruntime.AgentInteractiveExecutionRequest) (agentruntime.AgentInteractiveExecutionResult, error) {
		return agentruntime.AgentInteractiveExecutionResult{}, nil
	})
	base.contextResolver = agentDialogContextResolverFunc(func(context.Context, agentruntime.GlobalAgentContextRequest) (agentmodel.GlobalAgentContext, error) {
		return agentmodel.GlobalAgentContext{}, wantErr
	})
	response := httptest.NewRecorder()
	base.agentDialogRunStream(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/runs/stream", strings.NewReader(`{"message":"hello"}`)))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("stream resolver status=%d", response.Code)
	}

	statusHandler := agentTaskHTTPHandler(principal, nil, nil)
	for _, test := range []struct {
		name, runID string
		reader      AgentInteractiveRunReader
		want        int
	}{
		{"empty", "", nil, http.StatusBadRequest},
		{"reader error", "interactive_run_1", interactiveRunReaderFunc(func(context.Context, string, principalmodel.Principal) (agentmodel.AgentInteractiveRun, bool, error) {
			return agentmodel.AgentInteractiveRun{}, false, wantErr
		}), http.StatusInternalServerError},
		{"reader missing", "interactive_run_1", interactiveRunReaderFunc(func(context.Context, string, principalmodel.Principal) (agentmodel.AgentInteractiveRun, bool, error) {
			return agentmodel.AgentInteractiveRun{}, false, nil
		}), http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			statusHandler.interactiveRuns = test.reader
			req := httptest.NewRequest(http.MethodGet, "/agent-dialog/runs/"+test.runID, nil)
			req.SetPathValue("runID", test.runID)
			w := httptest.NewRecorder()
			statusHandler.agentDialogRunStatus(w, req)
			if w.Code != test.want {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}

func TestAgentDialogGetTaskRunBranches(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "default", AuthorizationRevision: "auth-2"}}, accessfixture.Bundle{Key: "role"})
	valid := agentmodel.AgentTaskRun{ID: "run", WorkspaceID: "default", Identity: agentmodel.AgentExecutionIdentity{Initiator: agentmodel.AgentPrincipalReference{UserID: "user", RoleKey: "role", AuthorizationRevision: "auth-2"}}}
	wantErr := errors.New("get failure")
	for _, test := range []struct {
		name string
		runs agentTaskRunService
		want int
	}{
		{"unavailable", nil, http.StatusServiceUnavailable},
		{"error", &agentTaskHTTPRuns{getErr: wantErr}, http.StatusInternalServerError},
		{"missing", &agentTaskHTTPRuns{}, http.StatusNotFound},
		{"workspace", &agentTaskHTTPRuns{run: withHTTPWorkspace(valid, "other"), found: true}, http.StatusNotFound},
		{"user", &agentTaskHTTPRuns{run: withHTTPUser(valid, "other"), found: true}, http.StatusNotFound},
		{"role", &agentTaskHTTPRuns{run: withHTTPRole(valid, "other"), found: true}, http.StatusNotFound},
		{"stale", &agentTaskHTTPRuns{run: withHTTPAuthRevision(valid, "old"), found: true}, http.StatusNotFound},
		{"success", &agentTaskHTTPRuns{run: valid, found: true}, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := agentTaskHTTPHandler(principal, test.runs, nil)
			req := httptest.NewRequest(http.MethodGet, "/agent-dialog/task-runs/run", nil)
			req.SetPathValue("taskRunID", "run")
			w := httptest.NewRecorder()
			h.agentDialogGetTaskRun(w, req)
			if w.Code != test.want {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}

func withHTTPWorkspace(run agentmodel.AgentTaskRun, value string) agentmodel.AgentTaskRun {
	run.WorkspaceID = value
	return run
}
func withHTTPUser(run agentmodel.AgentTaskRun, value string) agentmodel.AgentTaskRun {
	run.Identity.Initiator.UserID = value
	return run
}
func withHTTPRole(run agentmodel.AgentTaskRun, value string) agentmodel.AgentTaskRun {
	run.Identity.Initiator.RoleKey = value
	return run
}
func withHTTPAuthRevision(run agentmodel.AgentTaskRun, value string) agentmodel.AgentTaskRun {
	run.Identity.Initiator.AuthorizationRevision = value
	return run
}
