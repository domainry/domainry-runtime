package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/modulecapability"
	identitysdk "github.com/domainry/domainry-identity-sdk"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type schedulerHTTPResult struct {
	status int
	err    error
	value  any
}

type schedulerServiceCall struct {
	operation string
	resource  string
	key       string
	note      string
	data      map[string]any
	principal principalmodel.Principal
}

type fakeSchedulerService struct {
	modulecapability.Binding
	call        schedulerServiceCall
	result      schedulerapplication.SchedulerDefinitionSimulation
	preview     schedulerapplication.SchedulerDefinitionPreview
	runs        []schedulersdk.Run
	deadLetters []schedulersdk.DeadLetter
	err         error
}

func (*fakeSchedulerService) Descriptor() schedulersdk.Descriptor { return schedulersdk.Descriptor{} }
func (*fakeSchedulerService) Reconcile(context.Context) error     { return nil }
func (*fakeSchedulerService) Preview(context.Context, schedulersdk.Schedule, time.Time, int) ([]time.Time, error) {
	return nil, nil
}
func (*fakeSchedulerService) Tick(context.Context, time.Time, int) (int, error) { return 0, nil }
func (f *fakeSchedulerService) TriggerNow(_ context.Context, resource, _ string) (schedulersdk.Run, error) {
	f.call = schedulerServiceCall{operation: "run", resource: resource, key: "command-a"}
	return schedulersdk.Run{Trigger: schedulersdk.Trigger{RunID: "run-a", DefinitionKey: resource}}, f.err
}
func (f *fakeSchedulerService) Reschedule(_ context.Context, resource string, _ time.Time, _ string) error {
	f.call = schedulerServiceCall{operation: "reschedule", resource: resource, key: "command-a"}
	return f.err
}
func (f *fakeSchedulerService) Runs(context.Context, int) ([]schedulersdk.Run, error) {
	return f.runs, nil
}
func (f *fakeSchedulerService) DeadLetters(context.Context, int) ([]schedulersdk.DeadLetter, error) {
	return f.deadLetters, nil
}
func (*fakeSchedulerService) Run(context.Context, string) (schedulersdk.Run, error) {
	return schedulersdk.Run{}, nil
}
func (f *fakeSchedulerService) RetryRun(_ context.Context, resource, _ string) (schedulersdk.Run, error) {
	f.call = schedulerServiceCall{operation: "retry", resource: resource, key: "command-a"}
	return schedulersdk.Run{Trigger: schedulersdk.Trigger{RunID: resource}}, f.err
}
func (f *fakeSchedulerService) CancelRun(_ context.Context, resource, _ string) (schedulersdk.Run, error) {
	f.call = schedulerServiceCall{operation: "cancel", resource: resource, key: "command-a"}
	return schedulersdk.Run{Trigger: schedulersdk.Trigger{RunID: resource}}, f.err
}
func (*fakeSchedulerService) DeadLetter(context.Context, string) (schedulersdk.DeadLetter, error) {
	return schedulersdk.DeadLetter{}, nil
}
func (f *fakeSchedulerService) ResolveDeadLetter(_ context.Context, resource, note string) (schedulersdk.DeadLetter, error) {
	f.call = schedulerServiceCall{operation: "resolve", resource: resource, key: "command-a", note: note}
	return schedulersdk.DeadLetter{RunID: resource}, f.err
}
func (f *fakeSchedulerService) RequeueDeadLetter(_ context.Context, resource, note string) (schedulersdk.Run, error) {
	f.call = schedulerServiceCall{operation: "requeue", resource: resource, key: "command-a", note: note}
	return schedulersdk.Run{Trigger: schedulersdk.Trigger{RunID: resource}}, f.err
}
func (*fakeSchedulerService) Start(context.Context, schedulersdk.WorkerConfig) <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}
func (*fakeSchedulerService) Close(context.Context) error { return nil }

func (f *fakeSchedulerService) ManagementDefinitions(_ context.Context, principal principalmodel.Principal) ([]schedulerapplication.ManagementSchedulerDefinitionDTO, error) {
	f.call = schedulerServiceCall{operation: "tenant_definitions", principal: principal}
	return []schedulerapplication.ManagementSchedulerDefinitionDTO{{Key: "definition-a"}}, f.err
}

func (f *fakeSchedulerService) ManagementDefinition(_ context.Context, resource string, principal principalmodel.Principal) (schedulerapplication.ManagementSchedulerDefinitionDTO, error) {
	f.call = schedulerServiceCall{operation: "tenant_definition", resource: resource, principal: principal}
	return schedulerapplication.ManagementSchedulerDefinitionDTO{Key: resource}, f.err
}

