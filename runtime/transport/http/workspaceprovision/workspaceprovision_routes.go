package workspaceprovision

import "net/http"

func (handler *WorkspaceProvisionHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /workspaces", handler.provision)
	mux.HandleFunc("POST /workspaces/{workspaceID}/role-reconciliations", handler.reconcileRoles)
}
