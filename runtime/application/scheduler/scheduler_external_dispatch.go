package scheduler

import (
	"context"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulermodulehost "github.com/domainry/domainry-runtime/runtime/modulehost/scheduler"
)

// DispatchOwnedTrigger executes downstream Runtime semantics for a run whose
// lifecycle and durable evidence are owned by the extracted Scheduler. It must
// not create, lease or finish Scheduler-owned run records.
func (s *SchedulerApplicationService) DispatchOwnedTrigger(ctx context.Context, definition PublishedDefinition, runID string, scheduledFor time.Time, limit int, principal principalmodel.Principal) (string, error) {
	if s == nil || s.downstream == nil {
		return "", schedulerErrorUnavailable("backend.scheduler.runtime_operation_executor_unavailable")
	}
	return s.downstream.Dispatch(ctx, schedulermodulehost.PublishedDefinition{Key: definition.Key, Data: definition.Data}, runID, scheduledFor, limit, principal)
}
