package workspaceprovision

import "net/http"

func (handler *WorkspaceProvisionHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /tenant-admin/workspaces/provision", handler.provision)
	mux.HandleFunc("POST /tenant-admin/workspaces/{workspaceID}/roles/reconcile", handler.reconcileRoles)
}
