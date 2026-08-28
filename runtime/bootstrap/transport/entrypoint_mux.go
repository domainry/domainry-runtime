package transport

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

// EntrypointMux switches the process between unauthenticated Provision transport
// and the activated business transport without leaking that lifecycle into app.
type EntrypointMux struct {
	mu              sync.RWMutex
	provision       http.Handler
	business        http.Handler
	lifecycleStatus string
	builderTaskID   string
}

func (h *EntrypointMux) SetConfiguring(builderTaskID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lifecycleStatus = "configuring"
	h.builderTaskID = strings.TrimSpace(builderTaskID)
}

func (h *EntrypointMux) SetProvision(handler http.Handler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.provision = handler
}

func (h *EntrypointMux) SetBusiness(handler http.Handler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.business = handler
}

func (h *EntrypointMux) SetProvisioning() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.business = nil
	h.lifecycleStatus = ""
	h.builderTaskID = ""
}

func (h *EntrypointMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	provisionHandler := h.provision
	businessHandler := h.business
	lifecycleStatus, builderTaskID := h.lifecycleStatus, h.builderTaskID
	h.mu.RUnlock()
	if (strings.HasPrefix(r.URL.Path, "/metadata/manifests") || strings.HasPrefix(r.URL.Path, "/provision/")) && provisionHandler != nil {
		provisionHandler.ServeHTTP(w, r)
		return
	}
	if lifecycleStatus == "configuring" && r.URL.Path == "/health" && provisionHandler != nil {
		provisionHandler.ServeHTTP(w, r)
		return
	}
	if businessHandler != nil {
		if lifecycleStatus == "configuring" {
			if strings.TrimSpace(r.Header.Get("Builder-Task-ID")) != builderTaskID {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusLocked)
				_ = json.NewEncoder(w).Encode(map[string]any{"code": "runtime.configuring_access_required", "status": "configuring"})
				return
			}
			ctx := requestcontext.WithRuntimeAuthoringBuilderTaskID(r.Context(), builderTaskID)
			ctx = requestcontext.WithWorkspaceID(ctx, "default")
			ctx = requestcontext.WithActorID(ctx, "runtime-builder:"+builderTaskID)
			r = r.WithContext(ctx)
		}
		businessHandler.ServeHTTP(w, r)
		return
	}
	if provisionHandler == nil {
		http.Error(w, "Runtime handler unavailable", http.StatusServiceUnavailable)
		return
	}
	provisionHandler.ServeHTTP(w, r)
}
