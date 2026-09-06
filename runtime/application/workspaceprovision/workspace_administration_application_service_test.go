package workspaceprovision

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

type workspaceAdministrationRepositoryProbe struct {
	listAfter  string
	listLimit  int
	statusCall int
	updateCall int
}

func (probe *workspaceAdministrationRepositoryProbe) ListWorkspaceCatalog(_ context.Context, after string, limit int) ([]workspaceprovisionmodel.CatalogEntry, bool, error) {
	probe.listAfter, probe.listLimit = after, limit
	return []workspaceprovisionmodel.CatalogEntry{{CanonicalCode: "alpha"}}, true, nil
}

func (probe *workspaceAdministrationRepositoryProbe) SetWorkspaceStatus(_ context.Context, _ workspaceprovisionmodel.AdministrationActor, canonicalCode string, expected int, status, key string) (workspaceprovisionmodel.LifecycleResult, error) {
	probe.statusCall++
	return workspaceprovisionmodel.LifecycleResult{Workspace: workspaceprovisionmodel.CatalogEntry{CanonicalCode: canonicalCode, Status: status, Revision: expected + 1}}, nil
}

func (probe *workspaceAdministrationRepositoryProbe) UpdateWorkspaceCommercialConfiguration(_ context.Context, _ workspaceprovisionmodel.AdministrationActor, canonicalCode string, request workspaceprovisionmodel.CommercialConfigurationUpdateRequest, key string) (workspaceprovisionmodel.CommercialConfigurationUpdateResult, error) {
	probe.updateCall++
	return workspaceprovisionmodel.CommercialConfigurationUpdateResult{Workspace: workspaceprovisionmodel.CatalogEntry{CanonicalCode: canonicalCode}}, nil
}

func workspaceAdministrationPrincipal(t *testing.T, role, workspaceID string, permissions ...string) principalmodel.Principal {
	t.Helper()
	previous := principalmodel.InstallationWorkspaceID
	principalmodel.InstallationWorkspaceID = "workspace-initial"
	t.Cleanup(func() { principalmodel.InstallationWorkspaceID = previous })
	bundle := &identitysdk.AccessBundle{ContractVersion: identitysdk.CurrentPolicyBundleVersion}
	for _, permission := range permissions {
		separator := strings.LastIndexByte(permission, '.')
		resource, action := permission[:separator], permission[separator+1:]
		bundle.FunctionGrants = append(bundle.FunctionGrants, identitysdk.FunctionGrant{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow})
		bundle.DataPolicies = append(bundle.DataPolicies, identitysdk.DataPolicy{Key: permission, Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow})
	}
	return principalmodel.NewPrincipalFromIdentity(identitysdk.Principal{
		Known: true, WorkspaceID: workspaceID, UserID: "tenant-admin", RoleKey: role, AuthorizationRevision: "auth-1", AccessBundle: bundle,
	}, "request-1")
}

