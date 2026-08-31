package workspaceprovision

import "net/http"

func (handler *WorkspaceProvisionHandler) RegisterRoutes(mux *http.ServeMux) {
	provision := handler.provision
	if handler.dependencies.ProvisionGuard != nil {
		provision = handler.dependencies.ProvisionGuard(provision)
	}
	reconcile := handler.reconcileRoles
	if handler.dependencies.ReconcileGuard != nil {
		reconcile = handler.dependencies.ReconcileGuard(reconcile)
	}
	mux.HandleFunc("POST /tenant-admin/workspaces/provision", provision)
	mux.HandleFunc("POST /tenant-admin/workspaces/{workspaceID}/roles/reconcile", reconcile)
}
