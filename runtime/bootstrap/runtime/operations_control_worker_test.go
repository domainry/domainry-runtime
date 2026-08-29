package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOperationsControlsShareOneLoadPerPollInterval(t *testing.T) {
	var loads atomic.Int32
	runtime := &Runtime{
		operationsControlPollInterval: time.Minute,
		operationsControlLoader: func(context.Context) ([]operationsmodel.OperationsControl, error) {
			loads.Add(1)
			return []operationsmodel.OperationsControl{{
				Kind: operationsmodel.OperationsControlWorkerPause, Owner: "workflow", State: operationsmodel.OperationsControlActive,
			}}, nil
		},
	}
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			controls, err := runtime.operationsControls(t.Context())
			if err != nil || !operationsControlActive(controls, operationsmodel.OperationsControlWorkerPause, "workflow") {
				t.Errorf("shared controls active=false err=%v", err)
			}
		}()
	}
	group.Wait()
	if got := loads.Load(); got != 1 {
		t.Fatalf("control snapshot loads=%d want=1", got)
	}
}

func TestOperationsControlsRefreshAndFailClosed(t *testing.T) {
	var loads atomic.Int32
	failure := errors.New("control store unavailable")
	runtime := &Runtime{
		operationsControlPollInterval: time.Millisecond,
		operationsControlLoader: func(context.Context) ([]operationsmodel.OperationsControl, error) {
			if loads.Add(1) == 1 {
				return nil, failure
			}
			return []operationsmodel.OperationsControl{{
				Kind: operationsmodel.OperationsControlMaintenance, Owner: "runtime", State: operationsmodel.OperationsControlActive,
			}}, nil
		},
	}
	if _, err := runtime.operationsControls(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("first refresh error=%v", err)
	}
	time.Sleep(2 * time.Millisecond)
	controls, err := runtime.operationsControls(t.Context())
	if err != nil || !operationsControlActive(controls, operationsmodel.OperationsControlMaintenance, "runtime") {
		t.Fatalf("refreshed maintenance active=false err=%v", err)
	}
}

