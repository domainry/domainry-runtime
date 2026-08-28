package runtime

import (
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

func TestRuntimeServicesReusesCanonicalIntegrationApplication(t *testing.T) {
	application, _ := newIntegrationCompositionApp(t, "canonical")
	defer application.Close()
	canonical := application.records.Applications().Integrations
	if canonical == nil || application.records.Applications().Integrations != canonical {
		t.Fatalf("canonical=%p resolved=%p", canonical, application.records.Applications().Integrations)
	}
}

func newIntegrationCompositionApp(t *testing.T, name string, extraProviders ...connector.Adapter) (*Runtime, config.Config) {
	t.Helper()
	cfg := config.Config{
		AppLocale: "en-US", DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), name+".db"),
		ManifestPath: filepath.Join("..", "..", "domain", "manifest", "testdata", "manifests", "domain-only-minimal.json"), UploadDir: filepath.Join(t.TempDir(), "uploads"),
	}
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	providers := connector.NewRegistry()
	if len(extraProviders) > 0 {
		if err := providers.RegisterProviderSet(connector.ProviderSet{Providers: extraProviders}); err != nil {
			t.Fatal(err)
		}
	}
	providers.Freeze()
	return NewProjectWithIdentity(t.Context(), cfg, handlers, providers, runtimehttp.RuntimeReleaseIdentity{}, deploymentapplication.RuntimeReleaseArtifactEvidence{}, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory()), cfg
}
