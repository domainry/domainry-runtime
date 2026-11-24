package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	appschemahttp "github.com/domainry/domainry-runtime/runtime/transport/http/appschema"
	discoveryhttp "github.com/domainry/domainry-runtime/runtime/transport/http/discovery"
	dispatchhttp "github.com/domainry/domainry-runtime/runtime/transport/http/dispatch"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
	recordhttp "github.com/domainry/domainry-runtime/runtime/transport/http/records"
)

type recoveredReleaseReadiness struct{}

func (recoveredReleaseReadiness) StorageReadiness(context.Context) error   { return nil }
func (recoveredReleaseReadiness) MigrationReadiness(context.Context) error { return nil }

func TestRuntimeReadinessRecoversWhileWorkersRemainFenced(t *testing.T) {
	runtime := newReleaseHeartbeatTestRuntime("instance", time.Now().Add(-time.Second))
	runtime.releaseCohort = deploymentapplication.NewDeploymentRuntimeReleaseCohortApplicationService(
		&recoverableReleaseHeartbeatStore{recover: func() error { return nil }},
	)
	var instanceDraining, maintenance bool
	runtime.api = runtimehttp.NewHTTPRouter(runtimehttp.HTTPRouterConfig{}, runtimehttp.HTTPRouterDependencies{
		RuntimeReadiness:        recoveredReleaseReadiness{},
		RuntimeReleaseAdmission: runtime.checkRuntimeReleaseAdmission,
		OperationsControlState: func(_ context.Context, kind, _ string) (bool, bool, error) {
			return kind == "instance_drain" && instanceDraining || kind == "maintenance" && maintenance, true, nil
		},
	})
	routes := releaseReadinessRoutes(runtime.api)
	expectReadyStatus := func(want int) {
		t.Helper()
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if response.Code != want {
			t.Fatalf("readiness = %d, want %d", response.Code, want)
		}
	}
	expectReadyStatus(http.StatusServiceUnavailable)
	if err := runtime.refreshRuntimeReleaseLease(t.Context(), 15*time.Second); err != nil {
		t.Fatalf("recover original lease: %v", err)
	}
	if err := runtime.checkRuntimeReleaseAdmission(); err != nil {
		t.Fatalf("request admission stayed closed: %v", err)
	}
	if runtime.worker.Control.Snapshot().State.AcceptsClaims() {
		t.Fatal("lease recovery reopened worker claims before checking operational controls")
	}
	expectReadyStatus(http.StatusOK)
	instanceDraining = true
	expectReadyStatus(http.StatusServiceUnavailable)
	instanceDraining = false
	expectReadyStatus(http.StatusOK)
	maintenance = true
	expectReadyStatus(http.StatusServiceUnavailable)
	maintenance = false
	runtime.api.SetDraining(true)
	expectReadyStatus(http.StatusServiceUnavailable)
}

func releaseReadinessRoutes(router *runtimehttp.HTTPRouter) http.Handler {
	authenticated := func(http.HandlerFunc) http.HandlerFunc { return http.NotFound }
	runtimehttp.UseHandlers(router, runtimehttp.HTTPRouterHandlers{
		Discovery:         &discoveryhttp.DiscoveryHandler{},
		ApplicationSchema: appschemahttp.NewApplicationSchemaHandler(appschemahttp.ApplicationSchemaDependencies{Authenticated: authenticated}),
		Records:           &recordhttp.RecordsHandler{},
		Dispatch:          &dispatchhttp.ExecutionHandler{},
		Operations:        operationshttp.NewOperationsHandler(operationshttp.OperationsDependencies{Authenticated: authenticated}),
	})
	router.MarkStartupComplete()
	return router.Routes()
}

func TestRuntimeReleaseHeartbeatKeepsTwoReplicasAdmittedAfterTemporaryDatabaseFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		now := time.Now()
		first := newReleaseHeartbeatTestRuntime("first", now.Add(45*time.Second))
		second := newReleaseHeartbeatTestRuntime("second", now.Add(45*time.Second))
		first.startRuntimeReleaseHeartbeat(ctx)
		second.startRuntimeReleaseHeartbeat(ctx)

		synctest.Wait()
		for _, runtime := range []*Runtime{first, second} {
			if err := runtime.checkRuntimeReleaseAdmission(); err != nil || !runtime.worker.Control.Snapshot().State.AcceptsClaims() {
				t.Fatalf("temporary database failure fenced a valid lease: %v", err)
			}
		}

		time.Sleep(4 * time.Second)
		synctest.Wait()
		for _, runtime := range []*Runtime{first, second} {
			if err := runtime.checkRuntimeReleaseAdmission(); err != nil || !runtime.worker.Control.Snapshot().State.AcceptsClaims() {
				t.Fatalf("runtime did not remain ready after retry: %v", err)
			}
			if expiry := runtime.runtimeReleaseLease().ExpiresAt; !expiry.After(now.Add(45 * time.Second)) {
				t.Fatalf("lease did not renew: %v", expiry)
			}
		}
	})
}

func TestRenewRuntimeReleaseLeaseImmediatelyStopsOnConfirmedLoss(t *testing.T) {
	lease := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "instance", ExpiresAt: time.Now().Add(time.Minute)}
	lost := errors.Join(deploymentmodel.ErrRuntimeReleaseLeaseLost, errors.New("generation changed"))
	attempts := 0
	next, err := renewRuntimeReleaseLease(t.Context(), lease, 15*time.Second, time.Now,
		func(context.Context, deploymentmodel.RuntimeReleaseCohortLease, time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error) {
			attempts++
			return lease, lost
		})
	if err != lost || attempts != 1 || next != lease {
		t.Fatalf("expected immediate fencing on confirmed loss, got %v after %d attempts", err, attempts)
	}
}

