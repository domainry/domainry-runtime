package runtimehost

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodulehost "github.com/domainry/domainry-identity-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/testsupport/auditmodulefixture"
)

type workspaceIdentityUsageAuthenticatorStub struct {
	principal identitysdk.Principal
	err       error
}

func (stub workspaceIdentityUsageAuthenticatorStub) Authenticate(context.Context, string) (identitysdk.Principal, error) {
	return stub.principal, stub.err
}

func TestRuntimeWorkspaceIdentityUsageAuthorityDurablyAuthorizesThenResolvesCanonicalScopeInsideActionTransaction(t *testing.T) {
	cfg := serverTestConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "workspace-identity-usage-authority.db")
	store, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg, database.FullRuntimeSchemaCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseContext(t.Context()) })
	auditmodulefixture.Bind(t, t.Context(), store)
	seedWorkspaceIdentityUsageAuthorityWorkspace(t, store, "workspace-installation", "primary", true)
	seedWorkspaceIdentityUsageAuthorityWorkspace(t, store, "workspace-night", "night-tokyo", false)

	handle := projectIdentityDatabaseHandle(store, cfg.DBPath, nil, projectIdentityUsageOptions{ApplicationKey: "nightpos", CursorSecret: "stable-secret"})
	authority, ok := handle.WorkspaceIdentityUsageAuthority.(*runtimeWorkspaceIdentityUsageAuthority)
	if !ok || len(handle.WorkspaceIdentityUsageCursorKey) != 32 {
		t.Fatalf("handle authority=%T cursor_key_length=%d", handle.WorkspaceIdentityUsageAuthority, len(handle.WorkspaceIdentityUsageCursorKey))
	}
	if err := authority.BindAuthenticator(workspaceIdentityUsageAuthenticatorStub{principal: identitysdk.Principal{
		Known: true, WorkspaceID: "workspace-installation", UserID: "billing-admin", RoleKey: workspaceIdentityUsageAdministratorRole,
		AuthorizationRevision: "authz-7",
	}}); err != nil {
		t.Fatal(err)
	}
	grant, err := authority.AuthorizeWorkspaceIdentityUsage(t.Context(), identitymodulehost.WorkspaceIdentityUsageAuthorizationRequest{
		AccessToken: "valid-token", PermissionKey: identitysdk.WorkspaceIdentityUsageAggregatePermission,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.SubjectID != "billing-admin" || grant.AuditWorkspaceID != "workspace-installation" || grant.AuthorizationAuditID == "" {
		t.Fatalf("grant=%#v", grant)
	}
	var authorizationAudits int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND id = ? AND event = ?`,
		"workspace-installation", grant.AuthorizationAuditID, "identity.workspace_identity_usage.authorize").Scan(&authorizationAudits); err != nil || authorizationAudits != 1 {
		t.Fatalf("authorization audits=%d err=%v", authorizationAudits, err)
	}
	tx, err := store.RuntimeProfile().BeginWrite(t.Context(), store.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	txCtx := database.WithActionExecutionTransaction(t.Context(), tx)
	resolved, err := authority.ResolveAuthorizedWorkspaceIdentityUsage(txCtx, grant, identitymodulehost.WorkspaceIdentityUsageCatalogResolve{WorkspaceCode: "night-tokyo"})
	if err != nil {
		t.Fatalf("resolve failed: %#v", err)
	}
	if resolved.WorkspaceID != "workspace-night" || resolved.Status != identitymodulehost.WorkspaceIdentityUsageCatalogActive || !resolved.Known || !resolved.Authorized {
		t.Fatalf("resolved=%#v", resolved)
	}
	page, err := authority.ListAuthorizedWorkspaceIdentityUsage(txCtx, grant, identitymodulehost.WorkspaceIdentityUsageCatalogQuery{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if page.CatalogRevision == "" || len(page.Workspaces) != 2 || page.Workspaces[0].WorkspaceID != "workspace-installation" || page.Workspaces[1].WorkspaceID != "workspace-night" {
		t.Fatalf("page=%#v", page)
	}
	if _, err := authority.ListAuthorizedWorkspaceIdentityUsage(txCtx, grant, identitymodulehost.WorkspaceIdentityUsageCatalogQuery{Limit: 3, ExpectedCatalogRevision: "stale"}); err == nil {
		t.Fatal("stale Workspace catalog revision was accepted")
	}
}

func TestRuntimeWorkspaceIdentityUsageAuthorityDeniesStaffAndHeadquartersAdministratorAndAuditsDecisions(t *testing.T) {
	cfg := serverTestConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "workspace-identity-usage-denial.db")
	store, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg, database.FullRuntimeSchemaCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseContext(t.Context()) })
	auditmodulefixture.Bind(t, t.Context(), store)
	seedWorkspaceIdentityUsageAuthorityWorkspace(t, store, "workspace-installation", "primary", true)
	authority := newRuntimeWorkspaceIdentityUsageAuthority(store, "nightpos")
	for index, roleKey := range []string{"staff", "headquarters_admin"} {
		if err := authority.BindAuthenticator(workspaceIdentityUsageAuthenticatorStub{principal: identitysdk.Principal{
			Known: true, WorkspaceID: "workspace-installation", UserID: roleKey + "-user", RoleKey: roleKey, AuthorizationRevision: fmt.Sprintf("authz-%d", index+2),
		}}); err != nil {
			t.Fatal(err)
		}
		if _, err := authority.AuthorizeWorkspaceIdentityUsage(t.Context(), identitymodulehost.WorkspaceIdentityUsageAuthorizationRequest{
			AccessToken: roleKey + "-token", PermissionKey: identitysdk.WorkspaceIdentityUsageAggregatePermission,
		}); err == nil {
			t.Fatalf("%s Workspace usage was authorized", roleKey)
		}
		var denied int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND event = ? AND role_key = ?`,
			"workspace-installation", "identity.workspace_identity_usage.authorize", roleKey).Scan(&denied); err != nil || denied != 1 {
			t.Fatalf("%s denied audits=%d err=%v", roleKey, denied, err)
		}
	}
}

func seedWorkspaceIdentityUsageAuthorityWorkspace(t *testing.T, store *bootstrap.ProjectDatabase, workspaceID, canonicalCode string, initial bool) {
	t.Helper()
	var installationIdentity any
	if initial {
		value, err := store.InstallationIdentity(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		installationIdentity = value
	}
	statement, arguments, err := query.NewInsertBuilder(store.RuntimeRenderer(), "_workspaces").
		Columns(
			"id", "canonical_code", "name", "status", "initial_installation_identity",
			"plan", "included_user_limit", "max_user_limit", "included_customer_limit", "max_customer_limit", "included_store_limit", "max_stores",
			"contract_date", "billing_day", "billing_contact_name", "billing_contact_phone", "billing_contact_email", "billing_contact_address", "billing_contact_notes",
			"commercial_revision", "revision", "created_at", "updated_at",
		).
		Values(workspaceID, canonicalCode, canonicalCode, "active", installationIdentity, "standard", 5, 25, 100, 1000, 1, 5, "2026-09-01", 25, "", "", "", "", "", 1, 1, "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z").Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB().ExecContext(t.Context(), statement, arguments...); err != nil {
		t.Fatal(err)
	}
}
