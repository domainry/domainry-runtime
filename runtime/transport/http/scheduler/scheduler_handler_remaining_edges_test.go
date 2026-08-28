package scheduler

import (
	"errors"
	"net/http"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestSchedulerTenantAndOpsReadSurfaceRemainingEdges(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "operator-a"}}
	tests := []struct {
		name      string
		path      map[string]string
		invoke    func(*SchedulerHandler, http.ResponseWriter, *http.Request)
		operation string
		resource  string
	}{
		{"tenant definitions", nil, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) {
			h.listTenantAdminSchedulerDefinitions(w, r)
		}, "tenant_definitions", ""},
		{"tenant definition", map[string]string{"definitionID": " definition-a "}, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) {
			h.getTenantAdminSchedulerDefinition(w, r)
		}, "tenant_definition", "definition-a"},
		{"tenant versions", map[string]string{"definitionID": " definition-a "}, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) {
			h.listTenantAdminSchedulerDefinitionVersions(w, r)
		}, "tenant_versions", "definition-a"},
		{"authoring contract", nil, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) {
			h.getTenantAdminSchedulerAuthoringContract(w, r)
		}, "tenant_authoring_contract", ""},
		{"ops state", nil, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) { h.getOpsSchedulerState(w, r) }, "ops_state", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, wantErr := range []bool{false, true} {
				service := &fakeSchedulerService{}
				if wantErr {
					service.err = errors.New("unavailable")
				}
				result := &schedulerHTTPResult{}
				handler := newSchedulerHTTPHandler(result)
				handler.service = service
				handler.principal = func(*http.Request) principalmodel.Principal { return principal }
				writer, request := schedulerHTTPRequest("", test.path)
				test.invoke(handler, writer, request)
				if wantErr {
					if !errors.Is(result.err, service.err) {
						t.Fatalf("service error=%v", result.err)
					}
					continue
				}
				if result.status != http.StatusOK || result.err != nil || service.call.operation != test.operation ||
					service.call.resource != test.resource || service.call.principal.UserID != principal.UserID {
					t.Fatalf("status=%d err=%v call=%+v", result.status, result.err, service.call)
				}
			}
		})
	}
}

func TestSchedulerHandlerPreservesExplicitAuthenticatedMiddleware(t *testing.T) {
	authenticated := func(handler http.HandlerFunc) http.HandlerFunc { return handler }
	handler := NewSchedulerHandler(SchedulerDependencies{Authenticated: authenticated})
	if handler.authenticated == nil {
		t.Fatal("explicit authenticated middleware was discarded")
	}
}

func TestSchedulerOpsMutationSurfaceRemainingEdges(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		path      map[string]string
		invoke    func(*SchedulerHandler, http.ResponseWriter, *http.Request)
		operation string
		note      string
	}{
		{"run", "", map[string]string{"definitionID": " definition-a "}, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) { h.runOpsSchedulerJob(w, r) }, "run", ""},
		{"reschedule", `{"next_run_at":"2026-08-22T09:00:00Z"}`, map[string]string{"definitionID": " definition-a "}, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) {
			h.rescheduleOpsSchedulerDefinition(w, r)
		}, "reschedule", ""},
		{"retry", "", map[string]string{"runID": " run-a "}, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) { h.retryOpsSchedulerRun(w, r) }, "retry", ""},
		{"cancel", "", map[string]string{"runID": " run-a "}, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) { h.cancelOpsSchedulerRun(w, r) }, "cancel", ""},
		{"resolve", `{"note":"fixed"}`, map[string]string{"deadLetterID": " dead-a "}, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) {
			h.resolveOpsSchedulerDeadLetter(w, r)
		}, "resolve", "fixed"},
		{"requeue", `{"note":"retry later"}`, map[string]string{"deadLetterID": " dead-a "}, func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) {
			h.requeueOpsSchedulerDeadLetter(w, r)
		}, "requeue", "retry later"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, wantErr := range []bool{false, true} {
				service := &fakeSchedulerService{result: schedulerapplication.SchedulerOperationResult{Status: "ok"}}
				if wantErr {
					service.err = errors.New("unavailable")
				}
				result := &schedulerHTTPResult{}
				handler := newSchedulerHTTPHandler(result)
				handler.service = service
				writer, request := schedulerHTTPRequest(test.body, test.path)
				request.Header.Set("Idempotency-Key", " command-a ")
				test.invoke(handler, writer, request)
				if wantErr {
					if !errors.Is(result.err, service.err) {
						t.Fatalf("service error=%v", result.err)
					}
					continue
				}
				if result.status != http.StatusOK || result.err != nil || service.call.operation != test.operation ||
					(test.operation != "reschedule" && service.call.key != "command-a") || service.call.note != test.note {
					t.Fatalf("status=%d err=%v call=%+v", result.status, result.err, service.call)
				}
			}
		})
	}
}

func TestSchedulerOpsDeadLetterOptionalBodyEdges(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*SchedulerHandler, http.ResponseWriter, *http.Request)
	}{
		{"resolve", func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) {
			h.resolveOpsSchedulerDeadLetter(w, r)
		}},
		{"requeue", func(h *SchedulerHandler, w http.ResponseWriter, r *http.Request) {
			h.requeueOpsSchedulerDeadLetter(w, r)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, nilBody := range []bool{false, true} {
				result := &schedulerHTTPResult{}
				handler := newSchedulerHTTPHandler(result)
				handler.service = &fakeSchedulerService{result: schedulerapplication.SchedulerOperationResult{Status: "ok"}}
				writer, request := schedulerHTTPRequest("", map[string]string{"deadLetterID": "dead-a"})
				if nilBody {
					request.Body = nil
				}
				test.invoke(handler, writer, request)
				if result.status != http.StatusOK || result.err != nil {
					t.Fatalf("nilBody=%v status=%d err=%v", nilBody, result.status, result.err)
				}
			}

			result := &schedulerHTTPResult{}
			handler := newSchedulerHTTPHandler(result)
			handler.service = &fakeSchedulerService{}
			writer, request := schedulerHTTPRequest("{", map[string]string{"deadLetterID": "dead-a"})
			test.invoke(handler, writer, request)
			if result.status != http.StatusBadRequest || result.err == nil {
				t.Fatalf("invalid body status=%d err=%v", result.status, result.err)
			}
		})
	}
}