func TestControlledWorkersReconstructOwnerPauseAndResumeIndependently(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "worker-controls.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	workerID, _ := workerplatform.NewWorkerID("instance-a")
	runtime := &Runtime{store: store, worker: workerplatform.NewDependencies(workerID), operationsControlPollInterval: 10 * time.Millisecond}
	controls := operationspersistence.NewOperationsStore(store)
	now := time.Now().UTC()
	pauseWorkflow := operationsmodel.OperationsControl{SystemPurpose: operationsmodel.OperationsSystemPurposeRuntimeControl, Kind: operationsmodel.OperationsControlWorkerPause, Owner: "workflow", State: operationsmodel.OperationsControlActive, Reason: "incident", UpdatedBy: "admin", Revision: 1, UpdatedAt: now}
	if changed, err := controls.PutOperationsControl(t.Context(), pauseWorkflow, 0); err != nil || !changed {
		t.Fatalf("pause workflow changed=%v err=%v", changed, err)
	}

	var workflowStarts, integrationStarts, integrationStops atomic.Int32
	runtime.startControlledWorker(t.Context(), "workflow", operationsTestWorker(&workflowStarts, nil))
	runtime.startControlledWorker(t.Context(), "integration_event", operationsTestWorker(&integrationStarts, &integrationStops))
	operationsEventually(t, time.Second, func() bool { return integrationStarts.Load() == 1 })
	if workflowStarts.Load() != 0 {
		t.Fatalf("paused workflow starts=%d", workflowStarts.Load())
	}

	pauseWorkflow.State, pauseWorkflow.Revision, pauseWorkflow.UpdatedAt = operationsmodel.OperationsControlInactive, 2, time.Now().UTC()
	if changed, err := controls.PutOperationsControl(t.Context(), pauseWorkflow, 1); err != nil || !changed {
		t.Fatalf("resume workflow changed=%v err=%v", changed, err)
	}
	operationsEventually(t, time.Second, func() bool { return workflowStarts.Load() == 1 })

	pauseIntegration := operationsmodel.OperationsControl{SystemPurpose: operationsmodel.OperationsSystemPurposeRuntimeControl, Kind: operationsmodel.OperationsControlWorkerPause, Owner: "integration_event", State: operationsmodel.OperationsControlActive, Reason: "provider incident", UpdatedBy: "admin", Revision: 1, UpdatedAt: time.Now().UTC()}
	if changed, err := controls.PutOperationsControl(t.Context(), pauseIntegration, 0); err != nil || !changed {
		t.Fatalf("pause integration changed=%v err=%v", changed, err)
	}
	operationsEventually(t, time.Second, func() bool { return integrationStops.Load() == 1 })
	if workflowStarts.Load() != 1 {
		t.Fatalf("unrelated workflow restarted or stopped: %d", workflowStarts.Load())
	}
	if err := runtime.stopWorkers(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestControlledWorkersSupportTwoInstanceRollingDrain(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "worker-rolling-drain.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	workerA, _ := workerplatform.NewWorkerID("instance-a")
	workerB, _ := workerplatform.NewWorkerID("instance-b")
	runtimeA := &Runtime{store: store, worker: workerplatform.NewDependencies(workerA), operationsControlPollInterval: 10 * time.Millisecond}
	runtimeB := &Runtime{store: store, worker: workerplatform.NewDependencies(workerB), operationsControlPollInterval: 10 * time.Millisecond}
	var startsA, startsB, stopsA, stopsB atomic.Int32
	runtimeA.startControlledWorker(t.Context(), "workflow", operationsTestWorker(&startsA, &stopsA))
	runtimeB.startControlledWorker(t.Context(), "workflow", operationsTestWorker(&startsB, &stopsB))
	operationsEventually(t, time.Second, func() bool { return startsA.Load() == 1 && startsB.Load() == 1 })

	controls := operationspersistence.NewOperationsStore(store)
	drain := operationsmodel.OperationsControl{SystemPurpose: operationsmodel.OperationsSystemPurposeRuntimeControl, Kind: operationsmodel.OperationsControlInstanceDrain, Owner: "instance-a", State: operationsmodel.OperationsControlActive, Reason: "rolling deployment", UpdatedBy: "admin", Revision: 1, UpdatedAt: time.Now().UTC()}
	if changed, err := controls.PutOperationsControl(t.Context(), drain, 0); err != nil || !changed {
		t.Fatalf("drain instance A changed=%v err=%v", changed, err)
	}
	operationsEventually(t, time.Second, func() bool { return stopsA.Load() == 1 })
	if stopsB.Load() != 0 || startsB.Load() != 1 {
		t.Fatalf("instance B was interrupted by instance A drain: starts=%d stops=%d", startsB.Load(), stopsB.Load())
	}

	drain.State, drain.Revision, drain.UpdatedAt = operationsmodel.OperationsControlInactive, 2, time.Now().UTC()
	if changed, err := controls.PutOperationsControl(t.Context(), drain, 1); err != nil || !changed {
		t.Fatalf("undrain instance A changed=%v err=%v", changed, err)
	}
	operationsEventually(t, time.Second, func() bool { return startsA.Load() == 2 })
	if err := runtimeA.stopWorkers(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := runtimeB.stopWorkers(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestControlledInstanceDrainWaitsForInFlightTick(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "worker-inflight-drain.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	workerID, _ := workerplatform.NewWorkerID("instance-a")
	runtime := &Runtime{store: store, worker: workerplatform.NewDependencies(workerID), operationsControlPollInterval: 10 * time.Millisecond}
	started, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	runtime.startControlledWorker(t.Context(), "workflow", func(ctx context.Context) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if !runtime.worker.Control.TryBegin() {
				return
			}
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				cancelled <- struct{}{}
			}
			runtime.worker.Control.End()
			<-ctx.Done()
		}()
		return done
	})
	<-started
	control := operationsmodel.OperationsControl{SystemPurpose: operationsmodel.OperationsSystemPurposeRuntimeControl, Kind: operationsmodel.OperationsControlInstanceDrain, Owner: "instance-a", State: operationsmodel.OperationsControlActive, Reason: "rolling deployment", UpdatedBy: "admin", Revision: 1, UpdatedAt: time.Now().UTC()}
	if changed, err := operationspersistence.NewOperationsStore(store).PutOperationsControl(t.Context(), control, 0); err != nil || !changed {
		t.Fatalf("activate drain changed=%v err=%v", changed, err)
	}
	time.Sleep(30 * time.Millisecond)
	select {
	case <-cancelled:
		t.Fatal("durable instance drain cancelled in-flight work before it completed")
	default:
	}
	close(release)
	operationsEventually(t, time.Second, func() bool { return runtime.worker.Control.Snapshot().InFlight == 0 })
	if err := runtime.stopWorkers(time.Second); err != nil {
		t.Fatal(err)
	}
}

func operationsTestWorker(starts, stops *atomic.Int32) func(context.Context) <-chan struct{} {
	return func(ctx context.Context) <-chan struct{} {
		starts.Add(1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-ctx.Done()
			if stops != nil {
				stops.Add(1)
			}
		}()
		return done
	}
}

func operationsEventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached before timeout")
}
