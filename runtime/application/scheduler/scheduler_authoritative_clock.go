package scheduler

import (
	"context"
	"time"
)

// SchedulerAuthoritativeClock supplies the database server time used for lease
// arbitration. Tests can omit it and retain their injected deterministic clock.
type SchedulerAuthoritativeClock interface {
	SchedulerNow(context.Context) (time.Time, error)
}

func (s *SchedulerApplicationService) authoritativeNow(ctx context.Context, fallback time.Time) (time.Time, error) {
	if s.authoritativeClock == nil {
		return fallback.UTC(), nil
	}
	now, err := s.authoritativeClock.SchedulerNow(ctx)
	if err != nil {
		return time.Time{}, internalError("read Scheduler database time", err)
	}
	return now.UTC(), nil
}
