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

	identitysdk "github.com/domainry/domainry-identity-sdk"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
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
	call    schedulerServiceCall
	result  schedulerapplication.SchedulerOperationResult
	preview schedulerapplication.SchedulerDefinitionPreview
	err     error
}

func (f *fakeSchedulerService) TenantAdminDefinitions(_ context.Context, principal principalmodel.Principal) ([]schedulerapplication.TenantAdminSchedulerDefinitionDTO, error) {
	f.call = schedulerServiceCall{operation: "tenant_definitions", principal: principal}
	return []schedulerapplication.TenantAdminSchedulerDefinitionDTO{{Key: "definition-a"}}, f.err
}

func (f *fakeSchedulerService) TenantAdminDefinition(_ context.Context, resource string, principal principalmodel.Principal) (schedulerapplication.TenantAdminSchedulerDefinitionDTO, error) {
	f.call = schedulerServiceCall{operation: "tenant_definition", resource: resource, principal: principal}
	return schedulerapplication.TenantAdminSchedulerDefinitionDTO{Key: resource}, f.err
}

func (f *fakeSchedulerService) TenantAdminDefinitionVersions(_ context.Context, resource string, principal principalmodel.Principal) ([]schedulerapplication.TenantAdminSchedulerDefinitionVersionDTO, error) {
	f.call = schedulerServiceCall{operation: "tenant_versions", resource: resource, principal: principal}
	return []schedulerapplication.TenantAdminSchedulerDefinitionVersionDTO{{VersionID: "version-a"}}, f.err
}

func (f *fakeSchedulerService) TenantAdminAuthoringContract(_ context.Context, principal principalmodel.Principal) (schedulerapplication.TenantAdminSchedulerAuthoringContract, error) {
	f.call = schedulerServiceCall{operation: "tenant_authoring_contract", principal: principal}
	return schedulerapplication.TenantAdminSchedulerAuthoringContract{ResourceType: "scheduler"}, f.err
}

func (f *fakeSchedulerService) OpsState(_ context.Context, principal principalmodel.Principal) (schedulerapplication.OpsSchedulerStateDTO, error) {
	f.call = schedulerServiceCall{operation: "ops_state", principal: principal}
	return schedulerapplication.OpsSchedulerStateDTO{}, f.err
}

func (f *fakeSchedulerService) GetDefinition(_ context.Context, resource string, principal principalmodel.Principal) (recordmodel.Record, error) {
	f.call = schedulerServiceCall{operation: "get", resource: resource, principal: principal}
	return recordmodel.Record{ID: resource}, f.err
}

func (f *fakeSchedulerService) DefinitionVersions(_ context.Context, resource string, principal principalmodel.Principal) ([]schedulerapplication.SchedulerDefinitionVersion, error) {
	f.call = schedulerServiceCall{operation: "versions", resource: resource, principal: principal}
	return []schedulerapplication.SchedulerDefinitionVersion{{VersionID: "version-a"}}, f.err
}

func (f *fakeSchedulerService) PreviewDefinition(_ context.Context, data map[string]any, principal principalmodel.Principal) (schedulerapplication.SchedulerDefinitionPreview, error) {
	f.call = schedulerServiceCall{operation: "preview", data: data, principal: principal}
	return f.preview, f.err
}

func (f *fakeSchedulerService) PreviewSchedule(_ context.Context, data map[string]any, principal principalmodel.Principal) (schedulerapplication.SchedulerDefinitionPreview, error) {
	f.call = schedulerServiceCall{operation: "schedule_preview", data: data, principal: principal}
	return f.preview, f.err
}

func (f *fakeSchedulerService) SimulateJob(_ context.Context, resource string, principal principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error) {
	f.call = schedulerServiceCall{operation: "simulate", resource: resource, principal: principal}
	return f.result, f.err
}

func (f *fakeSchedulerService) SimulateTenantAdminDefinition(_ context.Context, resource string, principal principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error) {
	f.call = schedulerServiceCall{operation: "tenant_simulate", resource: resource, principal: principal}
	return f.result, f.err
}

func (f *fakeSchedulerService) RunJob(_ context.Context, resource, key string, principal principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error) {
	f.call = schedulerServiceCall{operation: "run", resource: resource, key: key, principal: principal}
	return f.result, f.err
}

func (f *fakeSchedulerService) RescheduleDefinition(_ context.Context, resource string, nextRunAt time.Time, principal principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error) {
	f.call = schedulerServiceCall{operation: "reschedule", resource: resource, data: map[string]any{"next_run_at": nextRunAt.UTC().Format(time.RFC3339)}, principal: principal}
	return f.result, f.err
}

