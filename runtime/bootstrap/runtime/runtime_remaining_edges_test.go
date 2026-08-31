package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type runtimeReleaseRepositoryStub struct {
	heartbeatErr error
	leaveErr     error
	heartbeats   int
	leaves       int
}

type runtimeNotificationCompilerStub struct{ called bool }

func (s *runtimeNotificationCompilerStub) CompileInboxIntent(intent notificationmodel.NotificationIntent, _ principalmodel.SystemScope) (notificationmodel.NotificationEvent, error) {
	s.called = true
	return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
}

func (*runtimeReleaseRepositoryStub) ClaimRuntimeRelease(context.Context, deploymentmodel.RuntimeReleaseCohortClaim) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	return deploymentmodel.RuntimeReleaseCohortLease{}, nil
}

func (s *runtimeReleaseRepositoryStub) HeartbeatRuntimeRelease(_ context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, _ time.Time, _ time.Duration) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	s.heartbeats++
	lease.Generation++
	return lease, s.heartbeatErr
}

func (s *runtimeReleaseRepositoryStub) ReleaseRuntimeRelease(context.Context, deploymentmodel.RuntimeReleaseCohortLease) error {
	s.leaves++
	return s.leaveErr
}

func TestRuntimeConfigurationHTTPAndBindingRemainingEdges(t *testing.T) {
	cfg := normalizeRuntimeConfig(config.Config{RuntimeInstanceID: " explicit "})
	if cfg.RuntimeInstanceID != " explicit " || cfg.RuntimeAllowDevIdentityHeaders {
		t.Fatalf("normalized config = %#v", cfg)
	}
	assertBootstrapPanic(t, func() { BindHTTP(nil, &Runtime{}) })

	if dependencies, err := newRuntimeWorkerDependencies("runtime-test"); err != nil || !dependencies.Valid() {
		t.Fatalf("worker dependencies=%#v error=%v", dependencies, err)
	}
	generated := normalizeRuntimeConfig(config.Config{Port: "8081"})
	if generated.RuntimeInstanceID == "" {
		t.Fatal("empty Runtime instance ID was not derived from hostname")
	}
	fallback := normalizeRuntimeConfigWithHostname(config.Config{Port: "8081"}, func() (string, error) { return " ", errors.New("hostname unavailable") })
	if fallback.RuntimeInstanceID != "runtime-localhost-8081" {
		t.Fatalf("fallback Runtime instance ID=%q", fallback.RuntimeInstanceID)
	}
}

func TestRuntimeStartupFailsWhenWorkerInitializationFails(t *testing.T) {
	_, err := newRuntimeWorkerDependencies(" ")
	if err == nil {
		t.Fatal("blank Runtime worker identity was accepted")
	}
	assertBootstrapPanic(t, func() { mustCompleteRuntimeStartup(err) })
}

func TestRuntimeWorkerNilAndOptionalOwnerEdges(t *testing.T) {
	var runtime *Runtime
	if RoutesForSurfaceGroup(nil, runtimehttp.SurfaceRouteGroupAll) == nil || RoutesForSurfaceGroup(&Runtime{}, runtimehttp.SurfaceRouteGroupAll) == nil {
		t.Fatal("nil HTTP surface fallback missing")
	}
	runtime.startSchedulerWorker(t.Context())
	runtime.startNotificationChannelWorker(t.Context())
	runtime.startIntegrationCredentialExpiryWorker(t.Context())
	runtime.startRuntimeReleaseHeartbeat(t.Context())
	runtime.startControlledWorker(t.Context(), "owner", func(context.Context) <-chan struct{} { return nil })
	runtime.startDataExchangeWorker(t.Context())
	runtime.startIdempotencyCleanupWorker(t.Context())
	runtime.startLifecycleCleanupWorker(t.Context())

	withoutStore := &Runtime{}
	withoutStore.startSchedulerWorker(t.Context())
	withoutStore.startNotificationChannelWorker(t.Context())
	withoutStore.startIntegrationCredentialExpiryWorker(t.Context())
	withoutStore.StartNotificationPublicationWorker(t.Context())
	withoutStore.startNotificationInboxWorker(t.Context())
	withoutStore.startRuntimeReleaseHeartbeat(t.Context())
	withoutStore.startControlledWorker(t.Context(), "owner", func(context.Context) <-chan struct{} { return nil })
	withStore := &Runtime{store: &persistence.RuntimeStore{}}
	withStore.startControlledWorker(t.Context(), "owner", nil)
	if len(withStore.workerDone) != 0 || len(withStore.workerCancels) != 0 {
		t.Fatal("nil controlled-worker starter was registered")
	}

	if (&Runtime{}).beginWorkerStartup() != true {
		t.Fatal("first worker startup was rejected")
	}
	alreadyStarted := &Runtime{workersStarted: true}
	if alreadyStarted.beginWorkerStartup() {
		t.Fatal("duplicate worker startup was accepted")
	}
	closing := &Runtime{workersClosing: true}
	if closing.beginWorkerStartup() {
		t.Fatal("closing Runtime accepted worker startup")
	}
	StartWorkers(t.Context(), nil)
	empty := &Runtime{}
	StartWorkers(t.Context(), empty)
	StartWorkers(t.Context(), empty)
	partial := &Runtime{records: &composition.RuntimeServices{}}
	partial.startDataExchangeWorker(t.Context())
	partial.startIdempotencyCleanupWorker(t.Context())
	partial.startLifecycleCleanupWorker(t.Context())
}

