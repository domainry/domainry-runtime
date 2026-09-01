package workspaceprovision

import (
	"context"
	"net/http"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

type UseCases interface {
	Provision(context.Context, principalmodel.Principal, workspaceprovisionmodel.Request) (workspaceprovisionmodel.Result, error)
	ReconcileWorkspaceRoles(context.Context, principalmodel.Principal, string) (workspaceprovisionmodel.RoleReconciliationResult, error)
}

type WorkspaceProvisionDependencies struct {
	UseCases          UseCases
	Principal         func(*http.Request) principalmodel.Principal
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	SecurityAudit     func(*http.Request, string, string, map[string]any)
}

type WorkspaceProvisionHandler struct {
	dependencies WorkspaceProvisionDependencies
}

func NewWorkspaceProvisionHandler(dependencies WorkspaceProvisionDependencies) *WorkspaceProvisionHandler {
	return &WorkspaceProvisionHandler{dependencies: dependencies}
}

func (handler *WorkspaceProvisionHandler) provision(response http.ResponseWriter, request *http.Request) {
	var input workspaceprovisionmodel.Request
	if !handler.dependencies.DecodeJSON(response, request, &input) {
		return
	}
	result, err := handler.dependencies.UseCases.Provision(request.Context(), handler.dependencies.Principal(request), input)
	if err != nil {
		handler.dependencies.WriteServiceError(response, request, err)
		return
	}
	if handler.dependencies.SecurityAudit != nil {
		handler.dependencies.SecurityAudit(request, "workspace.provisioned", result.WorkspaceID, map[string]any{
			"request_id": input.RequestID, "tenant_registry_id": result.TenantRegistryID,
			"workspace_id": result.WorkspaceID, "canonical_code": result.CanonicalCode,
			"admin_login_id": result.AdminLoginID, "replayed": result.Replayed,
		})
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	handler.dependencies.WriteJSON(response, status, result)
}

func (handler *WorkspaceProvisionHandler) reconcileRoles(response http.ResponseWriter, request *http.Request) {
	workspaceID := strings.TrimSpace(request.PathValue("workspaceID"))
	result, err := handler.dependencies.UseCases.ReconcileWorkspaceRoles(request.Context(), handler.dependencies.Principal(request), workspaceID)
	if err != nil {
		handler.dependencies.WriteServiceError(response, request, err)
		return
	}
	if handler.dependencies.SecurityAudit != nil {
		handler.dependencies.SecurityAudit(request, "workspace.roles_reconciled", workspaceID, map[string]any{
			"workspace_id": workspaceID, "provisioned_roles": result.ProvisionedRoles,
		})
	}
	handler.dependencies.WriteJSON(response, http.StatusOK, result)
}
