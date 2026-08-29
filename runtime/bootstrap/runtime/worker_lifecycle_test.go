package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func TestRuntimeStopWorkersCancelsAndWaits(t *testing.T) {
	application := &Runtime{}
	application.startTrackedWorker(t.Context(), func(ctx context.Context) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-ctx.Done()
		}()
		return done
	})
	if err := application.stopWorkers(time.Second); err != nil {
		t.Fatalf("stop workers: %v", err)
	}
}

func TestRuntimeStopWorkersDrainsInFlightBeforeCancellation(t *testing.T) {
	workerID, _ := workerplatform.NewWorkerID("runtime-drain-test")
	runtime := &Runtime{worker: workerplatform.NewDependencies(workerID)}
	started, release, cancelledEarly := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	runtime.startTrackedWorker(t.Context(), func(ctx context.Context) <-chan struct{} {
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
				cancelledEarly <- struct{}{}
			}
			runtime.worker.Control.End()
			<-ctx.Done()
		}()
		return done
	})
	<-started
	result := make(chan error, 1)
	go func() { result <- runtime.stopWorkers(time.Second) }()
	time.Sleep(20 * time.Millisecond)
	select {
	case <-cancelledEarly:
		t.Fatal("worker context was cancelled before in-flight drain")
	default:
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("drain shutdown: %v", err)
	}
}

func TestRuntimeStopWorkersHasBoundedTimeout(t *testing.T) {
	application := &Runtime{}
	application.startTrackedWorker(t.Context(), func(context.Context) <-chan struct{} {
		return make(chan struct{})
	})
	started := time.Now()
	if err := application.stopWorkers(20 * time.Millisecond); err == nil {
		t.Fatal("expected shutdown timeout")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("worker shutdown was not bounded: %s", elapsed)
	}
}

func TestRuntimeStopWorkersRejectsAlreadyExpiredDeadline(t *testing.T) {
	done := make(chan struct{})
	close(done)
	workerDone := make([]<-chan struct{}, 1024)
	for index := range workerDone {
		workerDone[index] = done
	}
	runtime := &Runtime{workerDone: workerDone}
	if err := runtime.stopWorkers(time.Nanosecond); err == nil {
		t.Fatal("expired shutdown deadline must fail")
	}
}

func TestRuntimeStartTrackedWorkerRejectsNilCancelledAndClosingParents(t *testing.T) {
	runtime := &Runtime{}
	starts := 0
	start := func(context.Context) <-chan struct{} {
		starts++
		done := make(chan struct{})
		close(done)
		return done
	}
	runtime.startTrackedWorker(nil, start)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	runtime.startTrackedWorker(cancelled, start)
	runtime.workersClosing = true
	runtime.startTrackedWorker(t.Context(), start)
	if starts != 0 || len(runtime.workerCancels) != 0 || len(runtime.workerDone) != 0 {
		t.Fatalf("rejected worker was started: starts=%d cancels=%d done=%d", starts, len(runtime.workerCancels), len(runtime.workerDone))
	}
}

func TestRuntimeStopWorkersIsRepeatableAndUsesDefaultTimeout(t *testing.T) {
	runtime := &Runtime{}
	runtime.startTrackedWorker(t.Context(), func(ctx context.Context) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-ctx.Done()
		}()
		return done
	})
	if err := runtime.stopWorkers(0); err != nil {
		t.Fatalf("first stop: %v", err)
	}
	if err := runtime.stopWorkers(0); err != nil {
		t.Fatalf("repeated stop: %v", err)
	}
}

func TestRuntimeNotificationWorkerSkipsMissingManagementService(t *testing.T) {
	runtime := &Runtime{}
	runtime.StartNotificationPublicationWorker(t.Context())
	if len(runtime.workerCancels) != 0 {
		t.Fatalf("notification worker started without management service: %d", len(runtime.workerCancels))
	}
}

