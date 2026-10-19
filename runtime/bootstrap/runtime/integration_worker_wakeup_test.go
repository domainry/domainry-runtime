package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/modulehttp"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type wakeupTestAdapter struct{ handler http.Handler }

type wakeupTestWorkers struct{ integrationsdk.LocalWorkers }

func (wakeupTestAdapter) ContractVersion() string { return modulehttp.ContractVersion }
func (wakeupTestAdapter) Owner() string           { return "integration" }
func (wakeupTestAdapter) Name() string            { return "integration_product" }
func (a wakeupTestAdapter) Handler() http.Handler { return a.handler }
func (wakeupTestAdapter) Routes() []modulehttp.Route {
	var result []modulehttp.Route
	for _, route := range integrationsdk.IntegrationHTTPAdapterContract().Routes {
		result = append(result, modulehttp.Route{Action: route.Action})
	}
	return result
}

func wakeupTestRequest(t *testing.T, key string) *http.Request {
	t.Helper()
	for _, route := range integrationsdk.IntegrationHTTPAdapterContract().Routes {
		if route.Action.Key == key {
			path := strings.NewReplacer("{workspaceID}", "workspace-a", "{connectorKey}", "connector-a", "{connectionKey}", "connection-a").Replace(route.Action.HTTP.RouteTemplate)
			return httptest.NewRequest(route.Action.HTTP.Method, path, strings.NewReader(`{}`))
		}
	}
	t.Fatalf("missing source-owned action %s", key)
	return nil
}

func TestIntegrationIngressWakeupPreservesOwnerContractAndResponse(t *testing.T) {
	for _, key := range []string{integrationsdk.ActionIntegrationWebhooksIngest, integrationsdk.ActionIntegrationRoutedWebhooksIngest, integrationsdk.ActionIntegrationCatalogRead} {
		for _, status := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusInternalServerError} {
			t.Run(key+http.StatusText(status), func(t *testing.T) {
				runtime := &Runtime{}
				provider, event := runtime.integrationQueueWakeup("integration_provider"), runtime.integrationQueueWakeup("integration_event")
				original := wakeupTestAdapter{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if len(provider) != 0 || len(event) != 0 {
						t.Error("woke before owner handler/transaction completed")
					}
					if key == integrationsdk.ActionIntegrationRoutedWebhooksIngest && r.PathValue("workspaceID") != "workspace-a" {
						t.Errorf("owner path values lost: %s", r.PathValue("workspaceID"))
					}
					w.Header().Set("X-Owner", "preserved")
					w.WriteHeader(status)
					_, _ = w.Write([]byte("source-owned response"))
				})}
				wrapped := integrationWakeupHTTPAdapter{Adapter: original, runtime: runtime}
				if !reflect.DeepEqual(wrapped.Routes(), original.Routes()) || wrapped.Owner() != original.Owner() || wrapped.ContractVersion() != original.ContractVersion() {
					t.Fatal("source-owned metadata changed")
				}
				response := httptest.NewRecorder()
				wrapped.Handler().ServeHTTP(response, wakeupTestRequest(t, key))
				if response.Code != status || response.Body.String() != "source-owned response" || response.Header().Get("X-Owner") != "preserved" {
					t.Fatalf("owner response changed: %+v", response)
				}
				wantEvent, wantProvider := 0, 0
				if status == http.StatusOK && key != integrationsdk.ActionIntegrationCatalogRead {
					wantEvent = 1
					if key == integrationsdk.ActionIntegrationRoutedWebhooksIngest {
						wantProvider = 1
					}
				}
				if len(provider) != wantProvider || len(event) != wantEvent {
					t.Fatalf("provider=%d event=%d want=%d/%d", len(provider), len(event), wantProvider, wantEvent)
				}
			})
		}
	}
}

func TestIntegrationWakeupsCoalesceAndInitializeConcurrently(t *testing.T) {
	runtime := &Runtime{}
	var group sync.WaitGroup
	for range 100 {
		group.Go(func() {
			runtime.signalIntegrationQueue("integration_provider")
			runtime.signalIntegrationQueue("integration_event")
		})
	}
	group.Wait()
	if len(runtime.integrationQueueWakeup("integration_provider")) != 1 || len(runtime.integrationQueueWakeup("integration_event")) != 1 {
		t.Fatal("wakeups did not coalesce")
	}
	if runtime.integrationQueueWakeup("integration_credentials") != nil {
		t.Fatal("unrelated worker unexpectedly got a queue hint")
	}
}

func TestIntegrationWakeupAdapterOnlyInstalledForLocalOwner(t *testing.T) {
	for _, local := range []bool{false, true} {
		original := wakeupTestAdapter{handler: http.NotFoundHandler()}
		runtime := &Runtime{moduleBindings: newRuntimeModuleBindingInventory(runtimeHTTPOnlyAdapterBinding{adapters: []modulehttp.Adapter{original}})}
		if local {
			runtime.integrationWorkers = wakeupTestWorkers{}
		}
		adapter := runtime.ModuleHTTPAdapters()[0]
		_, wrapped := adapter.(integrationWakeupHTTPAdapter)
		if wrapped != local || !reflect.DeepEqual(adapter.Routes(), original.Routes()) {
			t.Fatalf("local=%v wrapped=%v; owner route declarations must be unchanged", local, wrapped)
		}
	}
}

func newIntegrationWakeupTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "worker.db")})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		store: store, worker: workerplatform.NewDependencies("integration-wakeup-test"),
		cfg:                     config.Config{WorkerPollInterval: time.Hour, WorkerBatchSize: 7},
		operationsControlLoader: func(context.Context) ([]operationsmodel.OperationsControl, error) { return nil, nil },
	}
	t.Cleanup(func() {
		if err := runtime.stopWorkers(time.Second); err != nil {
			t.Error(err)
		}
		_ = store.Close()
	})
	return runtime
}

func awaitIntegrationWakeup(t *testing.T, called <-chan struct{}) {
	t.Helper()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("worker waited for idle poll instead of immediate wakeup")
	}
}

func TestIntegrationWakeupInterruptsIdleAndProviderWakesCommittedEvents(t *testing.T) {
	runtime := newIntegrationWakeupTestRuntime(t)
	eventCalled, providerCalled := make(chan struct{}, 4), make(chan struct{}, 4)
	var work atomic.Bool
	runtime.startIntegrationOwnerLoop(t.Context(), "integration_event", "test event", func(context.Context, int) (int, error) {
		eventCalled <- struct{}{}
		return 0, nil
	})
	runtime.startIntegrationOwnerLoop(t.Context(), "integration_provider", "test provider", func(_ context.Context, batch int) (int, error) {
		if batch != 7 {
			t.Errorf("batch=%d want 7", batch)
		}
		providerCalled <- struct{}{}
		if work.Swap(false) {
			return 1, errors.New("other task in batch failed after committed event")
		}
		return 0, nil
	})
	awaitIntegrationWakeup(t, eventCalled)
	awaitIntegrationWakeup(t, providerCalled)
	work.Store(true)
	runtime.signalIntegrationQueue("integration_provider")
	awaitIntegrationWakeup(t, providerCalled)
	awaitIntegrationWakeup(t, eventCalled)
}

func TestIntegrationWakeupDuringRunningTickIsNotLost(t *testing.T) {
	runtime := newIntegrationWakeupTestRuntime(t)
	entered, release, called := make(chan struct{}), make(chan struct{}), make(chan struct{}, 2)
	var count atomic.Int32
	runtime.startIntegrationOwnerLoop(t.Context(), "integration_event", "test event", func(ctx context.Context, _ int) (int, error) {
		if count.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		called <- struct{}{}
		return 0, nil
	})
	awaitIntegrationWakeup(t, entered)
	runtime.signalIntegrationQueue("integration_event")
	close(release)
	awaitIntegrationWakeup(t, called)
	awaitIntegrationWakeup(t, called)
}

func TestIntegrationPushIsAdmittedWhileRecoveryBatchIsStillRunning(t *testing.T) {
	runtime := newIntegrationWakeupTestRuntime(t)
	entered, release, pushCompleted := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var count atomic.Int32
	runtime.startIntegrationOwnerLoop(t.Context(), "integration_provider", "test provider", func(ctx context.Context, _ int) (int, error) {
		if count.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		} else {
			pushCompleted <- struct{}{}
		}
		return 0, nil
	})
	awaitIntegrationWakeup(t, entered)
	runtime.signalIntegrationQueue("integration_provider")
	awaitIntegrationWakeup(t, pushCompleted)
	close(release)
}

func TestIntegrationHostCommitHintSharesTheRuntimeEventQueue(t *testing.T) {
	wakeups := newIntegrationQueueWakeups()
	host := runtimeIntegrationModuleHost{wakeups: wakeups}
	runtime := constructRuntime(runtimeConstructionInput{integrationWakeups: wakeups})
	host.EventWorkNotifier().EventsCommitted()
	awaitIntegrationWakeup(t, runtime.integrationQueueWakeup("integration_event"))
}

func TestIntegrationWakeupDoesNotBypassOwnerPause(t *testing.T) {
	for _, owner := range []string{"integration_event", "integration_provider"} {
		t.Run(owner, func(t *testing.T) {
			runtime := newIntegrationWakeupTestRuntime(t)
			var paused atomic.Bool
			paused.Store(true)
			runtime.operationsControlPollInterval = 5 * time.Millisecond
			runtime.operationsControlLoader = func(context.Context) ([]operationsmodel.OperationsControl, error) {
				if paused.Load() {
					return []operationsmodel.OperationsControl{{Kind: operationsmodel.OperationsControlWorkerPause, Owner: owner, State: operationsmodel.OperationsControlActive}}, nil
				}
				return nil, nil
			}
			called := make(chan struct{}, 4)
			runtime.startIntegrationOwnerLoop(t.Context(), owner, "test owner", func(context.Context, int) (int, error) {
				called <- struct{}{}
				return 0, nil
			})
			runtime.signalIntegrationQueue(owner)
			select {
			case <-called:
				t.Fatal("queue hint bypassed paused owner")
			case <-time.After(30 * time.Millisecond):
			}
			paused.Store(false)
			awaitIntegrationWakeup(t, called)
		})
	}
}

func TestIntegrationWakeupRetainsRecoveryWithoutHint(t *testing.T) {
	runtime := newIntegrationWakeupTestRuntime(t)
	runtime.cfg.WorkerPollInterval = 5 * time.Millisecond
	called := make(chan struct{}, 4)
	runtime.startIntegrationOwnerLoop(t.Context(), "integration_event", "test event", func(context.Context, int) (int, error) {
		called <- struct{}{}
		return 0, nil
	})
	awaitIntegrationWakeup(t, called)
	// No process-local signal: restart/cross-process work must still recover.
	awaitIntegrationWakeup(t, called)
}
