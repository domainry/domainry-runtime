package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
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

func TestReadinessLogsOnlyFailedCriticalCheckNamesAndReasons(t *testing.T) {
	core, observed := observer.New(zap.WarnLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(restore)
	router := &HTTPRouter{
		healthRegistry: newRuntimeHealthRegistry(), healthCheckTimeout: time.Second,
		runtimeReadiness:        routerRuntimeStatusStub{},
		runtimeReleaseAdmission: func() error { return errors.New("private-lease-diagnostic") },
	}
	router.MarkStartupComplete()
	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()
	router.ready(response, request)
	entries := observed.FilterMessage("runtime_readiness_failed").All()
	if response.Code != http.StatusServiceUnavailable || len(entries) != 1 {
		t.Fatalf("response status %d, readiness log count %d", response.Code, len(entries))
	}
	fields := fmt.Sprint(entries[0].ContextMap())
	if !strings.Contains(fields, "release_cohort") || !strings.Contains(fields, "check_failed") ||
		strings.Contains(fields, "private-lease-diagnostic") || strings.Contains(fields, "migration") {
		t.Fatalf("readiness log fields = %s", fields)
	}
	router.runtimeReleaseAdmission = nil
	response = httptest.NewRecorder()
	router.ready(response, request)
	if response.Code != http.StatusOK || observed.FilterMessage("runtime_readiness_failed").Len() != 1 {
		t.Fatal("healthy readiness logged a failure")
	}
}

func TestRuntimeReleaseAdmissionFailureFailsLivenessForOrchestratorRestart(t *testing.T) {
	router := &HTTPRouter{runtimeReleaseAdmission: func() error { return errors.New("cohort lease lost") }}
	response := httptest.NewRecorder()
	router.live(response, httptest.NewRequest(http.MethodGet, "/live", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "runtime.release_cohort_unavailable") {
		t.Fatalf("live status=%d body=%s", response.Code, response.Body.String())
	}

	healthy := &HTTPRouter{runtimeReleaseAdmission: func() error { return nil }}
	response = httptest.NewRecorder()
	healthy.live(response, httptest.NewRequest(http.MethodGet, "/live", nil))
	if response.Code != http.StatusOK || response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("healthy live status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExpiredRuntimeReleaseBlocksReadinessAndTrafficWithoutFailingLiveness(t *testing.T) {
	router := &HTTPRouter{
		healthRegistry: newRuntimeHealthRegistry(), healthCheckTimeout: 50 * time.Millisecond,
		runtimeReadiness:        routerRuntimeStatusStub{},
		runtimeReleaseAdmission: func() error { return deploymentmodel.ErrRuntimeReleaseLeaseExpired },
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	router.MarkStartupComplete()
	for _, test := range []struct {
		path    string
		handler http.HandlerFunc
		want    int
	}{
		{"/live", router.live, http.StatusOK},
		{"/ready", router.ready, http.StatusServiceUnavailable},
		{"/records/customer", router.withAdmission(nil, next).ServeHTTP, http.StatusServiceUnavailable},
	} {
		response := httptest.NewRecorder()
		test.handler(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.want {
			t.Errorf("%s = %d, want %d", test.path, response.Code, test.want)
		}
	}
}