func (f *fakeSchedulerService) ManagementDefinitionVersions(_ context.Context, resource string, principal principalmodel.Principal) ([]schedulerapplication.ManagementSchedulerDefinitionVersionDTO, error) {
	f.call = schedulerServiceCall{operation: "tenant_versions", resource: resource, principal: principal}
	return []schedulerapplication.ManagementSchedulerDefinitionVersionDTO{{VersionID: "version-a"}}, f.err
}

func (f *fakeSchedulerService) ManagementAuthoringContract(_ context.Context, principal principalmodel.Principal) (schedulerapplication.ManagementSchedulerAuthoringContract, error) {
	f.call = schedulerServiceCall{operation: "tenant_authoring_contract", principal: principal}
	return schedulerapplication.ManagementSchedulerAuthoringContract{ResourceType: "scheduler"}, f.err
}

func (f *fakeSchedulerService) AuthorizeOpsRead(_ context.Context, principal principalmodel.Principal) error {
	f.call = schedulerServiceCall{operation: "ops_state", principal: principal}
	return f.err
}

func (f *fakeSchedulerService) PreviewDefinition(_ context.Context, data map[string]any, principal principalmodel.Principal) (schedulerapplication.SchedulerDefinitionPreview, error) {
	f.call = schedulerServiceCall{operation: "preview", data: data, principal: principal}
	return f.preview, f.err
}

func (f *fakeSchedulerService) PreviewSchedule(_ context.Context, data map[string]any, principal principalmodel.Principal) (schedulerapplication.SchedulerDefinitionPreview, error) {
	f.call = schedulerServiceCall{operation: "schedule_preview", data: data, principal: principal}
	return f.preview, f.err
}

func (f *fakeSchedulerService) SimulateManagementDefinition(_ context.Context, resource string, principal principalmodel.Principal) (schedulerapplication.SchedulerDefinitionSimulation, error) {
	f.call = schedulerServiceCall{operation: "tenant_simulate", resource: resource, principal: principal}
	return f.result, f.err
}

func newSchedulerHTTPHandler(result *schedulerHTTPResult) *SchedulerHandler {
	return NewSchedulerHandler(SchedulerDependencies{
		Service:   schedulerapplication.NewSchedulerApplicationService(nil),
		Principal: func(*http.Request) principalmodel.Principal { return principalmodel.Principal{} },
		WriteJSON: func(_ http.ResponseWriter, status int, value any) { result.status, result.value = status, value },
		WriteServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) {
			result.err = err
		},
		DecodeJSON: func(w http.ResponseWriter, request *http.Request, value any) bool {
			if err := json.NewDecoder(request.Body).Decode(value); err != nil {
				result.status, result.err = http.StatusBadRequest, err
				return false
			}
			return true
		},
		Authenticated: func(handler http.HandlerFunc) http.HandlerFunc { return handler },
	})
}