func TestRenewRuntimeReleaseLeaseCannotAcceptRenewalAfterConfirmedExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		lease := deploymentmodel.RuntimeReleaseCohortLease{InstanceID: "instance", ExpiresAt: now.Add(10 * time.Second)}
		attempts := 0
		next, err := renewRuntimeReleaseLease(t.Context(), lease, 15*time.Second, time.Now,
			func(ctx context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, observed time.Time) (deploymentmodel.RuntimeReleaseCohortLease, error) {
				attempts++
				<-ctx.Done()
				lease.ExpiresAt = observed.Add(45 * time.Second)
				return lease, nil
			})
		if next != lease || !errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseExpired) || attempts != 2 || time.Since(now) != 10*time.Second {
			t.Fatalf("expected the previously confirmed expiry, got %+v, %v after %d attempts", next, err, attempts)
		}

		runtime := newReleaseHeartbeatTestRuntime("instance", lease.ExpiresAt)
		if err := runtime.checkRuntimeReleaseAdmission(); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseExpired) {
			t.Fatalf("HTTP admission still open at lease expiry: %v", err)
		}
	})
}

func TestRuntimeRecoversExpiredReleaseAndKeepsChangedGenerationFenced(t *testing.T) {
	for _, changedGeneration := range []bool{false, true} {
		name := "same generation"
		if changedGeneration {
			name = "changed generation"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				expiry := time.Now().Add(2 * time.Second)
				runtime := newReleaseHeartbeatTestRuntime("instance", expiry)
				child, cancel := runtime.beginReleaseWorker(t.Context())
				t.Cleanup(cancel)
				storeDown := errors.New("database unavailable")
				var databaseReady atomic.Bool
				var attempts atomic.Int32
				requestCtx, stopHeartbeat := context.WithCancel(t.Context())
				defer stopHeartbeat()
				runtime.releaseCohort = deploymentapplication.NewDeploymentRuntimeReleaseCohortApplicationService(
					&recoverableReleaseHeartbeatStore{recover: func() error {
						attempts.Add(1)
						if !databaseReady.Load() {
							return storeDown
						}
						if changedGeneration {
							return deploymentmodel.ErrRuntimeReleaseLeaseLost
						}
						return nil
					}},
				)

				runtime.startRuntimeReleaseHeartbeat(requestCtx)
				time.Sleep(2 * time.Second)
				synctest.Wait()
				if attempts.Load() != 1 {
					t.Fatalf("expected recovery to start when the lease expired, got %d attempts", attempts.Load())
				}
				if child.Err() == nil || runtime.worker.Control.Snapshot().State.AcceptsClaims() {
					t.Fatal("lease expiry did not stop workers")
				}
				if err := runtime.checkRuntimeReleaseAdmission(); !errors.Is(err, deploymentmodel.ErrRuntimeReleaseLeaseExpired) {
					t.Fatalf("request admission at expiry = %v", err)
				}

				databaseReady.Store(true)
				time.Sleep(3 * time.Second)
				synctest.Wait()
				if attempts.Load() != 2 {
					t.Fatalf("expected recovery to retry before the next ordinary heartbeat, got %d attempts", attempts.Load())
				}
				next, stop := runtime.beginReleaseWorker(t.Context())
				if changedGeneration {
					if next != nil || stop != nil {
						t.Fatal("old generation recovered")
					}
					if admission := runtime.checkRuntimeReleaseAdmission(); admission == nil || errors.Is(admission, deploymentmodel.ErrRuntimeReleaseLeaseExpired) {
						t.Fatalf("confirmed loss must stay terminal: %v", admission)
					}
					time.Sleep(15 * time.Second)
					synctest.Wait()
					if attempts.Load() != 2 {
						t.Fatal("terminal release attempted to recover again")
					}
					return
				}
				if stop == nil || next.Err() != nil ||
					runtime.checkRuntimeReleaseAdmission() != nil || !runtime.worker.Control.Snapshot().State.AcceptsClaims() {
					t.Fatal("same generation did not recover")
				}
				stop()
			})
		})
	}
}

type recoverableReleaseHeartbeatStore struct {
	deploymentrepository.RuntimeReleaseCohortRepository
	calls   atomic.Int32
	recover func() error
}

func (s *recoverableReleaseHeartbeatStore) HeartbeatRuntimeRelease(_ context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, now time.Time, duration time.Duration) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	if s.calls.Add(1) == 1 {
		return lease, errors.New("database connection interrupted")
	}
	lease.ExpiresAt = now.Add(duration)
	return lease, nil
}

func (s *recoverableReleaseHeartbeatStore) RecoverRuntimeRelease(_ context.Context, lease deploymentmodel.RuntimeReleaseCohortLease, now time.Time, duration time.Duration) (deploymentmodel.RuntimeReleaseCohortLease, error) {
	if err := s.recover(); err != nil {
		return lease, err
	}
	lease.ExpiresAt = now.Add(duration)
	return lease, nil
}

func newReleaseHeartbeatTestRuntime(instance string, expiry time.Time) *Runtime {
	return &Runtime{
		worker:           workerplatform.NormalizeDependencies(workerplatform.Dependencies{}),
		releaseCohort:    deploymentapplication.NewDeploymentRuntimeReleaseCohortApplicationService(&recoverableReleaseHeartbeatStore{}),
		releaseLease:     deploymentmodel.RuntimeReleaseCohortLease{InstanceID: instance, ExpiresAt: expiry},
		releaseAdmission: &deploymentapplication.RuntimeReleaseAdmission{},
	}
}
