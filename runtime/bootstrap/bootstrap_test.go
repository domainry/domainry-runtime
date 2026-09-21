package bootstrap

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/domainry/domainry-connector-sdk"
	integrationmodule "github.com/domainry/domainry-integration/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	dataexchangefixture "github.com/domainry/domainry-runtime/testsupport/dataexchangefixture"
)

func TestBootstrapEntrypointsAssembleRunnableGraphs(t *testing.T) {
	cfg := config.Config{
		AppLocale:               "en-US",
		IdentityWorkspaceID:     "workspace-primary",
		NotificationWorkspaceID: "workspace-primary",

		DatabaseDriver:             "sqlite",
		AuditExportTokenKey:        "test-audit-export-signing-key",
		DBPath:                     filepath.Join(t.TempDir(), "runtime.db"),
		ManifestPath:               filepath.Join("..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json"),
		UploadDir:                  filepath.Join(t.TempDir(), "uploads"),
		SchedulerPollInterval:      5 * time.Millisecond,
		SchedulerBatchSize:         5,
		HTTPShutdownTimeout:        10 * time.Second,
		SchedulerLeaseTTL:          time.Minute,
		SchedulerMaxCatchupWindows: 1,
	}
	runtime := New(t.Context(), cfg, bootstrapIdentityBindingStub{}, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory())
	if runtime == nil || BindHTTP(t.Context(), runtime) != runtime {
		t.Fatal("bootstrap runtime entrypoints did not preserve the assembled owner")
	}
	StartWorkers(t.Context(), runtime)
	StartWorkers(t.Context(), runtime)
	assertBootstrapLiveness(t, runtime.Routes())
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatalf("repeated close: %v", err)
	}

	server := AssembleHTTPServer(t.Context(), composition.NewRuntimeServices(t.Context(), composition.RuntimeServicesConfig{}), bootstrapIdentityBindingStub{}, t.TempDir(), nil, true)
	if server == nil {
		t.Fatal("HTTP composition entrypoint returned nil")
	}
	assertBootstrapLiveness(t, server.Routes())
}

func TestBootstrapExtensionAndProjectFacadeEntrypoints(t *testing.T) {
	base := config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", ManifestPath: filepath.Join("..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json"), UploadDir: t.TempDir(), HTTPShutdownTimeout: time.Second,
		IdentityWorkspaceID: "workspace-primary", NotificationWorkspaceID: "workspace-primary",

		AuditExportTokenKey: "test-audit-export-signing-key",
	}
	handlers := runtimeext.NewProjectExtensionRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	constructors := []func(config.Config) *Runtime{
		func(cfg config.Config) *Runtime {
			return NewWithProjectExtensions(t.Context(), cfg, handlers, bootstrapIdentityBindingStub{}, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory())
		},
		func(cfg config.Config) *Runtime {
			return NewWithExtensions(t.Context(), cfg, handlers, connectors, bootstrapIdentityBindingStub{}, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory())
		},
		func(cfg config.Config) *Runtime {
			return NewVerifiedProjectWithIdentity(t.Context(), cfg, handlers, connectors, runtimehttp.RuntimeReleaseIdentity{}, RuntimeReleaseArtifactEvidence{}, bootstrapIdentityBindingStub{}, notificationmodule.NewFactory(notificationmodule.OptionsFromEnvironment()), dataexchangefixture.NewFactory(), integrationmodule.NewFactory())
		},
	}
	for index, constructor := range constructors {
		cfg := base
		cfg.DBPath = filepath.Join(t.TempDir(), "runtime.db")
		runtime := constructor(cfg)
		if runtime == nil {
			t.Fatalf("constructor %d returned nil", index)
		}
		if RoutesForListenerGroup(runtime, runtimehttp.ListenerRouteGroupPublic) == nil {
			t.Fatalf("constructor %d routes nil", index)
		}
		if err := runtime.CloseContext(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if RoutesForListenerGroup(nil, runtimehttp.ListenerRouteGroupPublic) == nil {
		t.Fatal("nil facade fallback missing")
	}
}

func assertBootstrapLiveness(t *testing.T, handler http.Handler) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("liveness status=%d body=%s", response.Code, response.Body.String())
	}
}