func TestSchedulerHandlersWriteSuccessfulResultsAndNormalizeInputs(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "operator-a"}}
	service := &fakeSchedulerService{
		preview: schedulerapplication.SchedulerDefinitionPreview{NextRuns: []string{"next"}},
		result:  schedulerapplication.SchedulerDefinitionSimulation{Status: "ok"},
	}
	result := &schedulerHTTPResult{}
	handler := newSchedulerHTTPHandler(result)
	handler.service = service
	handler.binding = service
	handler.principal = func(*http.Request) principalmodel.Principal { return principal }

	tests := []struct {
		name      string
		body      string
		path      map[string]string
		call      func(http.ResponseWriter, *http.Request)
		operation string
		resource  string
		note      string
	}{
		{name: "preview", body: `{"data":{"key":"daily"}}`, call: handler.previewSchedulerJob, operation: "preview"},
		{name: "schedule preview", body: `{"schedule_type":"interval","interval_seconds":300}`, call: handler.previewSchedulerSchedule, operation: "schedule_preview"},
		{name: "simulate", path: map[string]string{"definitionID": "  definition-a  "}, call: handler.simulateSchedulerJob, operation: "tenant_simulate", resource: "definition-a"},
		{name: "run", path: map[string]string{"definitionID": "  definition-a  "}, call: handler.runSchedulerJob, operation: "run", resource: "definition-a"},
		{name: "reschedule", body: `{"next_run_at":"2026-08-22T09:00:00Z"}`, path: map[string]string{"definitionID": "  definition-a  "}, call: handler.rescheduleOpsSchedulerDefinition, operation: "reschedule", resource: "definition-a"},
		{name: "retry", path: map[string]string{"runID": "  run-a  "}, call: handler.retrySchedulerRun, operation: "retry", resource: "run-a"},
		{name: "cancel", path: map[string]string{"runID": "  run-a  "}, call: handler.cancelSchedulerRun, operation: "cancel", resource: "run-a"},
		{name: "resolve", body: `{"note":"recovered"}`, path: map[string]string{"deadLetterID": "  dead-a  "}, call: handler.resolveSchedulerDeadLetter, operation: "resolve", resource: "dead-a", note: "recovered"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			*result = schedulerHTTPResult{}
			service.call = schedulerServiceCall{}
			writer, request := schedulerHTTPRequest(test.body, test.path)
			request.Header.Set("Idempotency-Key", "  command-a  ")
			request.Header.Set("Expected-Record-Version", "  command-a  ")
			test.call(writer, request)
			if result.status != http.StatusOK || result.err != nil {
				t.Fatalf("status=%d error=%v", result.status, result.err)
			}
			if service.call.operation != test.operation || service.call.resource != test.resource {
				t.Fatalf("call=%+v", service.call)
			}
			ownerOperation := test.operation == "run" || test.operation == "reschedule" || test.operation == "retry" || test.operation == "cancel" || test.operation == "resolve"
			if !ownerOperation && (service.call.principal.WorkspaceID != principal.WorkspaceID || service.call.principal.UserID != principal.UserID) {
				t.Fatalf("principal call=%+v", service.call)
			}
			if service.call.note != test.note {
				t.Fatalf("note=%q want %q", service.call.note, test.note)
			}
			if test.operation == "preview" {
				if service.call.data["key"] != "daily" {
					t.Fatalf("preview data=%v", service.call.data)
				}
			} else if test.operation == "schedule_preview" {
				if service.call.data["schedule_type"] != "interval" {
					t.Fatalf("schedule preview data=%v", service.call.data)
				}
			} else if (test.operation == "run" || test.operation == "reschedule" || test.operation == "retry" || test.operation == "cancel" || test.operation == "resolve") && service.call.key != "command-a" {
				t.Fatalf("key=%q", service.call.key)
			}
		})
	}
}

func TestSchedulerHandlersPropagateFakeServiceErrors(t *testing.T) {
	want := errors.New("scheduler unavailable")
	service := &fakeSchedulerService{err: want}
	result := &schedulerHTTPResult{}
	handler := newSchedulerHTTPHandler(result)
	handler.service = service
	handler.binding = service
	tests := []struct {
		name string
		body string
		path map[string]string
		call func(http.ResponseWriter, *http.Request)
	}{
		{name: "preview", body: `{"data":{}}`, call: handler.previewSchedulerJob},
		{name: "schedule preview", body: `{}`, call: handler.previewSchedulerSchedule},
		{name: "simulate", path: map[string]string{"definitionID": "id"}, call: handler.simulateSchedulerJob},
		{name: "run", path: map[string]string{"definitionID": "id"}, call: handler.runSchedulerJob},
		{name: "reschedule", body: `{"next_run_at":"2026-08-22T09:00:00Z"}`, path: map[string]string{"definitionID": "id"}, call: handler.rescheduleOpsSchedulerDefinition},
		{name: "retry", path: map[string]string{"runID": "id"}, call: handler.retrySchedulerRun},
		{name: "cancel", path: map[string]string{"runID": "id"}, call: handler.cancelSchedulerRun},
		{name: "resolve", body: `{}`, path: map[string]string{"deadLetterID": "id"}, call: handler.resolveSchedulerDeadLetter},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			*result = schedulerHTTPResult{}
			writer, request := schedulerHTTPRequest(test.body, test.path)
			test.call(writer, request)
			if !errors.Is(result.err, want) {
				t.Fatalf("error=%v", result.err)
			}
		})
	}
}

