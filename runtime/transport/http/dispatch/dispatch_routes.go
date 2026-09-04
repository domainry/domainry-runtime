package dispatch

import "net/http"

const RuntimeExecutionPath = "/dispatch/executions"

func (h *ExecutionHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /dispatch/executions", h.acceptExecution)
}
