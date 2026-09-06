package workspaceprovision

import (
	"context"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

func workspaceProvisionTenantAdministrator(t *testing.T) principalmodel.Principal {
	t.Helper()
	previous := principalmodel.InstallationWorkspaceID
	principalmodel.InstallationWorkspaceID = "workspace-primary"
	t.Cleanup(func() { principalmodel.InstallationWorkspaceID = previous })
	bundle := &identitysdk.AccessBundle{
		ContractVersion: identitysdk.CurrentPolicyBundleVersion,
		FunctionGrants:  []identitysdk.FunctionGrant{{Resource: "runtime.workspaceprovision", Action: "provision_workspace", Effect: identitysdk.EffectAllow}},
		DataPolicies:    []identitysdk.DataPolicy{{Key: "workspace-provision", Resource: "runtime.workspaceprovision", Action: "provision_workspace", Effect: identitysdk.EffectAllow}},
	}
	return principalmodel.NewPrincipalFromIdentity(identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary", UserID: "platform-admin", RoleKey: WorkspaceAdministratorRoleKey, AccessBundle: bundle}, "")
}

func validWorkspaceProvisionApplicationRequest() workspaceprovisionmodel.Request {
	return workspaceprovisionmodel.Request{
		RequestID: "request-1", WorkspaceCode: "workspace-a", WorkspaceName: "Workspace A", FirstStoreCode: "store-a", FirstStoreName: "Store A",
		AdminLoginID: "admin@example.test", AdminName: "Admin",
		CommercialConfiguration: workspaceprovisionmodel.CommercialConfiguration{
			Plan: "standard", IncludedUserLimit: 1, MaxUserLimit: 2, IncludedCustomerLimit: 1, MaxCustomerLimit: 2,
			IncludedStoreLimit: 1, MaxStores: 2, ContractDate: "2026-09-06", BillingDay: 1,
		},
	}
}

type workspaceProvisionRepositoryProbe struct {
	provisionCalls int
	provisionErr   error
}

func (repository *workspaceProvisionRepositoryProbe) Provision(context.Context, workspaceprovisionmodel.Request) (workspaceprovisionmodel.Result, error) {
	repository.provisionCalls++
	return workspaceprovisionmodel.Result{WorkspaceID: "workspace-new"}, repository.provisionErr
}

func TestWorkspaceProvisionApplicationServiceMapsAcceptanceFailureToStableCode(t *testing.T) {
	repository := &workspaceProvisionRepositoryProbe{provisionErr: workspaceprovisionmodel.ErrAcceptanceFailure}
	service := NewWorkspaceProvisionApplicationService(repository)
	principal := workspaceProvisionTenantAdministrator(t)
	request := validWorkspaceProvisionApplicationRequest()
	request.RequestID = "secret-request"
	result, err := service.Provision(t.Context(), principal, request)
	if result.WorkspaceID != "" || result.InitialPassword != "" || apperror.CodeOf(err) != "workspace.provision_failed" || apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("result=%#v code=%q kind=%q error=%v", result, apperror.CodeOf(err), apperror.KindOf(err), err)
	}
}

func TestWorkspaceProvisionApplicationServiceAuthorizesBeforeRepositoryAccess(t *testing.T) {
	repository := &workspaceProvisionRepositoryProbe{}
	service := NewWorkspaceProvisionApplicationService(repository)
	request := validWorkspaceProvisionApplicationRequest()

	if _, err := service.Provision(t.Context(), principalmodel.Principal{}, request); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("provision denial=%v", err)
	}
	if repository.provisionCalls != 0 {
		t.Fatalf("denied request reached repository: provision=%d", repository.provisionCalls)
	}

	provisioner := workspaceProvisionTenantAdministrator(t)
	if _, err := service.Provision(t.Context(), provisioner, request); err != nil || repository.provisionCalls != 1 {
		t.Fatalf("authorized provision calls=%d err=%v", repository.provisionCalls, err)
	}
}