func TestRuntimeTrackedWorkerGuardBranches(t *testing.T) {
	runtime := &Runtime{}
	runtime.startTrackedWorker(nil, func(context.Context) <-chan struct{} { return nil })
	runtime.workersClosing = true
	runtime.startTrackedWorker(t.Context(), func(context.Context) <-chan struct{} { return nil })
	runtime.workersClosing = false
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	runtime.startTrackedWorker(cancelled, func(context.Context) <-chan struct{} { return nil })
	done := make(chan struct{})
	close(done)
	runtime.startTrackedWorker(t.Context(), func(context.Context) <-chan struct{} { return done })
	if len(runtime.workerDone) != 1 || !runtime.worker.Valid() {
		t.Fatalf("tracked workers=%d dependencies valid=%v", len(runtime.workerDone), runtime.worker.Valid())
	}
}

func TestRuntimeStopWorkersDefaultAndDrainTimeoutEdges(t *testing.T) {
	runtime := &Runtime{}
	if err := runtime.stopWorkers(-time.Second); err != nil {
		t.Fatalf("empty worker registry with default timeout error = %v", err)
	}

	dependencies, err := newRuntimeWorkerDependencies("runtime-blocked")
	if err != nil {
		t.Fatal(err)
	}
	cancelled := false
	blocked := &Runtime{worker: dependencies, workerCancels: []context.CancelFunc{func() { cancelled = true }}}
	if !blocked.worker.Control.TryBegin() {
		t.Fatal("failed to mark worker in flight")
	}
	defer blocked.worker.Control.End()
	if err := blocked.stopWorkers(time.Millisecond); err == nil {
		t.Fatal("in-flight worker drain unexpectedly completed")
	}
	if !cancelled {
		t.Fatal("drain timeout did not cancel registered workers")
	}

	closedDone := make(chan struct{})
	close(closedDone)
	completed := &Runtime{worker: mustRuntimeWorkerDependencies(t, "runtime-completed"), workerDone: []<-chan struct{}{closedDone}}
	if err := completed.stopWorkers(time.Second); err != nil {
		t.Fatalf("completed worker shutdown error=%v", err)
	}

	timedOut := &Runtime{worker: mustRuntimeWorkerDependencies(t, "runtime-timeout"), workerDone: []<-chan struct{}{make(chan struct{})}}
	if err := timedOut.stopWorkers(time.Millisecond); err == nil {
		t.Fatal("stuck worker shutdown unexpectedly completed")
	}

	expiredClock := &advancingRuntimeClock{now: time.Unix(100, 0).UTC(), steps: []time.Duration{0, 2 * time.Second}}
	expiredDependencies := mustRuntimeWorkerDependencies(t, "runtime-expired")
	expiredDependencies.Clock = expiredClock
	expired := &Runtime{worker: expiredDependencies, workerDone: []<-chan struct{}{closedDone}}
	if err := expired.stopWorkers(time.Second); err == nil {
		t.Fatal("expired worker shutdown deadline unexpectedly completed")
	}
}

