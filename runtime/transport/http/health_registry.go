package http

import (
	"context"
	"sync/atomic"

	healthplatform "github.com/domainry/domainry-foundation/health"
)

// runtimeHealthRegistry keeps Runtime readiness lifecycle separate from the
// reusable Foundation health-check evaluator.
type runtimeHealthRegistry struct {
	checks      *healthplatform.Registry
	startupDone atomic.Bool
	draining    atomic.Bool
	maintenance atomic.Bool
}

func newRuntimeHealthRegistry() *runtimeHealthRegistry {
	return &runtimeHealthRegistry{checks: healthplatform.NewRegistry()}
}

func (r *runtimeHealthRegistry) Evaluate(ctx context.Context, checks []healthplatform.Check) healthplatform.Snapshot {
	if r == nil || r.checks == nil {
		return healthplatform.Snapshot{Status: "unavailable"}
	}
	return r.checks.Evaluate(ctx, checks)
}

func (r *runtimeHealthRegistry) MarkStartupComplete()      { r.startupDone.Store(true) }
func (r *runtimeHealthRegistry) SetDraining(value bool)    { r.draining.Store(value) }
func (r *runtimeHealthRegistry) SetMaintenance(value bool) { r.maintenance.Store(value) }
func (r *runtimeHealthRegistry) StartupComplete() bool     { return r.startupDone.Load() }
func (r *runtimeHealthRegistry) Draining() bool            { return r.draining.Load() }
func (r *runtimeHealthRegistry) Maintenance() bool         { return r.maintenance.Load() }
