package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
)

// Use the real Identity publisher and projection. Pre-populating a role stub
// hides the upgrade where a workflow first references a new service role.
func TestRuntimePublishesServiceRolesBeforeWorkflowInitialization(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_database=%t", existing), func(t *testing.T) {
			cfg := bootstrapTestConfig(t)
			cfg.ManifestPath = filepath.Join(t.TempDir(), "manifest.json")
			cfg.DefinitionUpgradeMode = "apply"
			identityPath := filepath.Join(t.TempDir(), "identity.db")
			start := func() (*Runtime, identitysdk.Binding) {
				binding, err := identitymodule.NewFactory(identitymodule.Options{DatabaseDriver: "sqlite", DatabasePath: identityPath}).Open(t.Context(), identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = binding.Close(context.Background()) })
				application := New(t.Context(), cfg, binding, runtimeTestNotificationFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
				t.Cleanup(func() { _ = application.CloseContext(context.Background()) })
				return application, binding
			}
			if existing {
				writeWorkflowRoleStartupManifest(t, cfg.ManifestPath, false, "")
				previous, binding := start()
				roles, err := binding.Projection().ListRoles(t.Context(), identitysdk.ProjectionQuery{})
				if err != nil {
					t.Fatal(err)
				}
				for _, role := range roles {
					if role.Key == "approval_worker" {
						t.Fatal("upgrade fixture already contains the new role")
					}
				}
				if err := previous.CloseContext(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			writeWorkflowRoleStartupManifest(t, cfg.ManifestPath, true, "approval_worker")
			application, binding := start()
			roles, err := binding.Projection().ListRoles(t.Context(), identitysdk.ProjectionQuery{})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, role := range roles {
				if role.Key == "approval_worker" {
					found = true
				}
			}
			if !found {
				t.Fatal("new service role was not published")
			}
			for _, role := range application.manifest.Roles {
				if role.Key == "approval_worker" && (role.Audience != "service" || role.AssignmentMode != "system_managed" || role.ProvisionToWorkspaces) {
					t.Fatalf("service role safety facts changed: %+v", role)
				}
			}
			definition, found, err := workflowpersistence.NewWorkflowDefinitionStore(application.store).GetDefinitionByKey(t.Context(), "document.approval")
			if err != nil || !found || !definition.Enabled || definition.CurrentPublishedVersionID == "" {
				t.Fatalf("workflow was not activated: definition=%+v found=%t err=%v", definition, found, err)
			}
		})
	}
}

func TestRuntimeWorkflowStartupStillRejectsUnknownServiceRole(t *testing.T) {
	cfg := bootstrapTestConfig(t)
	cfg.ManifestPath = filepath.Join(t.TempDir(), "manifest.json")
	writeWorkflowRoleStartupManifest(t, cfg.ManifestPath, true, "missing_worker")
	binding, err := identitymodule.NewFactory(identitymodule.Options{DatabaseDriver: "sqlite", DatabasePath: filepath.Join(t.TempDir(), "identity.db")}).Open(t.Context(), identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)})
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close(context.Background())
	defer func() {
		failure := recover()
		if failure == nil || !strings.Contains(fmt.Sprint(failure), "backend.workflow.run_as_role_not_found") {
			t.Fatalf("unexpected startup rejection: %v", failure)
		}
	}()
	application := New(t.Context(), cfg, binding, runtimeTestNotificationFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	_ = application.CloseContext(t.Context())
}

func writeWorkflowRoleStartupManifest(t *testing.T, path string, withWorkflow bool, runAs string) {
	t.Helper()
	manifest := manifestmodel.ManifestSchema{
		TemplateID: "workflow_role_startup", Version: "1", Name: "Workflow role startup", SchemaVersion: "2",
		Objects: []definitionmodel.ObjectSchema{{Key: "document", Name: "Document", Fields: []definitionmodel.FieldSchema{{Key: "title", Name: "Title", Type: "text"}}}},
		Roles:   []manifestmodel.RoleSchema{{Key: "operator", Name: "Operator", Audience: "user", AssignmentMode: "manual", Permissions: []manifestmodel.RolePermission{{PermissionKey: "document.read", DataScope: identitysdk.DataScopeAll}}}},
	}
	if withWorkflow {
		manifest.Version = "2"
		manifest.Roles = append(manifest.Roles, manifestmodel.RoleSchema{Key: "approval_worker", Name: "Approval worker", Audience: "service", AssignmentMode: "system_managed", Permissions: []manifestmodel.RolePermission{{PermissionKey: "document.finalize", DataScope: identitysdk.DataScopeAll}}})
		manifest.Actions = []definitionmodel.ActionSchema{{Key: "document.finalize", ObjectKey: "document", Label: "Finalize", Kind: "record_update", AuditEvent: "document_finalized"}}
		manifest.Workflows = []definitionmodel.WorkflowSchema{{
			Key: "document.approval", Name: "Document approval", Enabled: true, RunAs: runAs,
			Trigger: map[string]any{"type": "manual"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Action: map[string]any{"type": "workflow_graph"},
			Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
				{ID: "start", Type: "trigger", Name: "Start"},
				{ID: "finalize", Type: "action", Name: "Finalize", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "document.finalize", ObjectKey: "document", OnError: "fail"}}},
			}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start-finalize", Source: "start", Target: "finalize"}}},
		}}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
