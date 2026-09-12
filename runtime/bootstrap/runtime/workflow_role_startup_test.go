package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
			assertWorkflowWorkloadStartup(t, binding, identityPath, cfg.IdentityWorkspaceID, cfg.IdentityAudience, definition.CurrentPublishedVersionID)
		})
	}
}

func assertWorkflowWorkloadStartup(t *testing.T, binding identitysdk.Binding, identityPath, workspaceID, applicationKey, definitionVersionID string) {
	t.Helper()
	probe, err := sql.Open("sqlite", identityPath)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	var subjectID, persistedVersionID, roleKey, actionKeysJSON, releaseID, releaseDigest, sourceKind, sourceID, status string
	var definitionVersion int
	if err := probe.QueryRowContext(t.Context(), `SELECT subject_id, definition_version_id, definition_version, role_key, action_keys_json, release_id, release_digest, source_kind, source_id, status
		FROM _identity_workflow_workload_bindings WHERE workspace_id=? AND application_key=? AND workflow_key=?`, workspaceID, applicationKey, "document.approval").Scan(
		&subjectID, &persistedVersionID, &definitionVersion, &roleKey, &actionKeysJSON, &releaseID, &releaseDigest, &sourceKind, &sourceID, &status,
	); err != nil {
		t.Fatalf("load workflow workload binding: %v", err)
	}
	var actionKeys []string
	if err := json.Unmarshal([]byte(actionKeysJSON), &actionKeys); err != nil {
		t.Fatal(err)
	}
	if subjectID != "workflow:document.approval" || persistedVersionID != definitionVersionID || definitionVersion != 1 || roleKey != "approval_worker" || len(actionKeys) != 1 || actionKeys[0] != "document.finalize" || releaseID != identitysdk.WorkflowWorkloadReleaseID(releaseDigest) || len(releaseDigest) != 64 || sourceKind != "deployment_control_plane" || sourceID != applicationKey || status != identitysdk.WorkflowWorkloadBindingActive {
		t.Fatalf("unexpected workflow workload binding: subject=%q version_id=%q version=%d role=%q actions=%v release=%q digest=%q source=%q/%q status=%q", subjectID, persistedVersionID, definitionVersion, roleKey, actionKeys, releaseID, releaseDigest, sourceKind, sourceID, status)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM _identity_users WHERE workspace_id=? AND id=?`,
		`SELECT COUNT(*) FROM _identity_user_role_assignments WHERE workspace_id=? AND user_id=?`,
	} {
		var count int
		if err := probe.QueryRowContext(t.Context(), query, workspaceID, subjectID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("workflow subject leaked into user identity tables: count=%d err=%v", count, err)
		}
	}
	workloadBinding, ok := binding.(identitysdk.WorkflowWorkloadIdentityBinding)
	if !ok || workloadBinding.WorkflowWorkloads() == nil {
		t.Fatal("Identity binding does not expose workflow workload identity")
	}
	resolvedBinding, err := workloadBinding.WorkflowWorkloads().GetWorkflowWorkloadBinding(t.Context(), identitysdk.GetWorkflowWorkloadBindingRequest{
		Application: identitysdk.ApplicationScope{WorkspaceID: identitysdk.WorkspaceID(workspaceID), ApplicationKey: identitysdk.ApplicationKey(applicationKey)},
		WorkflowKey: "document.approval", DefinitionVersionID: definitionVersionID, ReleaseDigest: releaseDigest,
	})
	if err != nil || resolvedBinding.SubjectID != identitysdk.SubjectID(subjectID) || resolvedBinding.RoleKey != roleKey {
		t.Fatalf("resolve workflow workload binding=%+v err=%v", resolvedBinding, err)
	}
	resolution, err := binding.Principals().Resolve(t.Context(), identitysdk.PrincipalResolutionRequest{
		SubjectID: identitysdk.SubjectID(subjectID), RoleKey: roleKey,
		Workload: &identitysdk.WorkflowWorkloadResolution{
			WorkflowKey: "document.approval", DefinitionVersionID: definitionVersionID, DefinitionVersion: definitionVersion,
			ReleaseID: releaseID, ReleaseDigest: releaseDigest, TaskID: "startup-task", SourceEventID: "startup-event", InitiatorSubjectID: "operator-1",
		},
	})
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	if err != nil || !resolution.Principal.Known || resolution.Principal.UserID != subjectID || resolution.Principal.Workload == nil || resolution.Principal.Workload.TaskID != "startup-task" || resolution.Principal.Workload.SourceEventID != "startup-event" || resolution.Principal.Workload.InitiatorSubjectID != "operator-1" || !resolution.Principal.HasPermission("document.finalize") {
		t.Fatalf("resolve workflow workload principal=%+v err=%v", resolution, err)
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

func TestRuntimeSynchronizesFiveGymWorkflowWorkloadsOnFreshAndExistingDatabases(t *testing.T) {
	type workloadCase struct{ workflowKey, roleKey, actionKey string }
	cases := []workloadCase{
		{"booking_waitlist_promotion", "booking_orchestration_service", "ledger_entry.promote_waitlist"},
		{"booking_waitlist_offer_expiry", "booking_orchestration_service", "booking.manage_booking_lifecycle"},
		{"payment_reconciliation", "payment_settlement_service", "payment_transaction.reconcile_payment_transactions"},
		{"scheduled_lifecycle_reconciliation", "entitlement_lifecycle_service", "entitlement.reconcile_due_lifecycle"},
		{"motion_analysis_retention", "motion_retention_service", "motion_analysis.expire_due"},
	}
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
				writeGymWorkflowWorkloadManifest(t, cfg.ManifestPath, false)
				previous, _ := start()
				if err := previous.CloseContext(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			writeGymWorkflowWorkloadManifest(t, cfg.ManifestPath, true)
			application, binding := start()
			store := workflowpersistence.NewWorkflowDefinitionStore(application.store)
			probe, err := sql.Open("sqlite", identityPath)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			var count int
			if err := probe.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _identity_workflow_workload_bindings WHERE workspace_id=? AND application_key=? AND status='active'`, cfg.IdentityWorkspaceID, cfg.IdentityAudience).Scan(&count); err != nil || count != len(cases) {
				t.Fatalf("active Gym workload bindings=%d want=%d err=%v", count, len(cases), err)
			}
			workloadBinding := binding.(identitysdk.WorkflowWorkloadIdentityBinding).WorkflowWorkloads()
			for _, item := range cases {
				definition, found, err := store.GetDefinitionByKey(t.Context(), item.workflowKey)
				if err != nil || !found || definition.CurrentPublishedVersionID == "" {
					t.Fatalf("workflow %s definition=%+v found=%t err=%v", item.workflowKey, definition, found, err)
				}
				var releaseID, releaseDigest string
				if err := probe.QueryRowContext(t.Context(), `SELECT release_id, release_digest FROM _identity_workflow_workload_bindings WHERE workspace_id=? AND application_key=? AND workflow_key=?`, cfg.IdentityWorkspaceID, cfg.IdentityAudience, item.workflowKey).Scan(&releaseID, &releaseDigest); err != nil {
					t.Fatal(err)
				}
				current, err := workloadBinding.GetWorkflowWorkloadBinding(t.Context(), identitysdk.GetWorkflowWorkloadBindingRequest{
					WorkflowKey: item.workflowKey, DefinitionVersionID: definition.CurrentPublishedVersionID, ReleaseDigest: releaseDigest,
				})
				if err != nil || current.SubjectID != identitysdk.WorkflowWorkloadSubjectID(item.workflowKey) || current.RoleKey != item.roleKey || len(current.ActionKeys) != 1 || current.ActionKeys[0] != item.actionKey || current.ReleaseID != releaseID {
					t.Fatalf("workflow %s binding=%+v err=%v", item.workflowKey, current, err)
				}
				resolution, err := binding.Principals().Resolve(t.Context(), identitysdk.PrincipalResolutionRequest{
					SubjectID: current.SubjectID, RoleKey: current.RoleKey,
					Workload: &identitysdk.WorkflowWorkloadResolution{WorkflowKey: current.WorkflowKey, DefinitionVersionID: current.DefinitionVersionID, DefinitionVersion: current.DefinitionVersion, ReleaseID: current.ReleaseID, ReleaseDigest: current.ReleaseDigest},
				})
				resolution.Principal.AccessBundle = &resolution.AccessBundle
				if err != nil || !resolution.Principal.Known || resolution.Principal.UserID != string(current.SubjectID) || !resolution.Principal.HasPermission(item.actionKey) {
					t.Fatalf("workflow %s principal=%+v err=%v", item.workflowKey, resolution, err)
				}
			}
			if err := probe.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _identity_users WHERE workspace_id=? AND id LIKE 'workflow:%'`, cfg.IdentityWorkspaceID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("Gym workloads leaked into users: count=%d err=%v", count, err)
			}
		})
	}
}

func writeGymWorkflowWorkloadManifest(t *testing.T, path string, includeWorkflows bool) {
	t.Helper()
	manifest := manifestmodel.ManifestSchema{
		TemplateID: "gym_workflow_workloads", Version: "1", Name: "Gym workflow workloads", SchemaVersion: "2",
		Objects: []definitionmodel.ObjectSchema{
			{Key: "booking", Name: "Booking", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text"}}},
			{Key: "ledger_entry", Name: "Ledger entry", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text"}}},
			{Key: "payment_transaction", Name: "Payment transaction", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text"}}},
			{Key: "entitlement", Name: "Entitlement", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text"}}},
			{Key: "motion_analysis", Name: "Motion analysis", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text"}}},
		},
		Roles: []manifestmodel.RoleSchema{{Key: "operator", Name: "Operator", Audience: "user", AssignmentMode: "manual", Permissions: []manifestmodel.RolePermission{{PermissionKey: "booking.read", DataScope: identitysdk.DataScopeAll}}}},
	}
	if includeWorkflows {
		manifest.Version = "2"
		type workloadCase struct{ workflowKey, roleKey, actionKey, objectKey string }
		for _, item := range []workloadCase{
			{"booking_waitlist_promotion", "booking_orchestration_service", "ledger_entry.promote_waitlist", "ledger_entry"},
			{"booking_waitlist_offer_expiry", "booking_orchestration_service", "booking.manage_booking_lifecycle", "booking"},
			{"payment_reconciliation", "payment_settlement_service", "payment_transaction.reconcile_payment_transactions", "payment_transaction"},
			{"scheduled_lifecycle_reconciliation", "entitlement_lifecycle_service", "entitlement.reconcile_due_lifecycle", "entitlement"},
			{"motion_analysis_retention", "motion_retention_service", "motion_analysis.expire_due", "motion_analysis"},
		} {
			roleIndex := slices.IndexFunc(manifest.Roles, func(role manifestmodel.RoleSchema) bool { return role.Key == item.roleKey })
			if roleIndex < 0 {
				manifest.Roles = append(manifest.Roles, manifestmodel.RoleSchema{Key: item.roleKey, Name: item.roleKey, Audience: "service", AssignmentMode: "system_managed", Permissions: []manifestmodel.RolePermission{{PermissionKey: item.actionKey, DataScope: identitysdk.DataScopeAll}}})
			} else {
				manifest.Roles[roleIndex].Permissions = append(manifest.Roles[roleIndex].Permissions, manifestmodel.RolePermission{PermissionKey: item.actionKey, DataScope: identitysdk.DataScopeAll})
			}
			manifest.Actions = append(manifest.Actions, definitionmodel.ActionSchema{Key: item.actionKey, ObjectKey: item.objectKey, Label: item.actionKey, Kind: "record_update", AuditEvent: "action." + item.actionKey})
			manifest.Workflows = append(manifest.Workflows, definitionmodel.WorkflowSchema{
				Key: item.workflowKey, Name: item.workflowKey, Enabled: true, RunAs: item.roleKey,
				Trigger: map[string]any{"type": "manual"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}, Action: map[string]any{"type": "workflow_graph"},
				Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
					{ID: "start", Type: "trigger", Name: "Start"},
					{ID: "execute", Type: "action", Name: "Execute", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: item.actionKey, ObjectKey: item.objectKey, OnError: "fail"}}},
				}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start-execute", Source: "start", Target: "execute"}}},
			})
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
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