func TestRuntimeStartSchedulerWorkerRegistersControlledOwner(t *testing.T) {
	runtime := New(t.Context(), bootstrapTestConfig(t), runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory())
	doneBefore, cancelsBefore := len(runtime.workerDone), len(runtime.workerCancels)
	runtime.startSchedulerWorker(t.Context())
	if len(runtime.workerDone) != doneBefore+1 || len(runtime.workerCancels) != cancelsBefore+1 {
		t.Fatalf(
			"scheduler supervisor registration delta = done:%d cancels:%d, want 1 each",
			len(runtime.workerDone)-doneBefore,
			len(runtime.workerCancels)-cancelsBefore,
		)
	}
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowContinuationWorkerProcessesImmediateQueue(t *testing.T) {
	workerID, err := workerplatform.NewWorkerID("workflow-continuation-test")
	if err != nil {
		t.Fatal(err)
	}
	dependencies := workerplatform.NewDependencies(workerID)
	ctx, cancel := context.WithCancel(t.Context())
	called := make(chan struct{}, 1)
	done := startWorkflowContinuationWorkerLoop(ctx, time.Hour, 7, dependencies.Control, nil, func(_ context.Context, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
		if limit != 7 {
			t.Errorf("limit = %d, want 7", limit)
		}
		if !principal.Known || !principal.SystemScope.Valid() || principal.WorkspaceID != principalmodel.InstallationWorkspaceID {
			t.Errorf("principal = %#v, want governed system principal", principal)
		}
		called <- struct{}{}
		return workflowmodel.WorkflowProcessResult{Processed: 1}, nil
	}, func(context.Context, workflowapplication.WorkflowContinuationLocator, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
		return workflowmodel.WorkflowProcessResult{}, nil
	})
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("workflow continuation worker did not process the immediate queue")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workflow continuation worker did not stop")
	}
}

func TestWorkflowContinuationWorkerExecutesCommittedLocator(t *testing.T) {
	dependencies := workerplatform.NewDependencies("workflow-continuation-wakeup")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wakeups := make(chan workflowapplication.WorkflowContinuationLocator, 1)
	executed := make(chan workflowapplication.WorkflowContinuationLocator, 1)
	done := startWorkflowContinuationWorkerLoop(ctx, time.Hour, 1, dependencies.Control, wakeups,
		func(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
			return workflowmodel.WorkflowProcessResult{}, nil
		},
		func(_ context.Context, locator workflowapplication.WorkflowContinuationLocator, _ principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
			executed <- locator
			return workflowmodel.WorkflowProcessResult{Processed: 1}, nil
		},
	)
	locator := workflowapplication.WorkflowContinuationLocator{WorkspaceID: "workspace-a", ExecutionID: "execution-a"}
	wakeups <- locator
	select {
	case got := <-executed:
		if got != locator {
			t.Fatalf("locator=%#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("committed continuation was not executed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workflow continuation worker did not stop")
	}
}

func TestRuntimeCloseStopsWorkersBeforeClosingStore(t *testing.T) {
	store, err := prepareRuntimeStore(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "close.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{store: store, cfg: config.Config{HTTPShutdownTimeout: time.Second}}
	runtime.startTrackedWorker(t.Context(), func(ctx context.Context) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-ctx.Done()
		}()
		return done
	})
	if err := runtime.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().PingContext(t.Context()); err == nil {
		t.Fatal("runtime store remained open after Close")
	}
}

func TestRuntimeCloseLeavesStoreOpenWhenWorkerShutdownTimesOut(t *testing.T) {
	store, err := prepareRuntimeStore(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "timeout.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runtime := &Runtime{store: store, cfg: config.Config{HTTPShutdownTimeout: time.Millisecond}}
	runtime.startTrackedWorker(t.Context(), func(context.Context) <-chan struct{} { return make(chan struct{}) })
	if err := runtime.CloseContext(t.Context()); err == nil {
		t.Fatal("worker timeout must fail Close")
	}
	if err := store.DB().PingContext(t.Context()); err != nil {
		t.Fatalf("store closed before workers stopped: %v", err)
	}
}
