package workspaceprovision

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
)

type UseCases interface {
	Provision(context.Context, principalmodel.Principal, workspaceprovisionmodel.Request) (workspaceprovisionmodel.Result, error)
}

type AdministrationUseCases interface {
	List(context.Context, principalmodel.Principal, workspaceprovisionmodel.CatalogQuery) (workspaceprovisionmodel.CatalogPage, error)
	Suspend(context.Context, principalmodel.Principal, string, int, string) (workspaceprovisionmodel.LifecycleResult, error)
	Reactivate(context.Context, principalmodel.Principal, string, int, string) (workspaceprovisionmodel.LifecycleResult, error)
	UpdateCommercialConfiguration(context.Context, principalmodel.Principal, string, workspaceprovisionmodel.CommercialConfigurationUpdateRequest, string) (workspaceprovisionmodel.CommercialConfigurationUpdateResult, error)
}

type WorkspaceProvisionDependencies struct {
	UseCases          UseCases
	Administration    AdministrationUseCases
	Principal         func(*http.Request) principalmodel.Principal
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	SecurityAudit     func(*http.Request, string, string, map[string]any)
}

func (handler *WorkspaceProvisionHandler) list(response http.ResponseWriter, request *http.Request) {
	pageSize := 0
	if raw := strings.TrimSpace(request.URL.Query().Get("page_size")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			value = -1
		}
		pageSize = value
	}
	result, err := handler.dependencies.Administration.List(request.Context(), handler.dependencies.Principal(request), workspaceprovisionmodel.CatalogQuery{
		PageSize: pageSize, Cursor: request.URL.Query().Get("cursor"),
	})
	if err != nil {
		handler.dependencies.WriteServiceError(response, request, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	handler.dependencies.WriteJSON(response, http.StatusOK, result)
}

func (handler *WorkspaceProvisionHandler) suspend(response http.ResponseWriter, request *http.Request) {
	handler.setStatus(response, request, true)
}

func (handler *WorkspaceProvisionHandler) reactivate(response http.ResponseWriter, request *http.Request) {
	handler.setStatus(response, request, false)
}

func (handler *WorkspaceProvisionHandler) setStatus(response http.ResponseWriter, request *http.Request, suspend bool) {
	var input workspaceprovisionmodel.LifecycleRequest
	if !handler.dependencies.DecodeJSON(response, request, &input) {
		return
	}
	var result workspaceprovisionmodel.LifecycleResult
	var err error
	if suspend {
		result, err = handler.dependencies.Administration.Suspend(request.Context(), handler.dependencies.Principal(request), request.PathValue("workspaceCode"), input.ExpectedRevision, request.Header.Get("Idempotency-Key"))
	} else {
		result, err = handler.dependencies.Administration.Reactivate(request.Context(), handler.dependencies.Principal(request), request.PathValue("workspaceCode"), input.ExpectedRevision, request.Header.Get("Idempotency-Key"))
	}
	if err != nil {
		handler.dependencies.WriteServiceError(response, request, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Idempotency-Replayed", strconv.FormatBool(result.Replayed))
	handler.dependencies.WriteJSON(response, http.StatusOK, result)
}

func (handler *WorkspaceProvisionHandler) updateCommercialConfiguration(response http.ResponseWriter, request *http.Request) {
	var input workspaceprovisionmodel.CommercialConfigurationUpdateRequest
	if !handler.dependencies.DecodeJSON(response, request, &input) {
		return
	}
	result, err := handler.dependencies.Administration.UpdateCommercialConfiguration(request.Context(), handler.dependencies.Principal(request), request.PathValue("workspaceCode"), input, request.Header.Get("Idempotency-Key"))
	if err != nil {
		handler.dependencies.WriteServiceError(response, request, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Idempotency-Replayed", strconv.FormatBool(result.Replayed))
	handler.dependencies.WriteJSON(response, http.StatusOK, result)
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
			"request_id": input.RequestID, "workspace_id": result.WorkspaceID, "canonical_code": result.CanonicalCode,
			"admin_login_id": result.AdminLoginID, "replayed": result.Replayed,
		})
	}
	response.Header().Set("Cache-Control", "no-store")
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	handler.dependencies.WriteJSON(response, status, result)
}
