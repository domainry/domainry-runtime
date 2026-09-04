package workspaceprovision

import "net/http"

func (handler *WorkspaceProvisionHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /workspace/provision", handler.provision)
	mux.HandleFunc("POST /workspace/{workspaceID}/roles/reconcile", handler.reconcileRoles)
}
