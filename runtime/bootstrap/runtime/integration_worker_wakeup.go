package runtime

import (
	"net/http"

	"github.com/domainry/domainry-foundation/modulehttp"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
)

// These coalesced, process-local hints never own work. Integration still owns
// durable rows, due times, leases and authorization; adaptive polling recovers
// dropped hints, restarts and work committed by another process. Both the HTTP
// adapter and optional post-commit notifier signal these queues; neither hint
// carries an invented durable task identity or invokes exact-task execution.
type integrationQueueWakeups struct {
	provider chan struct{}
	event    chan struct{}
}

func newIntegrationQueueWakeups() *integrationQueueWakeups {
	return &integrationQueueWakeups{provider: make(chan struct{}, 1), event: make(chan struct{}, 1)}
}

func (a *Runtime) integrationQueueWakeup(name string) chan struct{} {
	if name != "integration_provider" && name != "integration_event" {
		return nil
	}
	a.integrationWakeupsOnce.Do(func() {
		if a.integrationWakeups == nil {
			a.integrationWakeups = newIntegrationQueueWakeups()
		}
	})
	if name == "integration_provider" {
		return a.integrationWakeups.provider
	}
	return a.integrationWakeups.event
}

func (a *Runtime) signalIntegrationQueue(name string) {
	select {
	case a.integrationQueueWakeup(name) <- struct{}{}:
	default:
	}
}

// Keep the source-owned HTTP declarations/handler and host governance intact.
// Native ingress acknowledges success only after its durable transaction has
// committed. Wake after the handler returns, never on entry or rejected input.
// Challenge/duplicate successes may cause a harmless empty recovery scan.
type integrationWakeupHTTPAdapter struct {
	modulehttp.Adapter
	runtime *Runtime
}

func (a integrationWakeupHTTPAdapter) Handler() http.Handler {
	next := a.Adapter.Handler()
	mux := http.NewServeMux()
	for _, route := range a.Routes() {
		key := route.Action.Key
		if key != integrationsdk.ActionIntegrationWebhooksIngest && key != integrationsdk.ActionIntegrationRoutedWebhooksIngest {
			continue
		}
		mux.Handle(route.Pattern(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := &integrationWakeupResponse{ResponseWriter: w}
			next.ServeHTTP(response, r)
			if response.status >= 200 && response.status < 300 {
				if key == integrationsdk.ActionIntegrationRoutedWebhooksIngest {
					a.runtime.signalIntegrationQueue("integration_provider")
				}
				a.runtime.signalIntegrationQueue("integration_event")
			}
		}))
	}
	mux.Handle("/", next)
	return mux
}

type integrationWakeupResponse struct {
	http.ResponseWriter
	status int
}

func (w *integrationWakeupResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *integrationWakeupResponse) WriteHeader(status int) {
	if status >= 200 && w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *integrationWakeupResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