func TestWorkspaceAdministrationApplicationServiceRequiresInstallationTenantAdministratorAndSealsCursor(t *testing.T) {
	now := time.Unix(1000, 0)
	codec, err := NewWorkspaceAdministrationCursorCodec([]byte("workspace-administration-test-key"), "installation-a", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	probe := &workspaceAdministrationRepositoryProbe{}
	service := NewWorkspaceAdministrationApplicationService(probe, codec)
	for _, principal := range []principalmodel.Principal{
		workspaceAdministrationPrincipal(t, "staff", "workspace-initial", ListWorkspacesActionKey),
		workspaceAdministrationPrincipal(t, identitysdk.WorkspaceBootstrapRoleHeadquartersAdmin, "workspace-initial", ListWorkspacesActionKey),
		workspaceAdministrationPrincipal(t, WorkspaceAdministratorRoleKey, "workspace-other", ListWorkspacesActionKey),
		workspaceAdministrationPrincipal(t, WorkspaceAdministratorRoleKey, "workspace-initial"),
	} {
		if _, err := service.List(t.Context(), principal, workspaceprovisionmodel.CatalogQuery{}); apperror.CodeOf(err) != "auth.permission_denied" {
			t.Fatalf("unauthorized principal=%+v err=%v", principal.Principal, err)
		}
	}
	principal := workspaceAdministrationPrincipal(t, WorkspaceAdministratorRoleKey, "workspace-initial", ListWorkspacesActionKey)
	first, err := service.List(t.Context(), principal, workspaceprovisionmodel.CatalogQuery{PageSize: 1})
	if err != nil || first.NextCursor == "" || probe.listLimit != 1 {
		t.Fatalf("first=%+v probe=%+v err=%v", first, probe, err)
	}
	if _, err := service.List(t.Context(), principal, workspaceprovisionmodel.CatalogQuery{PageSize: 1, Cursor: first.NextCursor + "tampered"}); apperror.CodeOf(err) != "workspace.administration_request_invalid" {
		t.Fatalf("tampered cursor=%v", err)
	}
	second, err := service.List(t.Context(), principal, workspaceprovisionmodel.CatalogQuery{PageSize: 1, Cursor: first.NextCursor})
	if err != nil || probe.listAfter != "alpha" || second.NextCursor == "" {
		t.Fatalf("second=%+v after=%q err=%v", second, probe.listAfter, err)
	}
	rotated := principal
	rotated.AuthorizationRevision = "auth-2"
	if _, err := service.List(t.Context(), rotated, workspaceprovisionmodel.CatalogQuery{PageSize: 1, Cursor: first.NextCursor}); apperror.CodeOf(err) != "workspace.administration_request_invalid" {
		t.Fatalf("authorization-rotation cursor=%v", err)
	}
	now = now.Add(workspaceAdministrationCursorTTL)
	if _, err := service.List(t.Context(), principal, workspaceprovisionmodel.CatalogQuery{PageSize: 1, Cursor: first.NextCursor}); apperror.CodeOf(err) != "workspace.administration_request_invalid" {
		t.Fatalf("expired cursor=%v", err)
	}
}

func TestWorkspaceAdministrationApplicationServiceValidatesCASAndTypedCommercialConfiguration(t *testing.T) {
	probe := &workspaceAdministrationRepositoryProbe{}
	service := NewWorkspaceAdministrationApplicationService(probe, nil)
	principal := workspaceAdministrationPrincipal(t, WorkspaceAdministratorRoleKey, "workspace-initial", SuspendWorkspaceActionKey, UpdateWorkspaceCommercialConfigurationActionKey)
	if _, err := service.Suspend(t.Context(), principal, "alpha", 1, ""); apperror.CodeOf(err) != "workspace.administration_request_invalid" || probe.statusCall != 0 {
		t.Fatalf("missing idempotency key call=%d err=%v", probe.statusCall, err)
	}
	if _, err := service.Suspend(t.Context(), principal, "alpha", 1, "suspend-1"); err != nil || probe.statusCall != 1 {
		t.Fatalf("valid lifecycle call=%d err=%v", probe.statusCall, err)
	}
	invalid := workspaceprovisionmodel.CommercialConfigurationUpdateRequest{ExpectedRevision: 1, Configuration: workspaceprovisionmodel.CommercialConfiguration{Plan: "standard"}}
	if _, err := service.UpdateCommercialConfiguration(t.Context(), principal, "alpha", invalid, "commercial-1"); apperror.CodeOf(err) != "workspace.administration_request_invalid" || probe.updateCall != 0 {
		t.Fatalf("invalid commercial call=%d err=%v", probe.updateCall, err)
	}
	valid := invalid
	valid.Configuration = workspaceprovisionmodel.CommercialConfiguration{Plan: "standard", IncludedStoreLimit: 1, MaxStores: 1, ContractDate: "2026-09-06", BillingDay: 1}
	if _, err := service.UpdateCommercialConfiguration(t.Context(), principal, "alpha", valid, "commercial-2"); err != nil || probe.updateCall != 1 {
		t.Fatalf("valid commercial call=%d err=%v", probe.updateCall, err)
	}
}
