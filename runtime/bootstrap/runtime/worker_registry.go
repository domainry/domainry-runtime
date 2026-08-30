package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

// CloseContext releases the shared release-cohort lease with the caller's
// process lifecycle context and closes every Runtime-owned dependency.
func (a *Runtime) CloseContext(ctx context.Context) error {
	return a.close(ctx)
}

func (a *Runtime) close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("runtime shutdown context is required")
	}
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
	var notificationErr error
	if a.notificationBinding != nil {
		closeParent := context.WithoutCancel(ctx)
		closeCtx, cancel := context.WithTimeout(closeParent, a.cfg.HTTPShutdownTimeout)
		notificationErr = a.notificationBinding.Close(closeCtx)
		cancel()
		a.notificationBinding = nil
	}
	var monitoringErr error
	if a.monitoringBinding != nil {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.HTTPShutdownTimeout)
		monitoringErr = a.monitoringBinding.Close(closeCtx)
		cancel()
		a.monitoringBinding = nil
	}
	var schedulerErr error
	if a.schedulerBinding != nil {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.HTTPShutdownTimeout)
		schedulerErr = a.schedulerBinding.Close(closeCtx)
		cancel()
		a.schedulerBinding = nil
	}
	var dataExchangeErr error
	if a.dataExchangeBinding != nil {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.HTTPShutdownTimeout)
		dataExchangeErr = a.dataExchangeBinding.Close(closeCtx)
		cancel()
		a.dataExchangeBinding = nil
	}
	var agentErr error
	if a.agentBinding != nil {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.HTTPShutdownTimeout)
		agentErr = a.agentBinding.Close(closeCtx)
		cancel()
		a.agentBinding = nil
	}
	var partyErr error
	if a.partyBinding != nil {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.cfg.HTTPShutdownTimeout)
		partyErr = a.partyBinding.Close(closeCtx)
		cancel()
		a.partyBinding = nil
	}
	if a.borrowedStore {
		return errors.Join(releaseErr, notificationErr, monitoringErr, schedulerErr, dataExchangeErr, agentErr, partyErr)
	}
	return errors.Join(releaseErr, notificationErr, monitoringErr, schedulerErr, dataExchangeErr, agentErr, partyErr, a.store.Close())
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
