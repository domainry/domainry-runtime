package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

func (a *Runtime) checkRuntimeReleaseAdmission() error {
	a.releaseMu.Lock()
	defer a.releaseMu.Unlock()
	return a.checkRuntimeReleaseAdmissionLocked()
}

func (a *Runtime) checkRuntimeReleaseAdmissionLocked() error {
	if err := a.releaseAdmission.Check(); err != nil {
		return err
	}
	if a.releaseLease.InstanceID != "" && !a.worker.Clock.Now().Before(a.releaseLease.ExpiresAt) {
		return deploymentmodel.ErrRuntimeReleaseLeaseExpired
	}
	return nil
}

func (a *Runtime) fenceRuntimeReleaseWorkersLocked() {
	a.worker.Control.Drain()
	if a.cancelReleaseWorkers != nil {
		a.cancelReleaseWorkers()
		a.cancelReleaseWorkers = nil
		a.releaseWorkersContext = nil
	}
}

// Workers share the controller with operational controls. The supervisor
// calls this only after checking durable pause and drain controls.
func (a *Runtime) beginReleaseWorker(ctx context.Context) (context.Context, context.CancelFunc) {
	a.workersMu.Lock()
	defer a.workersMu.Unlock()
	a.releaseMu.Lock()
	defer a.releaseMu.Unlock()
	if a.workersClosing || ctx.Err() != nil {
		return nil, nil
	}
	if a.checkRuntimeReleaseAdmissionLocked() != nil {
		a.fenceRuntimeReleaseWorkersLocked()
		return nil, nil
	}
	a.worker.Control.Undrain()
	child, cancel := context.WithCancel(ctx)
	if a.releaseLease.InstanceID == "" {
		return child, cancel
	}
	if a.releaseWorkersContext == nil {
		a.releaseWorkersContext, a.cancelReleaseWorkers = context.WithCancel(context.Background())
	}
	stop := context.AfterFunc(a.releaseWorkersContext, cancel)
	return child, func() {
		stop()
		cancel()
	}
}

func (a *Runtime) refreshRuntimeReleaseLease(ctx context.Context, interval time.Duration) error {
	lease := a.runtimeReleaseLease()
	clock := a.worker.Clock.Now
	next, err := renewRuntimeReleaseLease(ctx, lease, interval, clock, a.releaseCohort.Heartbeat)
	if errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseExpired) {
		a.releaseMu.Lock()
		a.releaseLease.ExpiresAt = time.Time{}
		a.fenceRuntimeReleaseWorkersLocked()
		a.releaseMu.Unlock()

		next, err = a.recoverRuntimeReleaseLease(ctx, lease, interval)
	}
	if err == nil {
		a.replaceRuntimeReleaseLease(next)
		return nil
	}
	if errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseLost) && ctx.Err() == nil {
		a.releaseMu.Lock()
		a.releaseAdmission.Fail(err)
		a.fenceRuntimeReleaseWorkersLocked()
		a.releaseMu.Unlock()
		if a.api != nil {
			a.api.SetDraining(true)
		}
	}
	return err
}

// An expired instance stays fenced while retrying. The repository checks
// the original generation and instance record on every attempt.
func (a *Runtime) recoverRuntimeReleaseLease(
	ctx context.Context,
	lease deploymentmodel.RuntimeReleaseCohortLease,
	interval time.Duration,
) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	reportedFailure := false
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, interval/3)
		next, err := a.releaseCohort.Recover(attemptCtx, lease, a.worker.Clock.Now())
		attemptErr := attemptCtx.Err()
		cancel()
		if err == nil {
			err = attemptErr
		}
		if err == nil {
			return next, nil
		}
		if errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseLost) {
			return lease, err
		}
		if !reportedFailure && ctx.Err() == nil {
			logging.RecordFailure(ctx, "runtime_release_recovery_retry", err, logging.Failure{Stage: "release_lease_recovery", Dependency: "database.runtime", Retryable: true})
			reportedFailure = true
		}
		waitForRuntimeReleaseRetry(ctx, interval/5)
	}
	return lease, ctx.Err()
}

// A failed database request can leave the previous lease valid. The last
// confirmed expiry bounds all retries and still fences traffic at expiry.
func renewRuntimeReleaseLease(
	ctx context.Context,
	lease deploymentmodel.RuntimeReleaseCohortLease,
	interval time.Duration,
	clock func() time.Time,
	heartbeat func(context.Context, deploymentmodel.RuntimeReleaseCohortLease, time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error),
) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	leaseCtx, cancel := context.WithTimeout(ctx, lease.ExpiresAt.Sub(clock()))
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return lease, err
		}
		now := clock()
		if leaseCtx.Err() != nil || !now.Before(lease.ExpiresAt) {
			return lease, deploymentmodel.ErrRuntimeReleaseLeaseExpired
		}

		attemptCtx, endAttempt := context.WithTimeout(leaseCtx, interval/3)
		next, err := heartbeat(attemptCtx, lease, now)
		attemptErr := attemptCtx.Err()
		endAttempt()
		if err == nil {
			err = attemptErr
		}
		if errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseLost) || errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseExpired) {
			return lease, err
		}
		if leaseCtx.Err() != nil || !clock().Before(lease.ExpiresAt) {
			if err := ctx.Err(); err != nil {
				return lease, err
			}
			return lease, deploymentmodel.ErrRuntimeReleaseLeaseExpired
		}
		if err == nil {
			return next, nil
		}

		logging.RecordFailure(ctx, "runtime_release_heartbeat_retry", err, logging.Failure{Stage: "release_lease_renewal", Dependency: "database.runtime", Retryable: true})
		waitForRuntimeReleaseRetry(leaseCtx, interval/5)
	}
}

func waitForRuntimeReleaseRetry(ctx context.Context, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
