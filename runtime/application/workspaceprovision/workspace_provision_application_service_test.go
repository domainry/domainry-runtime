package workspaceprovision

import (
	"context"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

type workspaceProvisionRepositoryProbe struct {
	provisionCalls int
	reconcileCalls int
	provisionErr   error
}

func (repository *workspaceProvisionRepositoryProbe) Provision(context.Context, workspaceprovisionmodel.Request) (workspaceprovisionmodel.Result, error) {
	repository.provisionCalls++
	return workspaceprovisionmodel.Result{WorkspaceID: "workspace-new"}, repository.provisionErr
}

func TestWorkspaceProvisionApplicationServiceMapsAcceptanceFailureToStableCode(t *testing.T) {
	repository := &workspaceProvisionRepositoryProbe{provisionErr: workspaceprovisionmodel.ErrAcceptanceFailure}
	service := NewWorkspaceProvisionApplicationService(repository)
	principal := principalmodel.NewSystemPrincipal("platform-admin", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "workspace provisioning"), ProvisionActionKey)
	result, err := service.Provision(t.Context(), principal, workspaceprovisionmodel.Request{RequestID: "secret-request"})
	if result.WorkspaceID != "" || result.TenantRegistryID != "" || result.InitialPassword != "" || len(result.ProjectionIDs) != 0 || apperror.CodeOf(err) != "workspace.provision_failed" || apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("result=%#v code=%q kind=%q error=%v", result, apperror.CodeOf(err), apperror.KindOf(err), err)
	}
}

func (repository *workspaceProvisionRepositoryProbe) ReconcileWorkspaceRoles(context.Context, string) (workspaceprovisionmodel.RoleReconciliationResult, error) {
	repository.reconcileCalls++
	return workspaceprovisionmodel.RoleReconciliationResult{WorkspaceID: "workspace-new"}, nil
}

func TestWorkspaceProvisionApplicationServiceAuthorizesBeforeRepositoryAccess(t *testing.T) {
	repository := &workspaceProvisionRepositoryProbe{}
	service := NewWorkspaceProvisionApplicationService(repository)
	request := workspaceprovisionmodel.Request{RequestID: "request-1"}

	if _, err := service.Provision(t.Context(), principalmodel.Principal{}, request); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("provision denial=%v", err)
	}
	if _, err := service.ReconcileWorkspaceRoles(t.Context(), principalmodel.Principal{}, "workspace-new"); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("reconcile denial=%v", err)
	}
	if repository.provisionCalls != 0 || repository.reconcileCalls != 0 {
		t.Fatalf("denied request reached repository: provision=%d reconcile=%d", repository.provisionCalls, repository.reconcileCalls)
	}

	provisioner := principalmodel.NewSystemPrincipal("platform-admin", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "workspace provisioning"), ProvisionActionKey)
	if _, err := service.Provision(t.Context(), provisioner, request); err != nil || repository.provisionCalls != 1 {
		t.Fatalf("authorized provision calls=%d err=%v", repository.provisionCalls, err)
	}
	reconciler := principalmodel.NewSystemPrincipal("platform-admin", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "role reconciliation"), ReconcileRolesActionKey)
	if _, err := service.ReconcileWorkspaceRoles(t.Context(), reconciler, "workspace-new"); err != nil || repository.reconcileCalls != 1 {
		t.Fatalf("authorized reconcile calls=%d err=%v", repository.reconcileCalls, err)
	}
}
