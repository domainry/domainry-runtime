package transport

import (
	"net/http"
	"sync"
)

// EntrypointMux publishes the authenticated business transport after Runtime
// initialization completes. There is deliberately no unauthenticated
// provisioning transport or builder lifecycle.
type EntrypointMux struct {
	mu       sync.RWMutex
	business http.Handler
}

func (h *EntrypointMux) SetBusiness(handler http.Handler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.business = handler
}

func (h *EntrypointMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	businessHandler := h.business
	h.mu.RUnlock()
	if businessHandler != nil {
		businessHandler.ServeHTTP(w, r)
		return
	}
	http.Error(w, "Runtime handler unavailable", http.StatusServiceUnavailable)
}