func TestRuntimeStartsConfiguredWorkerOwners(t *testing.T) {
	const shutdownTimeout = 10 * time.Second

	cfg := bootstrapTestConfig(t)
	cfg.WorkerPollInterval = 0
	cfg.WorkerBatchSize = 0
	runtime := New(t.Context(), cfg, runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	if RoutesForListenerGroup(runtime, runtimehttp.ListenerRouteGroupAll) == nil {
		t.Fatal("assembled Runtime helpers were unavailable")
	}
	StartWorkers(t.Context(), runtime)
	time.Sleep(20 * time.Millisecond)
	if err := runtime.stopWorkers(shutdownTimeout); err != nil {
		t.Fatalf("default worker shutdown error=%v", err)
	}
	if err := runtime.store.Close(); err != nil {
		t.Fatal(err)
	}

	highBatch := New(t.Context(), bootstrapTestConfig(t), runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	highBatch.cfg.WorkerPollInterval = time.Millisecond
	highBatch.cfg.WorkerBatchSize = 26
	highBatch.startDataExchangeWorker(t.Context())
	highBatch.StartNotificationPublicationWorker(t.Context())
	time.Sleep(20 * time.Millisecond)
	if err := highBatch.stopWorkers(shutdownTimeout); err != nil {
		t.Fatalf("high-batch worker shutdown error=%v", err)
	}
	if err := highBatch.store.Close(); err != nil {
		t.Fatal(err)
	}

	mediumBatch := New(t.Context(), bootstrapTestConfig(t), runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	mediumBatch.cfg.WorkerPollInterval = time.Millisecond
	mediumBatch.cfg.WorkerBatchSize = 10
	mediumBatch.startDataExchangeWorker(t.Context())
	time.Sleep(20 * time.Millisecond)
	if err := mediumBatch.stopWorkers(shutdownTimeout); err != nil {
		t.Fatalf("medium-batch worker shutdown error=%v", err)
	}
	if err := mediumBatch.store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeStartupNamedCallbacks(t *testing.T) {
	compiler := &runtimeNotificationCompilerStub{}
	callbacks := &runtimeStartupCallbacks{
		records:                   composition.NewRuntimeServices(t.Context(), composition.RuntimeServicesConfig{}),
		notificationManagement:    compiler,
		workflowNotificationScope: principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test notification compilation"),
	}
	event, err := callbacks.CompileNotification(notificationmodel.NotificationIntent{EventType: "test.event"})
	if err != nil || !compiler.called || event.EventType != "test.event" {
		t.Fatalf("event=%+v called=%v err=%v", event, compiler.called, err)
	}
	_ = callbacks.ReportsForPrincipal(t.Context(), principalmodel.Principal{})
	_ = callbacks.Objects()
	_ = callbacks.Extensions()
}

func TestRuntimeNotificationWorkerBoundsAndLiveErrors(t *testing.T) {
	for _, batch := range []int{10, 101} {
		runtime := New(t.Context(), bootstrapTestConfig(t), runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
		runtime.cfg.WorkerPollInterval = time.Millisecond
		runtime.cfg.WorkerBatchSize = batch
		runtime.startNotificationChannelWorker(t.Context())
		runtime.startNotificationInboxWorker(t.Context())
		time.Sleep(20 * time.Millisecond)
		if err := runtime.stopWorkers(time.Second); err != nil {
			t.Fatal(err)
		}
		if err := runtime.store.Close(); err != nil {
			t.Fatal(err)
		}
	}

	runtime := New(t.Context(), bootstrapTestConfig(t), runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	runtime.cfg.WorkerPollInterval = time.Millisecond
	runtime.cfg.WorkerBatchSize = 10
	if err := runtime.store.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.startNotificationChannelWorker(t.Context())
	runtime.startNotificationInboxWorker(t.Context())
	runtime.startIntegrationCredentialExpiryWorker(t.Context())
	time.Sleep(20 * time.Millisecond)
	if err := runtime.stopWorkers(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeReleaseHeartbeatAndCloseEdges(t *testing.T) {
	lease := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "runtime-release", Generation: 1}
	for _, test := range []struct {
		name      string
		failure   error
		admission error
	}{
		{name: "success"},
		{name: "heartbeat failure", failure: errors.New("heartbeat failed")},
		{name: "already rejected", admission: errors.New("already rejected")},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &runtimeReleaseRepositoryStub{heartbeatErr: test.failure}
			admission := &deploymentapplication.RuntimeReleaseAdmission{}
			admission.Fail(test.admission)
			runtime := &Runtime{
				worker: mustRuntimeWorkerDependencies(t, "release-"+test.name), releaseCohort: deploymentapplication.NewDeploymentRuntimeReleaseCohortApplicationService(repository),
				releaseLease: lease, releaseAdmission: admission,
			}
			StartWorkers(t.Context(), runtime)
			time.Sleep(20 * time.Millisecond)
			if err := runtime.stopWorkers(time.Second); err != nil {
				t.Fatal(err)
			}
			if test.admission != nil && repository.heartbeats != 0 {
				t.Fatalf("rejected admission heartbeat calls=%d", repository.heartbeats)
			}
			if test.admission == nil && repository.heartbeats != 1 {
				t.Fatalf("process worker startup heartbeat calls=%d", repository.heartbeats)
			}
			if test.failure != nil && admission.Check() == nil {
				t.Fatal("heartbeat failure did not reject admission")
			}
		})
	}

	for _, test := range []struct {
		name  string
		lease deploymentmodel.RuntimeReleaseCohortLease
		repo  *runtimeReleaseRepositoryStub
	}{
		{name: "empty lease", repo: &runtimeReleaseRepositoryStub{}},
		{name: "missing cohort", lease: lease},
		{name: "leave success", lease: lease, repo: &runtimeReleaseRepositoryStub{}},
		{name: "leave failure", lease: lease, repo: &runtimeReleaseRepositoryStub{leaveErr: errors.New("leave failed")}},
	} {
		t.Run("close "+test.name, func(t *testing.T) {
			store, err := prepareRuntimeStore(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: t.TempDir() + "/release-close.db"})
			if err != nil {
				t.Fatal(err)
			}
			runtime := &Runtime{store: store, cfg: config.Config{HTTPShutdownTimeout: time.Second}, releaseLease: test.lease}
			if test.repo != nil {
				runtime.releaseCohort = deploymentapplication.NewDeploymentRuntimeReleaseCohortApplicationService(test.repo)
			}
			err = runtime.CloseContext(t.Context())
			if test.repo != nil && test.repo.leaveErr != nil {
				if !errors.Is(err, test.repo.leaveErr) {
					t.Fatalf("close error=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeWorkerTickErrorAndCancellationOutcomes(t *testing.T) {
	failure := errors.New("worker failure")
	loggedCalls := 0
	loggedControl := workerplatform.NewController()
	runLoggedRuntimeWorkerTick(t.Context(), loggedControl, "worker failed", func() error { loggedCalls++; return failure })
	cancelledLogged, cancelLogged := context.WithCancel(t.Context())
	cancelLogged()
	runLoggedRuntimeWorkerTick(cancelledLogged, loggedControl, "worker failed", func() error { loggedCalls++; return failure })
	loggedControl.Drain()
	runLoggedRuntimeWorkerTick(t.Context(), loggedControl, "worker failed", func() error { loggedCalls++; return nil })
	if loggedCalls != 2 {
		t.Fatalf("logged worker calls=%d", loggedCalls)
	}
	for _, run := range []struct {
		name string
		tick func(context.Context, *workerplatform.Controller, func() error, func() error)
	}{
		{name: "lifecycle", tick: runLifecycleCleanupTick},
	} {
		t.Run(run.name, func(t *testing.T) {
			calls := 0
			control := workerplatform.NewController()
			run.tick(t.Context(), control, func() error { calls++; return failure }, func() error { calls++; return failure })
			if calls != 2 {
				t.Fatalf("live failure calls=%d", calls)
			}

			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			run.tick(cancelled, control, func() error { calls++; return failure }, func() error { calls++; return failure })
			if calls != 4 {
				t.Fatalf("cancelled failure calls=%d", calls)
			}

			control.Drain()
			run.tick(t.Context(), control, func() error { calls++; return nil }, func() error { calls++; return nil })
			if calls != 4 {
				t.Fatalf("drained control executed callbacks: %d", calls)
			}
		})
	}
}

func TestControlledWorkerObservesChildCompletion(t *testing.T) {
	runtime := New(t.Context(), bootstrapTestConfig(t), runtimeIdentityBindingStub{}, runtimeTestNotificationFactory(), runtimeTestPartyFactory(), runtimeTestDataExchangeFactory(), runtimeTestIntegrationFactory())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	started := make(chan int, 2)
	firstChildDone := make(chan struct{})
	starts := 0
	go runtime.runControlledWorker(ctx, "test", func(childCtx context.Context) <-chan struct{} {
		starts++
		started <- starts
		if starts == 1 {
			return firstChildDone
		}
		childDone := make(chan struct{})
		go func() {
			<-childCtx.Done()
			close(childDone)
		}()
		return childDone
	}, done)
	if start := <-started; start != 1 {
		t.Fatalf("first child start=%d", start)
	}
	close(firstChildDone)
	select {
	case start := <-started:
		if start != 2 {
			t.Fatalf("second child start=%d", start)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("controlled worker did not reconcile completed child")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("controlled worker did not stop")
	}
	if err := runtime.store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestControlledWorkerWaitCoversCompletionAndTimeout(t *testing.T) {
	completed := make(chan struct{})
	close(completed)
	waitForControlledWorker(completed, time.Second)
	waitForControlledWorker(make(chan struct{}), time.Millisecond)
}

func mustRuntimeWorkerDependencies(t *testing.T, id string) workerplatform.Dependencies {
	t.Helper()
	dependencies, err := newRuntimeWorkerDependencies(id)
	if err != nil {
		t.Fatal(err)
	}
	return dependencies
}

type advancingRuntimeClock struct {
	now   time.Time
	steps []time.Duration
}

func (c *advancingRuntimeClock) Now() time.Time {
	if len(c.steps) == 0 {
		return c.now
	}
	step := c.steps[0]
	c.steps = c.steps[1:]
	c.now = c.now.Add(step)
	return c.now
}
