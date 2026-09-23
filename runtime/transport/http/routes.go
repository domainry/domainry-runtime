package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	healthplatform "github.com/domainry/domainry-foundation/health"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

var runtimeEndpointContracts = endpointmodel.EndpointContracts

func validateCompiledEndpointContracts() error {
	if len(runtimeEndpointContracts) == 0 {
		return fmt.Errorf("endpoint contract inventory is empty")
	}
	for route, contract := range runtimeEndpointContracts {
		if contract.EndpointIdentity != route {
			return fmt.Errorf("endpoint contract %q identity=%q", route, contract.EndpointIdentity)
		}
		if err := contract.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func endpointVisibleOnListener(contract endpointmodel.RuntimeEndpointContractV1, exposure endpointmodel.ListenerExposure) bool {
	for _, candidate := range contract.ListenerExposures {
		if candidate == exposure {
			return true
		}
	}
	return false
}

func (s *HTTPRouter) registerProbeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /live", s.live)
	mux.HandleFunc("GET /ready", s.ready)
	mux.HandleFunc("GET /startup", s.startup)
}

func (s *HTTPRouter) registerFallbackRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/{path...}", s.notFound)
}

func (s *HTTPRouter) notFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]any{"code": "route_not_found", "message": "unknown domain Runtime route", "path": r.URL.Path, "service_kind": s.serviceKind, "guidance": "Use the published client route without an /api/v1 prefix; /api/v1 is not supported."})
}

func (s *HTTPRouter) apiInfo(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		s.notFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"service": s.productBrandName + " Business Runtime", "service_kind": s.serviceKind, "runtime_version": s.runtimeVersion, "api_contract_version": s.apiContractVersion})
}

func (s *HTTPRouter) health(w http.ResponseWriter, r *http.Request) {
	principal := s.principalFromRequest(r)
	if !healthAllowed(principal) {
		writeError(w, r, http.StatusForbidden, "auth.permission_denied")
		return
	}
	if s.runtimeHealth == nil {
		writeError(w, r, http.StatusServiceUnavailable, "monitoring.health_unavailable")
		return
	}
	payload := s.runtimeHealth.Health(r.Context())
	payload["service_kind"], payload["runtime_version"] = s.serviceKind, s.runtimeVersion
	payload["api_contract_version"], payload["api_contract_hash"] = s.apiContractVersion, s.apiContractHash
	readiness := s.readinessSnapshot(r.Context())
	payload["ready"], payload["readiness"] = readiness.Status == "ok", readiness
	payload["project_key"], payload["project_model_hash"] = s.projectKey, s.projectModelHash
	payload["readiness_report_version"], payload["release_identity"] = "runtime.readiness.v1", s.releaseIdentity
	payload["runtime_catalog_urls"] = []string{"/automation/execution-catalog", "/integration/catalog", "/operations/catalog"}
	payload["api"], payload["capacity"] = s.httpMetricsSummary(), s.capacityController.Snapshot()
	writeJSON(w, http.StatusOK, payload)
}

func healthAllowed(principal principalmodel.Principal) bool {
	return principal.Known
}

func (s *HTTPRouter) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *HTTPRouter) ready(w http.ResponseWriter, r *http.Request) {
	s.writeProbe(w, s.readinessSnapshot(r.Context()))
}

func (s *HTTPRouter) startup(w http.ResponseWriter, r *http.Request) {
	checks := []healthplatform.Check{
		{Name: "project_model", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(context.Context) error {
			if s.projectKey != "" && s.projectModelHash != "" {
				return nil
			}
			return errors.New("project model unavailable")
		}},
		{Name: "workers", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(context.Context) error {
			if s.healthRegistry.StartupComplete() {
				return nil
			}
			return errors.New("workers not initialized")
		}},
	}
	s.writeProbe(w, s.healthRegistry.Evaluate(r.Context(), checks))
}

func (s *HTTPRouter) writeProbe(w http.ResponseWriter, snapshot healthplatform.Snapshot) {
	status := http.StatusOK
	if snapshot.Status == "unavailable" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]string{"status": snapshot.Status})
}

func (s *HTTPRouter) MarkStartupComplete() {
	if s != nil && s.healthRegistry != nil {
		s.healthRegistry.MarkStartupComplete()
	}
}

func (s *HTTPRouter) SetDraining(value bool) {
	if s != nil && s.healthRegistry != nil {
		s.healthRegistry.SetDraining(value)
	}
}

func (s *HTTPRouter) SetMaintenance(value bool) {
	if s != nil && s.healthRegistry != nil {
		s.healthRegistry.SetMaintenance(value)
	}
}

func (s *HTTPRouter) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if s.httpMetrics != nil {
		_, _ = w.Write([]byte(s.httpMetrics.Prometheus()))
	}
	if s.technicalMetrics != nil {
		_, _ = w.Write([]byte(s.technicalMetrics(r.Context())))
	}
	if s.capacityController != nil {
		_, _ = w.Write([]byte(capacityOpenMetrics(s.capacityController.Snapshot())))
	}
	if s.businessEvents != nil {
		snapshot := s.businessEvents.Snapshot(r.Context())
		_, _ = fmt.Fprintf(w, "# HELP domainry_runtime_business_event_stream_connections Active business SSE connections.\n# TYPE domainry_runtime_business_event_stream_connections gauge\ndomainry_runtime_business_event_stream_connections %d\n# HELP domainry_runtime_business_event_stream_opened_total Business SSE connections opened.\n# TYPE domainry_runtime_business_event_stream_opened_total counter\ndomainry_runtime_business_event_stream_opened_total %d\n# HELP domainry_runtime_business_event_stream_closed_total Business SSE connections closed.\n# TYPE domainry_runtime_business_event_stream_closed_total counter\ndomainry_runtime_business_event_stream_closed_total %d\n# HELP domainry_runtime_business_event_stream_rejected_total Business SSE connections rejected by capacity limits.\n# TYPE domainry_runtime_business_event_stream_rejected_total counter\ndomainry_runtime_business_event_stream_rejected_total %d\n# HELP domainry_runtime_business_event_stream_open_failed_total Business SSE connections that failed to open in the backplane.\n# TYPE domainry_runtime_business_event_stream_open_failed_total counter\ndomainry_runtime_business_event_stream_open_failed_total %d\n# HELP domainry_runtime_business_events_published_total Business refresh events published.\n# TYPE domainry_runtime_business_events_published_total counter\ndomainry_runtime_business_events_published_total %d\n# HELP domainry_runtime_business_events_publish_failed_total Business refresh events that failed to publish.\n# TYPE domainry_runtime_business_events_publish_failed_total counter\ndomainry_runtime_business_events_publish_failed_total %d\n", snapshot.ActiveConnections, snapshot.OpenedTotal, snapshot.ClosedTotal, snapshot.RejectedTotal, snapshot.OpenFailedTotal, snapshot.PublishedTotal, snapshot.PublishFailedTotal)
	}
	_, _ = w.Write([]byte("# EOF\n"))
}
