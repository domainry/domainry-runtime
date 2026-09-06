package workspaceprovision

import "net/http"

func (handler *WorkspaceProvisionHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /workspaces", handler.list)
	mux.HandleFunc("POST /workspaces", handler.provision)
	mux.HandleFunc("POST /workspaces/{workspaceCode}/suspend", handler.suspend)
	mux.HandleFunc("POST /workspaces/{workspaceCode}/reactivate", handler.reactivate)
	mux.HandleFunc("PUT /workspaces/{workspaceCode}/commercial-configuration", handler.updateCommercialConfiguration)
}