func (f *fakeSchedulerService) RetryRun(_ context.Context, resource, key string, principal principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error) {
	f.call = schedulerServiceCall{operation: "retry", resource: resource, key: key, principal: principal}
	return f.result, f.err
}

func (f *fakeSchedulerService) CancelRun(_ context.Context, resource, key string, principal principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error) {
	f.call = schedulerServiceCall{operation: "cancel", resource: resource, key: key, principal: principal}
	return f.result, f.err
}

func (f *fakeSchedulerService) ResolveDeadLetter(_ context.Context, resource, note, key string, principal principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error) {
	f.call = schedulerServiceCall{operation: "resolve", resource: resource, key: key, note: note, principal: principal}
	return f.result, f.err
}

func (f *fakeSchedulerService) RequeueDeadLetter(_ context.Context, resource, note, key string, principal principalmodel.Principal) (schedulerapplication.SchedulerOperationResult, error) {
	f.call = schedulerServiceCall{operation: "requeue", resource: resource, key: key, note: note, principal: principal}
	return f.result, f.err
}

func newSchedulerHTTPHandler(result *schedulerHTTPResult) *SchedulerHandler {
	return NewSchedulerHandler(SchedulerDependencies{
		Service:   schedulerapplication.NewSchedulerApplicationService(nil, nil, nil, nil),
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
		Admin: func(handler http.HandlerFunc) http.HandlerFunc { return handler },
	})
}

func TestSchedulerHandlersWriteSuccessfulResultsAndNormalizeInputs(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "operator-a"}}
	service := &fakeSchedulerService{
		preview: schedulerapplication.SchedulerDefinitionPreview{NextRuns: []string{"next"}},
		result:  schedulerapplication.SchedulerOperationResult{Status: "ok"},
	}
	result := &schedulerHTTPResult{}
	handler := newSchedulerHTTPHandler(result)
	handler.service = service
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
		{name: "get", path: map[string]string{"definitionID": "  definition-a  "}, call: handler.getSchedulerJob, operation: "get", resource: "definition-a"},
		{name: "versions", path: map[string]string{"definitionID": "  definition-a  "}, call: handler.listSchedulerJobVersions, operation: "versions", resource: "definition-a"},
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
			if service.call.operation != test.operation || service.call.resource != test.resource || service.call.principal.WorkspaceID != principal.WorkspaceID || service.call.principal.UserID != principal.UserID {
				t.Fatalf("call=%+v", service.call)
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
			} else if (test.operation == "run" || test.operation == "retry" || test.operation == "cancel" || test.operation == "resolve") && service.call.key != "command-a" {
				t.Fatalf("key=%q", service.call.key)
			}
			if test.operation == "reschedule" && service.call.data["next_run_at"] != "2026-08-22T09:00:00Z" {
				t.Fatalf("reschedule data=%v", service.call.data)
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
	tests := []struct {
		name string
		body string
		path map[string]string
		call func(http.ResponseWriter, *http.Request)
	}{
		{name: "preview", body: `{"data":{}}`, call: handler.previewSchedulerJob},
		{name: "schedule preview", body: `{}`, call: handler.previewSchedulerSchedule},
		{name: "get", path: map[string]string{"definitionID": "id"}, call: handler.getSchedulerJob},
		{name: "versions", path: map[string]string{"definitionID": "id"}, call: handler.listSchedulerJobVersions},
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
	adminCalls := 0
	authenticatedCalls := 0
	handler := newSchedulerHTTPHandler(&schedulerHTTPResult{})
	handler.admin = func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			adminCalls++
			w.WriteHeader(http.StatusNoContent)
		}
	}
	handler.authenticated = func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			authenticatedCalls++
			w.WriteHeader(http.StatusNoContent)
		}
	}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	paths := []string{
		"/tenant-admin/scheduler/definitions/validate",
		"/tenant-admin/scheduler/schedules/preview",
		"/tenant-admin/scheduler/definitions/id/simulate",
		"/operations/scheduler/definitions/id/run",
		"/operations/scheduler/definitions/id/reschedule",
		"/operations/scheduler/runs/id/retry",
		"/operations/scheduler/runs/id/cancel",
		"/operations/scheduler/dead-letters/id/resolve",
	}
	for _, path := range paths {
		writer := httptest.NewRecorder()
		mux.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, path, nil))
		if writer.Code != http.StatusNoContent {
			t.Fatalf("POST %s status=%d", path, writer.Code)
		}
	}
	if adminCalls != 0 || authenticatedCalls != 8 {
		t.Fatalf("admin calls=%d authenticated calls=%d", adminCalls, authenticatedCalls)
	}
	wrongMethod := httptest.NewRecorder()
	mux.ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodGet, "/operations/scheduler/definitions/id/run", nil))
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
