package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

type runtimeReleaseBootstrapHeartbeat struct {
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once
	clock    func() time.Time

	mu    sync.Mutex
	lease deploymentmodel.RuntimeReleaseCohortLease
	err   error
}

func startRuntimeReleaseBootstrapHeartbeat(
	parent context.Context,
	interval time.Duration,
	lease deploymentmodel.RuntimeReleaseCohortLease,
	clock func() time.Time,
	heartbeat func(context.Context, deploymentmodel.RuntimeReleaseCohortLease, time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error),
) *runtimeReleaseBootstrapHeartbeat {
	if parent == nil || interval <= 0 || lease.InstanceID == "" || clock == nil || heartbeat == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	keeper := &runtimeReleaseBootstrapHeartbeat{cancel: cancel, done: make(chan struct{}), clock: clock, lease: lease}
	go func() {
		defer close(keeper.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := keeper.currentLease()
				next, err := renewRuntimeReleaseLease(ctx, current, interval, clock, heartbeat)
				if err != nil {
					if ctx.Err() != nil && !errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseLost) {
						return
					}
					keeper.fail(fmt.Errorf("maintain Runtime release cohort during startup: %w", err))
					return
				}
				keeper.replaceLease(next)
			}
		}
	}()
	return keeper
}

func (h *runtimeReleaseBootstrapHeartbeat) Stop() (deploymentmodel.RuntimeReleaseCohortLease, error) {
	if h == nil {
		return deploymentmodel.RuntimeReleaseCohortLease{}, nil
	}
	h.stopOnce.Do(func() {
		h.cancel()
		<-h.done
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.err == nil && !h.clock().Before(h.lease.ExpiresAt) {
			h.err = fmt.Errorf("hand off Runtime release cohort after startup: %w", deploymentmodel.ErrRuntimeReleaseLeaseLost)
		}
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lease, h.err
}

func (h *runtimeReleaseBootstrapHeartbeat) currentLease() deploymentmodel.RuntimeReleaseCohortLease {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lease
}

func (h *runtimeReleaseBootstrapHeartbeat) replaceLease(lease deploymentmodel.RuntimeReleaseCohortLease) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lease = lease
}

func (h *runtimeReleaseBootstrapHeartbeat) fail(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.err = err
}
