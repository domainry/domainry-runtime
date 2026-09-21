package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

type runtimeReleaseBootstrapHeartbeat struct {
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once

	mu    sync.Mutex
	lease deploymentmodel.RuntimeReleaseCohortLease
	err   error
}

func startRuntimeReleaseBootstrapHeartbeat(
	parent context.Context,
	interval time.Duration,
	lease deploymentmodel.RuntimeReleaseCohortLease,
	heartbeat func(context.Context, deploymentmodel.RuntimeReleaseCohortLease) (deploymentmodel.RuntimeReleaseCohortLease, error),
) *runtimeReleaseBootstrapHeartbeat {
	if parent == nil || interval <= 0 || lease.InstanceID == "" || heartbeat == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	keeper := &runtimeReleaseBootstrapHeartbeat{cancel: cancel, done: make(chan struct{}), lease: lease}
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
				next, err := heartbeat(ctx, current)
				if err != nil {
					if ctx.Err() != nil {
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
