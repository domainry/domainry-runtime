package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHighRiskOperationPolicyIsEnforcedFromCompiledEndpointContract(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /operations/scheduler/definitions/{definitionID}/run", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /operations/scheduler/runs/{runID}/cancel", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /operations/break-glass", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := (&HTTPRouter{}).withHighRiskOperationPolicy(mux, mux)

	tests := []struct {
		name         string
		path         string
		reason       string
		confirmation string
		wantStatus   int
	}{
		{name: "reason missing", path: "/operations/scheduler/definitions/job-1/run", wantStatus: http.StatusBadRequest},
		{name: "reason supplied", path: "/operations/scheduler/definitions/job-1/run", reason: "incident-42 manual run", wantStatus: http.StatusNoContent},
		{name: "confirmation missing", path: "/operations/scheduler/runs/run-1/cancel", reason: "incident-42 stuck lease", wantStatus: http.StatusBadRequest},
		{name: "confirmation supplied", path: "/operations/scheduler/runs/run-1/cancel", reason: "incident-42 stuck lease", confirmation: operationConfirmedValue, wantStatus: http.StatusNoContent},
		{name: "break glass requires distinct confirmation", path: "/operations/break-glass", reason: "incident-42 emergency recovery", confirmation: operationConfirmedValue, wantStatus: http.StatusBadRequest},
		{name: "break glass supplied", path: "/operations/break-glass", reason: "incident-42 emergency recovery", confirmation: operationBreakGlassValue, wantStatus: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, nil)
			if test.reason != "" {
				request.Header.Set(operationReasonHeader, test.reason)
			}
			if test.confirmation != "" {
				request.Header.Set(operationConfirmationHeader, test.confirmation)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestHighRiskOperationPolicyDoesNotApplyToReadsOrUnclassifiedFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /operations/scheduler/state", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	handler := (&HTTPRouter{}).withHighRiskOperationPolicy(mux, mux)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/operations/scheduler/state", nil),
		httptest.NewRequest(http.MethodGet, "/not-registered", nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusBadRequest {
			t.Fatalf("%s %s unexpectedly entered high-risk mutation gate", request.Method, request.URL.Path)
		}
	}
}

func TestHighRiskOperationPolicyDecodesUTF8Reason(t *testing.T) {
	mux := http.NewServeMux()
	var received string
	mux.HandleFunc("POST /operations/scheduler/definitions/{definitionID}/run", func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get(operationReasonHeader)
		w.WriteHeader(http.StatusNoContent)
	})
	handler := (&HTTPRouter{}).withHighRiskOperationPolicy(mux, mux)

	request := httptest.NewRequest(http.MethodPost, "/operations/scheduler/definitions/job-1/run", nil)
	request.Header.Set(operationReasonHeader, "UTF-8''%E8%A1%A5%E5%85%85%E7%BB%93%E7%AE%97%E6%98%8E%E7%BB%86")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if received != "补充结算明细" {
		t.Fatalf("decoded reason=%q", received)
	}
}

func TestHighRiskOperationPolicyRejectsInvalidUTF8ReasonEncoding(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /operations/scheduler/definitions/{definitionID}/run", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := (&HTTPRouter{}).withHighRiskOperationPolicy(mux, mux)

	request := httptest.NewRequest(http.MethodPost, "/operations/scheduler/definitions/job-1/run", nil)
	request.Header.Set(operationReasonHeader, "UTF-8''%zz")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}