func TestSchedulerRoutesEnforceMethodsAndApplySurfaceMiddleware(t *testing.T) {
	authenticatedCalls := 0
	handler := newSchedulerHTTPHandler(&schedulerHTTPResult{})
	handler.authenticated = func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			authenticatedCalls++
			w.WriteHeader(http.StatusNoContent)
		}
	}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	paths := []string{
		"/scheduler/definitions/validate",
		"/scheduler/schedules/preview",
		"/scheduler/definitions/id/simulate",
		"/scheduler/definitions/id/run",
		"/scheduler/definitions/id/reschedule",
		"/scheduler/runs/id/retry",
		"/scheduler/runs/id/cancel",
		"/scheduler/dead-letters/id/resolve",
	}
	for _, path := range paths {
		writer := httptest.NewRecorder()
		mux.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, path, nil))
		if writer.Code != http.StatusNoContent {
			t.Fatalf("POST %s status=%d", path, writer.Code)
		}
	}
	if authenticatedCalls != 8 {
		t.Fatalf("authenticated calls=%d", authenticatedCalls)
	}
	wrongMethod := httptest.NewRecorder()
	mux.ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodGet, "/scheduler/definitions/id/run", nil))
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status=%d", wrongMethod.Code)
	}
	old := httptest.NewRecorder()
	mux.ServeHTTP(old, httptest.NewRequest(http.MethodPost, "/scheduler/jobs/preview", nil))
	if old.Code != http.StatusNotFound {
		t.Fatalf("legacy scheduler route status=%d", old.Code)
	}
}

func schedulerHTTPRequest(body string, pathValues map[string]string) (*httptest.ResponseRecorder, *http.Request) {
	request := httptest.NewRequest(http.MethodPost, "/scheduler", strings.NewReader(body))
	for key, value := range pathValues {
		request.SetPathValue(key, value)
	}
	return httptest.NewRecorder(), request
}

func TestSchedulerHandlersRejectDecodeAndPropagateServiceErrors(t *testing.T) {
	result := &schedulerHTTPResult{}
	handler := newSchedulerHTTPHandler(result)
	writer, request := schedulerHTTPRequest("{", nil)
	handler.previewSchedulerJob(writer, request)
	if result.status != http.StatusBadRequest {
		t.Fatalf("preview decode status=%d error=%v", result.status, result.err)
	}
	*result = schedulerHTTPResult{}
	writer, request = schedulerHTTPRequest("{", nil)
	handler.previewSchedulerSchedule(writer, request)
	if result.status != http.StatusBadRequest {
		t.Fatalf("schedule preview decode status=%d error=%v", result.status, result.err)
	}

	tests := []struct {
		name string
		body string
		path map[string]string
		call func(http.ResponseWriter, *http.Request)
	}{
		{name: "preview", body: `{}`, call: handler.previewSchedulerJob},
		{name: "simulate", path: map[string]string{"definitionID": "missing"}, call: handler.simulateSchedulerJob},
		{name: "run", path: map[string]string{"definitionID": "missing"}, call: handler.runSchedulerJob},
		{name: "retry", path: map[string]string{"runID": "missing"}, call: handler.retrySchedulerRun},
		{name: "cancel", path: map[string]string{"runID": "missing"}, call: handler.cancelSchedulerRun},
		{name: "resolve", body: `{}`, path: map[string]string{"deadLetterID": "missing"}, call: handler.resolveSchedulerDeadLetter},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			*result = schedulerHTTPResult{}
			writer, request := schedulerHTTPRequest(test.body, test.path)
			test.call(writer, request)
			if result.err == nil {
				t.Fatalf("service error missing: status=%d", result.status)
			}
		})
	}
}

func TestResolveSchedulerDeadLetterOptionalBodyEdges(t *testing.T) {
	for _, nilBody := range []bool{true, false} {
		result := &schedulerHTTPResult{}
		handler := newSchedulerHTTPHandler(result)
		writer, request := schedulerHTTPRequest("", map[string]string{"deadLetterID": "missing"})
		if nilBody {
			request.Body = nil
		}
		handler.resolveSchedulerDeadLetter(writer, request)
		if result.status == http.StatusBadRequest || result.err == nil {
			t.Fatalf("nilBody=%v status=%d error=%v", nilBody, result.status, result.err)
		}
	}
	result := &schedulerHTTPResult{}
	handler := newSchedulerHTTPHandler(result)
	writer, request := schedulerHTTPRequest("{", map[string]string{"deadLetterID": "missing"})
	handler.resolveSchedulerDeadLetter(writer, request)
	if result.status != http.StatusBadRequest {
		t.Fatalf("invalid optional body status=%d error=%v", result.status, result.err)
	}
}

func TestSchedulerConfiguredOperationsAndRoutes(t *testing.T) {
	result := &schedulerHTTPResult{}
	handler := newSchedulerHTTPHandler(result)
	handler.operations = operationsapplication.NewOperationsApplicationService(nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/scheduler", nil)
	if _, err := handler.executeOwnerOperation(request, "unknown", "unknown", "resource", nil, func(context.Context) (any, error) { return nil, nil }); err == nil {
		t.Fatal("configured operations service was bypassed")
	}
	handler.RegisterRoutes(http.NewServeMux())
}
