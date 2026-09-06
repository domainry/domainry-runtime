package runtime

import (
	"testing"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	workspaceprovision "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestConstructRuntimeMapsProcessState(t *testing.T) {
	handlers := runtimeext.NewBusinessHandlerRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	rolePolicy := workspaceprovision.WorkspaceBootstrapRolePolicyEvidence{
		RoleCatalogSHA256: "catalog-digest", InitialWorkspaceAdministratorRoleKey: "crm_acceptance_admin",
	}
	runtime := constructRuntime(runtimeConstructionInput{
		config:              config.Config{RuntimeVersion: "test-version"},
		templateID:          "template",
		manifest:            manifestmodel.ManifestSchema{TemplateID: "template", Version: "1.0.0"},
		workspaceRolePolicy: rolePolicy,
		businessHandlers:    handlers,
		connectorProviders:  connectors,
	})
	if runtime.cfg.RuntimeVersion != "test-version" || runtime.templateID != "template" {
		t.Fatalf("runtime identity = %#v", runtime)
	}
	if runtime.manifest.Version != "1.0.0" {
		t.Fatalf("runtime manifest = %#v", runtime.manifest)
	}
	if runtime.workspaceRolePolicy != rolePolicy {
		t.Fatalf("workspace role policy = %#v", runtime.workspaceRolePolicy)
	}
	if runtime.businessHandlers != handlers {
		t.Fatal("runtime did not retain the frozen business handler registry")
	}
	if runtime.connectorProviders != connectors {
		t.Fatal("runtime did not retain the frozen connector provider registry")
	}
}
