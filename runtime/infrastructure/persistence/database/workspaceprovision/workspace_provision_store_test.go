package workspaceprovision

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type identityParticipantProbe struct{}

func (identityParticipantProbe) ProvisionWorkspaceIdentity(ctx context.Context, request identitysdk.WorkspaceIdentityProvisionRequest, transaction identitysdk.EmbeddedTransaction) (identitysdk.WorkspaceIdentityProvisionResult, error) {
	tx, ok := transaction.Native.(*sql.Tx)
	if !ok {
		return identitysdk.WorkspaceIdentityProvisionResult{}, errors.New("host transaction unavailable")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO "identity_provision_probe" ("workspace_id", "admin_login_id") VALUES (?, ?)`, request.WorkspaceID, request.AdminLoginID); err != nil {
		return identitysdk.WorkspaceIdentityProvisionResult{}, err
	}
	if transaction.WorkspaceProvisionFailures != nil {
		for _, point := range []string{
			identitysdk.WorkspaceProvisionFailureAfterIdentityUser,
			identitysdk.WorkspaceProvisionFailureAfterIdentityRole,
			identitysdk.WorkspaceProvisionFailureAfterRoleAssignment,
			identitysdk.WorkspaceProvisionFailureAfterCredential,
		} {
			if err := transaction.WorkspaceProvisionFailures.InjectWorkspaceProvisionFailure(point); err != nil {
				return identitysdk.WorkspaceIdentityProvisionResult{}, err
			}
		}
	}
	return identitysdk.WorkspaceIdentityProvisionResult{AdminLoginID: request.AdminLoginID, InitialPassword: "OneTime1!", MustChangePassword: true, ProvisionedRoles: 1}, nil
}

func (identityParticipantProbe) ReconcileWorkspaceRoles(context.Context, identitysdk.WorkspaceRoleReconcileRequest, identitysdk.EmbeddedTransaction) (identitysdk.WorkspaceRoleReconcileResult, error) {
	return identitysdk.WorkspaceRoleReconcileResult{ProvisionedRoles: 2}, nil
}

type failureInjectorFunc func(string) error

func (inject failureInjectorFunc) Inject(point string) error { return inject(point) }

func TestWorkspaceProvisioningIsAtomicAndIdempotent(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workspace-provisioning.db"), IntegrationSecretKey: "workspace-provisioning-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseContext(t.Context()) })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE "identity_provision_probe" ("workspace_id" TEXT PRIMARY KEY, "admin_login_id" TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	failure := errors.New("after Identity participant")
	failed := &WorkspaceProvisionStore{runtime: store, identity: identityParticipantProbe{}, failures: failureInjectorFunc(func(point string) error {
		if point == FailureAfterCredential {
			return failure
		}
		return nil
	})}
	request := workspaceprovisionmodel.Request{RequestID: "request-failed", TenantCode: "North_Store", TenantName: "North Store", AdminLoginID: "OWNER@NORTH.EXAMPLE", AdminName: "Owner", StoreConfiguration: map[string]any{"currency": "CNY"}}
	if _, err := failed.Initialize(t.Context(), request, "BootstrapAdmin1!"); !errors.Is(err, failure) {
		t.Fatalf("failure=%v", err)
	}
	for _, table := range []string{"_workspaces", "_tenant_registry", "_tenant_installation", "_workspace_configuration", "_workspace_provisioning_receipts", "identity_provision_probe"} {
		assertWorkspaceProvisionRowCount(t, store, table, 0)
	}

	repository := &WorkspaceProvisionStore{runtime: store, identity: identityParticipantProbe{}}
	request.RequestID = "request-success"
	result, err := repository.Initialize(t.Context(), request, "BootstrapAdmin1!")
	if err != nil {
		t.Fatal(err)
	}
	if result.CanonicalCode != "north-store" || result.InitialPassword != "OneTime1!" || result.Replayed || !result.MustChangePassword {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, table := range []string{"_workspaces", "_tenant_registry", "_tenant_installation", "_workspace_configuration", "_workspace_provisioning_receipts", "identity_provision_probe"} {
		assertWorkspaceProvisionRowCount(t, store, table, 1)
	}
	replay, err := repository.Initialize(t.Context(), request, "BootstrapAdmin1!")
	if err != nil || !replay.Replayed || replay.WorkspaceID != result.WorkspaceID || replay.InitialPassword != result.InitialPassword {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	request.TenantName = "Different"
	if _, err := repository.Initialize(t.Context(), request, "BootstrapAdmin1!"); !errors.Is(err, workspaceprovisionmodel.ErrIdempotencyConflict) {
		t.Fatalf("conflict=%v", err)
	}
	reconciled, err := repository.ReconcileWorkspaceRoles(t.Context(), result.WorkspaceID)
	if err != nil || reconciled.ProvisionedRoles != 2 {
		t.Fatalf("reconciled=%#v err=%v", reconciled, err)
	}
}

func TestTenantInitializationRejectsLegacyWorkspacesWithoutInstallationMarker(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "legacy-workspace.db"), IntegrationSecretKey: "workspace-provisioning-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseContext(t.Context()) })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO "_workspaces" ("id", "canonical_code", "name", "status", "created_at", "updated_at") VALUES ('workspace-legacy', 'legacy', 'Legacy', 'active', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}

	if _, found, err := LoadInstallation(t.Context(), store); err == nil || found || !strings.Contains(err.Error(), "explicit migration is required") {
		t.Fatalf("legacy installation found=%v error=%v", found, err)
	}
	request := workspaceprovisionmodel.Request{RequestID: "request-initial", TenantCode: "primary", TenantName: "Primary", AdminLoginID: "owner@example.test", AdminName: "Owner", StoreConfiguration: map[string]any{}}
	if _, err := (&WorkspaceProvisionStore{runtime: store, identity: identityParticipantProbe{}}).Initialize(t.Context(), request, "BootstrapAdmin1!"); err == nil || !strings.Contains(err.Error(), "explicit migration is required") {
		t.Fatalf("initialization error=%v", err)
	}
	assertWorkspaceProvisionRowCount(t, store, "_workspaces", 1)
	assertWorkspaceProvisionRowCount(t, store, "_tenant_installation", 0)
}

func TestProvisionAcceptanceFailurePointsRollbackEveryBoundaryAndAllowCleanRetry(t *testing.T) {
	points := []string{
		FailureAfterWorkspace,
		FailureAfterTenantRegistry,
		FailureAfterInstallation,
		FailureAfterIdentityUser,
		FailureAfterIdentityRole,
		FailureAfterRoleAssignment,
		FailureAfterCredential,
		FailureAfterWorkspaceConfiguration,
		FailureAfterApplicationProjection + "headquarters_tenant",
		FailureAfterApplicationProjection + "workspace_config",
		FailureAfterApplicationProjections,
		FailureAfterReceipt,
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workspace-provisioning.db"), IntegrationSecretKey: "workspace-provisioning-test"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.CloseContext(t.Context()) })
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			statements := []string{
				`CREATE TABLE "identity_provision_probe" ("workspace_id" TEXT PRIMARY KEY, "admin_login_id" TEXT NOT NULL)`,
				`CREATE TABLE ` + store.TableIdentifier("tenant") + ` ("workspace_id" TEXT NOT NULL,"id" TEXT NOT NULL,"created_at" TEXT NOT NULL,"updated_at" TEXT NOT NULL,"name" TEXT NOT NULL,"code" TEXT NOT NULL)`,
				`CREATE TABLE ` + store.TableIdentifier("store_config") + ` ("workspace_id" TEXT NOT NULL,"id" TEXT NOT NULL,"created_at" TEXT NOT NULL,"updated_at" TEXT NOT NULL,"name" TEXT NOT NULL,"config_json" TEXT)`,
			}
			for _, statement := range statements {
				if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			manifest := manifestmodel.ManifestSchema{
				Objects: []definitionmodel.ObjectSchema{
					{Key: "tenant", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}, {Key: "code", Type: "text", Required: true}}},
					{Key: "store_config", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}, {Key: "config_json", Type: "long_text"}}},
				},
				WorkspaceProvisioning: []manifestmodel.WorkspaceProvisionProjection{
					{Key: "headquarters_tenant", ObjectKey: "tenant", Scope: "headquarters", Data: map[string]any{"name": "$provision.tenant_name", "code": "$provision.canonical_code"}},
					{Key: "workspace_config", ObjectKey: "store_config", Scope: "provisioned_workspace", Data: map[string]any{"name": "$provision.tenant_name", "config_json": "$provision.configuration_json"}},
				},
			}
			request := workspaceprovisionmodel.Request{RequestID: "acceptance-failure", TenantCode: "acceptance-store", TenantName: "Acceptance Store", AdminLoginID: "owner@example.test", AdminName: "Owner", StoreConfiguration: map[string]any{"currency": "CNY"}}
			failedRepository := &WorkspaceProvisionStore{runtime: store, identity: identityParticipantProbe{}, manifest: manifest, failures: NewAcceptanceFailureInjector(point)}
			failedResult, err := failedRepository.Initialize(t.Context(), request, "BootstrapAdmin1!")
			if !errors.Is(err, workspaceprovisionmodel.ErrAcceptanceFailure) || !reflect.DeepEqual(failedResult, workspaceprovisionmodel.Result{}) {
				t.Fatalf("result=%#v error=%v", failedResult, err)
			}
			if err.Error() != "workspace provisioning acceptance failure" {
				t.Fatalf("failure disclosed transaction identity: %q", err)
			}
			if _, leaked := failedRepository.issued.Load(request.RequestID); leaked {
				t.Fatal("failed transaction retained its one-time credential")
			}
			for _, table := range []string{"_workspaces", "_tenant_registry", "_tenant_installation", "_workspace_configuration", "_workspace_provisioning_receipts", "identity_provision_probe", "tenant", "store_config"} {
				assertWorkspaceProvisionRowCount(t, store, table, 0)
			}

			retryRepository := &WorkspaceProvisionStore{runtime: store, identity: identityParticipantProbe{}, manifest: manifest}
			retried, err := retryRepository.Initialize(t.Context(), request, "BootstrapAdmin1!")
			if err != nil || retried.WorkspaceID == "" || retried.TenantRegistryID == "" || retried.InitialPassword == "" || retried.Replayed || len(retried.ProjectionIDs) != 2 {
				t.Fatalf("clean retry result=%#v error=%v", retried, err)
			}
			for _, table := range []string{"_workspaces", "_tenant_registry", "_tenant_installation", "_workspace_configuration", "_workspace_provisioning_receipts", "identity_provision_probe", "tenant", "store_config"} {
				assertWorkspaceProvisionRowCount(t, store, table, 1)
			}
		})
	}
}

func TestProvisionReceiptRestoresExactProjectionIDsAfterDatabaseReopenAndManifestEvolution(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "workspace-provisioning.db")
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath, IntegrationSecretKey: "workspace-provisioning-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE "identity_provision_probe" ("workspace_id" TEXT PRIMARY KEY, "admin_login_id" TEXT NOT NULL)`,
		`CREATE TABLE ` + store.TableIdentifier("tenant") + ` ("workspace_id" TEXT NOT NULL,"id" TEXT NOT NULL,"created_at" TEXT NOT NULL,"updated_at" TEXT NOT NULL,"name" TEXT NOT NULL)`,
		`CREATE TABLE ` + store.TableIdentifier("store_config") + ` ("workspace_id" TEXT NOT NULL,"id" TEXT NOT NULL,"created_at" TEXT NOT NULL,"updated_at" TEXT NOT NULL,"name" TEXT NOT NULL)`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			_ = store.CloseContext(t.Context())
			t.Fatal(err)
		}
	}
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{
			{Key: "tenant", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}}},
			{Key: "store_config", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true}}},
		},
		WorkspaceProvisioning: []manifestmodel.WorkspaceProvisionProjection{{Key: "tenant", ObjectKey: "tenant", Scope: "headquarters", Data: map[string]any{"name": "$provision.tenant_name"}}},
	}
	request := workspaceprovisionmodel.Request{RequestID: "persistent-receipt", TenantCode: "persistent-store", TenantName: "Persistent Store", AdminLoginID: "owner@example.test", AdminName: "Owner", StoreConfiguration: map[string]any{}}
	created, err := (&WorkspaceProvisionStore{runtime: store, identity: identityParticipantProbe{}, manifest: manifest}).Initialize(t.Context(), request, "BootstrapAdmin1!")
	if err != nil || len(created.ProjectionIDs) != 1 {
		_ = store.CloseContext(t.Context())
		t.Fatalf("created=%#v error=%v", created, err)
	}
	if err := store.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	reopened, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath, IntegrationSecretKey: "workspace-provisioning-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.CloseContext(t.Context()) })
	if err := reopened.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	evolved := manifest
	evolved.WorkspaceProvisioning = append(evolved.WorkspaceProvisioning, manifestmodel.WorkspaceProvisionProjection{Key: "workspace_config", ObjectKey: "store_config", Scope: "provisioned_workspace", Data: map[string]any{"name": "$provision.tenant_name"}})
	replay, err := (&WorkspaceProvisionStore{runtime: reopened, identity: identityParticipantProbe{}, manifest: evolved}).Initialize(t.Context(), request, "BootstrapAdmin1!")
	if err != nil || !replay.Replayed || replay.InitialPassword != "" || !reflect.DeepEqual(replay.ProjectionIDs, created.ProjectionIDs) {
		t.Fatalf("replay=%#v created=%#v error=%v", replay, created, err)
	}
	assertWorkspaceProvisionRowCount(t, reopened, "tenant", 1)
	assertWorkspaceProvisionRowCount(t, reopened, "store_config", 0)
}

func assertWorkspaceProvisionRowCount(t *testing.T, store *database.RuntimeStore, table string, want int) {
	t.Helper()
	var count int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.TableIdentifier(table)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count=%d want=%d", table, count, want)
	}
}
