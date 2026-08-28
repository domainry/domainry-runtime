package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func (a *Runtime) Close() error {
	return a.close(nil)
}

// CloseContext releases the shared release-cohort lease with the caller's
// process lifecycle context. Generic callers may use Close; project Runtime
// hosts use this method so a clean shutdown does not wait for lease expiry.
func (a *Runtime) CloseContext(ctx context.Context) error {
	return a.close(ctx)
}

func (a *Runtime) close(ctx context.Context) error {
	if a.api != nil {
		a.api.SetDraining(true)
	}
	if err := a.stopWorkers(a.cfg.HTTPShutdownTimeout); err != nil {
		return err
	}
	var releaseErr error
	releaseLease := a.runtimeReleaseLease()
	if ctx != nil && releaseLease.InstanceID != "" && a.releaseCohort != nil {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.HTTPShutdownTimeout)
		releaseErr = a.releaseCohort.Leave(releaseCtx, releaseLease)
		cancel()
		if releaseErr == nil {
			a.replaceRuntimeReleaseLease(deploymentmodel.RuntimeReleaseCohortLease{})
		}
	}
	if a.borrowedStore {
		return releaseErr
	}
	return errors.Join(releaseErr, a.store.Close())
}

func (a *Runtime) startTrackedWorker(parent context.Context, start func(context.Context) <-chan struct{}) {
	if parent == nil {
		return
	}
	a.workersMu.Lock()
	defer a.workersMu.Unlock()
	if a.workersClosing || parent.Err() != nil {
		return
	}
	if !a.worker.Valid() {
		a.worker = workerplatform.NormalizeDependencies(a.worker)
	}
	workerCtx, cancel := context.WithCancel(parent)
	a.workerCancels = append(a.workerCancels, cancel)
	a.workerDone = append(a.workerDone, start(workerCtx))
}

func (a *Runtime) stopWorkers(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	a.workersMu.Lock()
	if !a.worker.Valid() {
		a.worker = workerplatform.NormalizeDependencies(a.worker)
	}
	worker := a.worker
	a.workersClosing = true
	cancels := append([]context.CancelFunc(nil), a.workerCancels...)
	done := append([]<-chan struct{}(nil), a.workerDone...)
	a.workersMu.Unlock()
	started := worker.Clock.Now()
	worker.Control.Drain()
	if !worker.Control.WaitIdle(timeout) {
		for _, cancel := range cancels {
			cancel()
		}
		worker.Control.Stop()
		return fmt.Errorf("runtime worker drain timed out after %s with %d in-flight", timeout, worker.Control.Snapshot().InFlight)
	}
	for _, cancel := range cancels {
		cancel()
	}
	deadline := started.Add(timeout)
	for index, workerDone := range done {
		remaining := deadline.Sub(worker.Clock.Now())
		if remaining <= 0 {
			worker.Control.Stop()
			return fmt.Errorf("runtime worker shutdown timed out after %s (%d/%d stopped)", timeout, index, len(done))
		}
		timer := time.NewTimer(remaining)
		select {
		case <-workerDone:
			timer.Stop()
		case <-timer.C:
			worker.Control.Stop()
			return fmt.Errorf("runtime worker shutdown timed out after %s (%d/%d stopped)", timeout, index, len(done))
		}
	}
	worker.Control.Stop()
	return nil
}
