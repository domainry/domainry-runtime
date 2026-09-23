package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type runtimeReleaseIntegrityStub struct {
	build, signature, schema, registry error
}

func (s runtimeReleaseIntegrityStub) BuildReadiness(context.Context) error     { return s.build }
func (s runtimeReleaseIntegrityStub) SignatureReadiness(context.Context) error { return s.signature }
func (s runtimeReleaseIntegrityStub) SchemaReadiness(context.Context) error    { return s.schema }
func (s runtimeReleaseIntegrityStub) RegistryReadiness(context.Context) error  { return s.registry }

func TestRuntimeReleaseAdmissionRejectsBusinessTrafficButKeepsProbes(t *testing.T) {
	router := NewHTTPRouter(HTTPRouterConfig{}, HTTPRouterDependencies{RuntimeReleaseAdmission: func() error { return errors.New("cohort lease lost") }})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, path := range []string{"/records/customer", "/automation/rules", "/"} {
		response := httptest.NewRecorder()
		router.withAdmission(nil, next).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "runtime.release_cohort_unavailable") {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/live", "/ready", "/startup", "/health", "/metrics"} {
		response := httptest.NewRecorder()
		router.withAdmission(nil, next).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("probe path=%s status=%d", path, response.Code)
		}
	}
}

func TestRuntimeReleaseIntegrityFactsAreIndependentCriticalReadinessChecks(t *testing.T) {
	cases := map[string]runtimeReleaseIntegrityStub{
		"release_build":     {build: errors.New("binary differs")},
		"release_signature": {signature: errors.New("signature differs")},
		"release_schema":    {schema: errors.New("schema differs")},
		"release_registry":  {registry: errors.New("registry differs")},
	}
	for checkName, integrity := range cases {
		t.Run(checkName, func(t *testing.T) {
			router := &HTTPRouter{
				healthRegistry: newRuntimeHealthRegistry(), healthCheckTimeout: 50 * time.Millisecond,
				runtimeReadiness: routerRuntimeStatusStub{}, runtimeReleaseIntegrity: integrity,
			}
			router.MarkStartupComplete()
			response := httptest.NewRecorder()
			router.ready(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
			if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"status\":\"unavailable\"}\n" {
				t.Fatalf("ready status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRuntimeReleaseAdmissionIsCriticalReadinessCheck(t *testing.T) {
	router := &HTTPRouter{
		healthRegistry: newRuntimeHealthRegistry(), healthCheckTimeout: 50 * time.Millisecond,
		runtimeReadiness: routerRuntimeStatusStub{}, runtimeReleaseAdmission: func() error { return errors.New("cohort lease lost") },
	}
	router.MarkStartupComplete()
	response := httptest.NewRecorder()
	router.ready(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"status\":\"unavailable\"}\n" {
		t.Fatalf("ready status=%d body=%s", response.Code, response.Body.String())
	}
}
