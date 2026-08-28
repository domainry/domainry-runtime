package runtime

import (
	"context"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
)

// startControlledWorker supervises one owner independently. Durable desired
// state is polled from the shared database, so pause, maintenance and drain are
// reconstructed after restart and observed by every Runtime instance.
func (a *Runtime) startControlledWorker(parent context.Context, owner string, start func(context.Context) <-chan struct{}) {
	if a == nil || a.store == nil || start == nil {
		return
	}
	a.startTrackedWorker(parent, func(supervisorCtx context.Context) <-chan struct{} {
		done := make(chan struct{})
		go a.runControlledWorker(supervisorCtx, owner, start, done)
		return done
	})
}

func (a *Runtime) runControlledWorker(ctx context.Context, owner string, start func(context.Context) <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	pollInterval := a.operationsControlPollInterval
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	poll := time.NewTimer(pollInterval)
	defer poll.Stop()
	var childCancel context.CancelFunc
	var childDone <-chan struct{}
	reconcile := func() {
		controls, err := a.operationsControls(ctx)
		globalPaused := err != nil || operationsControlActive(controls, operationsmodel.OperationsControlMaintenance, "runtime") ||
			operationsControlActive(controls, operationsmodel.OperationsControlInstanceDrain, a.worker.WorkerID.String())
		paused := globalPaused || operationsControlActive(controls, operationsmodel.OperationsControlWorkerPause, owner)
		if paused && childCancel != nil {
			if globalPaused {
				a.worker.Control.Drain()
				a.worker.Control.WaitIdle(5 * time.Second)
			}
			childCancel()
			return
		}
		if !paused && childCancel == nil {
			a.worker.Control.Undrain()
			childCtx, cancel := context.WithCancel(ctx)
			childCancel, childDone = cancel, start(childCtx)
		}
	}
	reconcile()
	for {
		select {
		case <-ctx.Done():
			if childCancel != nil {
				childCancel()
			}
			if childDone != nil {
				waitForControlledWorker(childDone, 5*time.Second)
			}
			return
		case <-childDone:
			childCancel, childDone = nil, nil
		case <-poll.C:
			reconcile()
			poll.Reset(pollInterval)
		}
	}
}

func waitForControlledWorker(done <-chan struct{}, timeout time.Duration) {
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (a *Runtime) operationsControls(ctx context.Context) (map[string]operationsmodel.OperationsControl, error) {
	if a == nil {
		return nil, context.Canceled
	}
	interval := a.operationsControlPollInterval
	if interval <= 0 {
		interval = time.Second
	}
	now := time.Now()
	a.operationsControlMu.Lock()
	defer a.operationsControlMu.Unlock()
	if !a.operationsControlRefreshedAt.IsZero() && now.Sub(a.operationsControlRefreshedAt) < interval {
		return a.operationsControlSnapshot, a.operationsControlSnapshotErr
	}
	loader := a.operationsControlLoader
	if loader == nil {
		loader = func(ctx context.Context) ([]operationsmodel.OperationsControl, error) {
			return operationspersistence.NewOperationsStore(a.store).ListOperationsControls(ctx, operationsmodel.OperationsSystemPurposeRuntimeControl, "", 100)
		}
	}
	controls, err := loader(ctx)
	snapshot := make(map[string]operationsmodel.OperationsControl, len(controls))
	for _, control := range controls {
		snapshot[operationsControlKey(control.Kind, control.Owner)] = control
	}
	a.operationsControlSnapshot = snapshot
	a.operationsControlSnapshotErr = err
	a.operationsControlRefreshedAt = now
	return snapshot, err
}

func operationsControlActive(controls map[string]operationsmodel.OperationsControl, kind operationsmodel.OperationsControlKind, owner string) bool {
	control, found := controls[operationsControlKey(kind, owner)]
	return found && control.Active()
}

func operationsControlKey(kind operationsmodel.OperationsControlKind, owner string) string {
	return string(kind) + "\x00" + owner
}
