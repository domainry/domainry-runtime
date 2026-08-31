package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	healthplatform "github.com/domainry/domainry-foundation/health"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

type probeReadinessRepository struct {
	pingErr   error
	migration deploymentmodel.MigrationStatus
}

type probeRuntimeStatus struct {
	*deploymentapplication.DeploymentRuntimeStatusApplicationService
}

func (probeRuntimeStatus) Health(context.Context) map[string]any {
	return map[string]any{"status": "ok"}
}

func (r probeReadinessRepository) Ping(context.Context) error { return r.pingErr }
func (r probeReadinessRepository) MigrationStatus(context.Context) (deploymentmodel.MigrationStatus, error) {
	return r.migration, nil
}

func TestLivenessDoesNotInvokeDependencies(t *testing.T) {
	router := &HTTPRouter{serviceKind: BusinessRuntimeServiceKind}
	response := httptest.NewRecorder()
	router.live(response, httptest.NewRequest(http.MethodGet, "/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("live status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHealthDiagnosticRequiresAuthentication(t *testing.T) {
	if anonymousAuthPath("/health") {
		t.Fatal("diagnostic health snapshot must not be anonymous")
	}
	for _, probe := range []string{"/live", "/ready", "/startup"} {
		if !anonymousAuthPath(probe) {
			t.Fatalf("orchestrator probe %s requires authentication", probe)
		}
	}
}

func TestProbeWriterSeparatesUnavailableStatus(t *testing.T) {
	router := &HTTPRouter{}
	response := httptest.NewRecorder()
	router.writeProbe(response, healthplatform.Snapshot{Status: "unavailable"})
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("probe status=%d", response.Code)
	}
}

func TestHealthRegistryCoversStartupDrainAndTimeoutState(t *testing.T) {
	registry := newRuntimeHealthRegistry()
	check := healthplatform.Check{Name: "database", Criticality: healthplatform.Critical, Timeout: time.Millisecond, Run: func(ctx context.Context) error { <-ctx.Done(); return errors.New("database detail must not escape") }}
	if snapshot := registry.Evaluate(t.Context(), []healthplatform.Check{check}); snapshot.Status != "unavailable" || snapshot.Checks[0].LastErrorCode != "timeout" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	registry.MarkStartupComplete()
	registry.SetDraining(true)
	if !registry.StartupComplete() || !registry.Draining() {
		t.Fatal("lifecycle state not retained")
	}
}

func TestReadinessReturnsUnavailableForDatabaseMigrationAndDrain(t *testing.T) {
	testCases := []struct {
		name       string
		repository probeReadinessRepository
		draining   bool
	}{
		{name: "database down", repository: probeReadinessRepository{pingErr: errors.New("dsn=secret database down"), migration: deploymentmodel.MigrationStatus{Current: true}}},
		{name: "migration pending", repository: probeReadinessRepository{migration: deploymentmodel.MigrationStatus{Current: false, Pending: 1}}},
		{name: "draining", repository: probeReadinessRepository{migration: deploymentmodel.MigrationStatus{Current: true}}, draining: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			status := deploymentapplication.NewDeploymentRuntimeStatusApplicationService(nil, nil, testCase.repository, nil, nil, nil, nil)
			router := &HTTPRouter{healthRegistry: newRuntimeHealthRegistry(), healthCheckTimeout: 50 * time.Millisecond, runtimeStatus: probeRuntimeStatus{status}}
			router.MarkStartupComplete()
			router.SetDraining(testCase.draining)
			response := httptest.NewRecorder()
			router.ready(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("ready status=%d body=%s", response.Code, response.Body.String())
			}
			if body := response.Body.String(); strings.Contains(body, "dsn=secret") || strings.Contains(body, "database down") {
				t.Fatalf("dependency error leaked into readiness response: %s", body)
			}
		})
	}
}

func TestStartupRequiresManifestAndInitializedWorkers(t *testing.T) {
	router := &HTTPRouter{healthRegistry: newRuntimeHealthRegistry(), healthCheckTimeout: 50 * time.Millisecond}
	response := httptest.NewRecorder()
	router.startup(response, httptest.NewRequest(http.MethodGet, "/startup", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("startup before initialization status=%d body=%s", response.Code, response.Body.String())
	}
	router.manifestTemplateID = "template"
	router.MarkStartupComplete()
	response = httptest.NewRecorder()
	router.startup(response, httptest.NewRequest(http.MethodGet, "/startup", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("startup after initialization status=%d body=%s", response.Code, response.Body.String())
	}
}
