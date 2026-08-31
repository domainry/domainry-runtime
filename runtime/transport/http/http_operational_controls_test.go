package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOperationalControlsRejectMutationButAllowReadAndRecovery(t *testing.T) {
	router := &HTTPRouter{healthRegistry: newRuntimeHealthRegistry(), runtimeInstanceID: "instance-a"}
	router.operationsControlState = func(_ context.Context, kind, owner string) (bool, bool, error) {
		return kind == "maintenance" && owner == "runtime", true, nil
	}
	handler := router.withOperationalControls(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	mutation := httptest.NewRecorder()
	handler.ServeHTTP(mutation, httptest.NewRequest(http.MethodPost, "/records/customer", nil))
	if mutation.Code != http.StatusServiceUnavailable {
		t.Fatalf("mutation status=%d body=%s", mutation.Code, mutation.Body.String())
	}
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/records/customer", nil))
	if read.Code != http.StatusNoContent {
		t.Fatalf("read status=%d", read.Code)
	}
	recovery := httptest.NewRecorder()
	handler.ServeHTTP(recovery, httptest.NewRequest(http.MethodPut, "/operations/controls/maintenance/runtime", nil))
	if recovery.Code != http.StatusNoContent {
		t.Fatalf("recovery status=%d", recovery.Code)
	}
}

func TestOperationalControlsFailClosedWhenDurableStateUnavailable(t *testing.T) {
	router := &HTTPRouter{healthRegistry: newRuntimeHealthRegistry()}
	router.operationsControlState = func(context.Context, string, string) (bool, bool, error) {
		return false, false, context.DeadlineExceeded
	}
	handler := router.withOperationalControls(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/records/customer/1", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
