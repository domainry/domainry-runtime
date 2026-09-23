package runtime

import (
	"testing"

	"github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	workspaceprovision "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestConstructRuntimeMapsProcessState(t *testing.T) {
	handlers := runtimeext.NewProjectExtensionRegistry()
	handlers.Freeze()
	connectors := connector.NewRegistry()
	connectors.Freeze()
	rolePolicy := workspaceprovision.WorkspaceBootstrapRolePolicyEvidence{
		RoleCatalogSHA256: "catalog-digest", InitialWorkspaceAdministratorRoleKey: "crm_acceptance_admin",
	}
	runtime := constructRuntime(runtimeConstructionInput{
		config:              config.Config{RuntimeVersion: "test-version"},
		templateID:          "template",
		projectModel:        projectmodel.RuntimeModel{ProjectKey: "project", ContentHash: "model-hash"},
		workspaceRolePolicy: rolePolicy,
		projectExtensions:   handlers,
		connectorProviders:  connectors,
	})
	if runtime.cfg.RuntimeVersion != "test-version" || runtime.templateID != "template" {
		t.Fatalf("runtime identity = %#v", runtime)
	}
	if runtime.projectModel.ContentHash != "model-hash" {
		t.Fatalf("runtime project model = %#v", runtime.projectModel)
	}
	if runtime.workspaceRolePolicy != rolePolicy {
		t.Fatalf("workspace role policy = %#v", runtime.workspaceRolePolicy)
	}
	if runtime.projectExtensions != handlers {
		t.Fatal("runtime did not retain the frozen project extension registry")
	}
	if runtime.connectorProviders != connectors {
		t.Fatal("runtime did not retain the frozen connector provider registry")
	}
}
