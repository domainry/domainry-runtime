package surfacecontext

import "net/http"

func (h *SurfaceContextHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /surfaces/{surfaceKey}/context", h.surfaceContext)
}
