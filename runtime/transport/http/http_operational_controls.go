package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
	healthplatform "github.com/domainry/domainry-runtime/runtime/platform/health"
)

func (s *HTTPRouter) withOperationalControls(routes *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.operationsControlState == nil || operationalControlExempt(r, routePolicyFor(routes, r).path) {
			next.ServeHTTP(w, r)
			return
		}
		maintenance, _, maintenanceErr := s.operationsControlState(r.Context(), "maintenance", "runtime")
		draining, _, drainErr := s.operationsControlState(r.Context(), "instance_drain", s.runtimeInstanceID)
		if maintenanceErr != nil || drainErr != nil {
			w.Header().Set("Retry-After", "5")
			writeError(w, r, http.StatusServiceUnavailable, "backend.operations.control_state_unavailable")
			return
		}
		s.healthRegistry.SetMaintenance(maintenance)
		s.healthRegistry.SetDraining(draining)
		if maintenance || draining {
			w.Header().Set("Retry-After", "5")
			code := "backend.operations.maintenance_active"
			if draining {
				code = "backend.operations.instance_draining"
			}
			writeError(w, r, http.StatusServiceUnavailable, code)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *HTTPRouter) refreshOperationalState(ctx context.Context) {
	if s == nil || s.operationsControlState == nil || s.healthRegistry == nil {
		return
	}
	if active, _, err := s.operationsControlState(ctx, "maintenance", "runtime"); err == nil {
		s.healthRegistry.SetMaintenance(active)
	}
	if active, _, err := s.operationsControlState(ctx, "instance_drain", s.runtimeInstanceID); err == nil {
		s.healthRegistry.SetDraining(active)
	}
}

func (s *HTTPRouter) readinessSnapshot(ctx context.Context) healthplatform.Snapshot {
	s.refreshOperationalState(ctx)
	checks := []healthplatform.Check{
		{Name: "startup", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(context.Context) error {
			if s.healthRegistry.StartupComplete() {
				return nil
			}
			return errors.New("startup incomplete")
		}},
		{Name: "database", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(ctx context.Context) error {
			if s.runtimeStatus == nil {
				return errors.New("runtime status unavailable")
			}
			return s.runtimeStatus.StorageReadiness(ctx)
		}},
		{Name: "migration", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(ctx context.Context) error {
			if s.runtimeStatus == nil {
				return errors.New("runtime status unavailable")
			}
			return s.runtimeStatus.MigrationReadiness(ctx)
		}},
		{Name: "drain", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(context.Context) error {
			workerDraining := s.workerControl != nil && s.workerControl.Snapshot().State == workerplatform.ShutdownDraining
			if !s.healthRegistry.Draining() && !s.healthRegistry.Maintenance() && !workerDraining {
				return nil
			}
			return errors.New("runtime unavailable for new work")
		}},
		{Name: "release_cohort", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(context.Context) error {
			if s.runtimeReleaseAdmission == nil {
				return nil
			}
			return s.runtimeReleaseAdmission()
		}},
		{Name: "release_build", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(ctx context.Context) error {
			if s.runtimeReleaseIntegrity == nil {
				return nil
			}
			return s.runtimeReleaseIntegrity.BuildReadiness(ctx)
		}},
		{Name: "release_signature", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(ctx context.Context) error {
			if s.runtimeReleaseIntegrity == nil {
				return nil
			}
			return s.runtimeReleaseIntegrity.SignatureReadiness(ctx)
		}},
		{Name: "release_schema", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(ctx context.Context) error {
			if s.runtimeReleaseIntegrity == nil {
				return nil
			}
			return s.runtimeReleaseIntegrity.SchemaReadiness(ctx)
		}},
		{Name: "release_registry", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(ctx context.Context) error {
			if s.runtimeReleaseIntegrity == nil {
				return nil
			}
			return s.runtimeReleaseIntegrity.RegistryReadiness(ctx)
		}},
		{Name: "capacity", Criticality: healthplatform.Critical, Timeout: s.healthCheckTimeout, Run: func(context.Context) error {
			if s.capacityController != nil && s.capacityController.State() == capacityplatform.Overloaded {
				return errors.New("runtime capacity overloaded")
			}
			return nil
		}},
	}
	return s.healthRegistry.Evaluate(ctx, checks)
}

func operationalControlExempt(r *http.Request, routePath string) bool {
	if r == nil {
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	path := strings.TrimSpace(routePath)
	return strings.HasPrefix(path, "/operations/") || path == "/operations"
}
