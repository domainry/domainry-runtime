package http

import (
	"context"
	"net/http"
	"strings"

	businesseventapplication "github.com/domainry/domainry-runtime/runtime/application/businessevent"
	businesseventcontract "github.com/domainry/domainry-runtime/runtime/domain/businessevent/contract"
	businesseventmodel "github.com/domainry/domainry-runtime/runtime/domain/businessevent/model"
	businesseventhttp "github.com/domainry/domainry-runtime/runtime/transport/http/businessevents"
)

func (s *HTTPRouter) configureBusinessEvents(config HTTPRouterConfig, backplane businesseventcontract.Backplane) {
	events := businesseventapplication.NewBusinessEventApplicationService(backplane, businesseventapplication.Limits{
		GlobalConnections: config.BusinessEventGlobalConnections, WorkspaceConnections: config.BusinessEventWorkspaceConnections,
		PrincipalConnections: config.BusinessEventPrincipalConnections,
	})
	s.businessEvents = events
	s.businessEventHTTP = businesseventhttp.NewBusinessEventsHandler(businesseventhttp.BusinessEventsDependencies{
		Service: events, Principal: s.principalFromRequest, WriteServiceError: writeServiceError,
		SecurityAuditForPrincipal: s.appendSecurityAuditForPrincipal,
		HeartbeatInterval:         config.BusinessEventHeartbeatInterval, RetryInterval: config.BusinessEventRetryInterval,
	})
}

type businessEventStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *businessEventStatusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *businessEventStatusWriter) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(payload)
}

func (w *businessEventStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *HTTPRouter) withBusinessEventPublication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !businessEventMutationMethod(r.Method) || r.URL.Path == businesseventhttp.Route {
			next.ServeHTTP(w, r)
			return
		}
		writer := &businessEventStatusWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r)
		if writer.status < 200 || writer.status >= 300 {
			return
		}
		principal, ok := principalFromContext(r)
		if !ok || strings.TrimSpace(principal.WorkspaceID) == "" {
			return
		}
		if _, err := s.PublishBusinessEvent(context.WithoutCancel(r.Context()), principal.WorkspaceID, businessEventObjectKey(r), "mutation"); err != nil {
			s.appendSecurityAuditForPrincipal(r, principal, "business_event_publish_failed", "Business refresh event publication failed", map[string]any{"error_code": "backend.event_stream.unavailable"})
		}
	})
}

func (s *HTTPRouter) PublishBusinessEvent(ctx context.Context, workspaceID, objectKey, reason string) (businesseventmodel.BusinessEvent, error) {
	return s.businessEvents.Publish(ctx, workspaceID, objectKey, reason)
}

func businessEventMutationMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func businessEventObjectKey(r *http.Request) string {
	if value := strings.TrimSpace(r.PathValue("objectKey")); value != "" {
		return value
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "records" && parts[1] == "objects" {
		return strings.TrimSpace(parts[2])
	}
	return ""
}
