package workspaceprovision

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
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
		if point == FailureAfterIdentity {
			return failure
		}
		return nil
	})}
	request := workspaceprovisionmodel.Request{RequestID: "request-failed", TenantCode: "North_Store", TenantName: "North Store", AdminLoginID: "OWNER@NORTH.EXAMPLE", AdminName: "Owner", StoreConfiguration: map[string]any{"currency": "CNY"}}
	if _, err := failed.Provision(t.Context(), request); !errors.Is(err, failure) {
		t.Fatalf("failure=%v", err)
	}
	for _, table := range []string{"_workspaces", "_tenant_registry", "_workspace_configuration", "_workspace_provisioning_receipts", "identity_provision_probe"} {
		assertWorkspaceProvisionRowCount(t, store, table, 0)
	}

	repository := &WorkspaceProvisionStore{runtime: store, identity: identityParticipantProbe{}}
	request.RequestID = "request-success"
	result, err := repository.Provision(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.CanonicalCode != "north-store" || result.InitialPassword != "OneTime1!" || result.Replayed || !result.MustChangePassword {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, table := range []string{"_workspaces", "_tenant_registry", "_workspace_configuration", "_workspace_provisioning_receipts", "identity_provision_probe"} {
		assertWorkspaceProvisionRowCount(t, store, table, 1)
	}
	replay, err := repository.Provision(t.Context(), request)
	if err != nil || !replay.Replayed || replay.WorkspaceID != result.WorkspaceID || replay.InitialPassword != result.InitialPassword {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	request.TenantName = "Different"
	if _, err := repository.Provision(t.Context(), request); !errors.Is(err, workspaceprovisionmodel.ErrIdempotencyConflict) {
		t.Fatalf("conflict=%v", err)
	}
	reconciled, err := repository.ReconcileWorkspaceRoles(t.Context(), result.WorkspaceID)
	if err != nil || reconciled.ProvisionedRoles != 2 {
		t.Fatalf("reconciled=%#v err=%v", reconciled, err)
	}
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
