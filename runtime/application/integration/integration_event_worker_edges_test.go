package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
	workertestkit "github.com/domainry/domainry-runtime/runtime/platform/worker/testkit"
)

type integrationEventWorkerEdgeRepository struct {
	integrationrepository.IntegrationWorkerRepository
	claim        integrationmodel.IntegrationEvent
	claimOK      bool
	claimErr     error
	heartbeatErr error
	updateErr    error
	scheduleErr  error
	updateCalls  int
	lastStatus   string
	lastError    string
}

func (r *integrationEventWorkerEdgeRepository) ClaimEvent(context.Context, string, string, string, string) (integrationmodel.IntegrationEvent, bool, error) {
	return r.claim, r.claimOK, r.claimErr
}

func (r *integrationEventWorkerEdgeRepository) HeartbeatEvent(context.Context, string, string, string, int64, string) (integrationmodel.IntegrationEvent, error) {
	return r.claim, r.heartbeatErr
}

func TestIntegrationEventHeartbeatEdges(t *testing.T) {
	event := integrationEventWorkerEdgeEvent()
	audits := []string{}
	repository := &integrationEventWorkerEdgeRepository{claim: event, heartbeatErr: errors.New("heartbeat failed")}
	service := integrationEventWorkerEdgeService(repository, nil, nil, &audits)
	if err := service.eventHeartbeat(event)(t.Context()); !errors.Is(err, repository.heartbeatErr) {
		t.Fatalf("heartbeat repository error=%v", err)
	}
	fault := errors.New("heartbeat fault")
	service.worker.Faults = workertestkit.NewScriptedFaultInjector(workertestkit.FaultEffect{Point: workerplatform.FaultWorkerHeartbeat, Err: fault})
	if err := service.eventHeartbeat(event)(t.Context()); !errors.Is(err, fault) {
		t.Fatalf("heartbeat fault error=%v", err)
	}
	if got := mergeIntegrationHeartbeatError(nil, fault); !errors.Is(got, fault) {
		t.Fatalf("merged heartbeat error=%v", got)
	}
	current := errors.New("current")
	if got := mergeIntegrationHeartbeatError(current, fault); !errors.Is(got, current) {
		t.Fatalf("current error replaced=%v", got)
	}
}

func (r *integrationEventWorkerEdgeRepository) UpdateEventStatus(_ context.Context, _, _ string, _ string, _ int64, status, errorText, _ string) (integrationmodel.IntegrationEvent, error) {
	r.updateCalls++
	r.lastStatus, r.lastError = status, errorText
	if r.updateErr != nil {
		return integrationmodel.IntegrationEvent{}, r.updateErr
	}
	saved := r.claim
	saved.Status, saved.Error, saved.LeaseOwner = status, errorText, ""
	return saved, nil
}

func (r *integrationEventWorkerEdgeRepository) ScheduleEventRetry(_ context.Context, _, _ string, _ string, _ int64, _ int, errorText, _ string) (integrationmodel.IntegrationEvent, error) {
	if r.scheduleErr != nil {
		return integrationmodel.IntegrationEvent{}, r.scheduleErr
	}
	saved := r.claim
	saved.Status, saved.Error, saved.NextRetryAt, saved.LeaseOwner = "failed", errorText, "later", ""
	return saved, nil
}

type integrationEventWorkerHandlerFunc func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error)

func (fn integrationEventWorkerHandlerFunc) ProcessIntegrationEvent(ctx context.Context, event integrationmodel.IntegrationEvent, principal principalmodel.Principal) (EventProcessDecision, error) {
	return fn(ctx, event, principal)
}

func integrationEventWorkerEdgeEvent() integrationmodel.IntegrationEvent {
	return integrationmodel.IntegrationEvent{ID: "event", WorkspaceID: "workspace", Provider: "provider", EventType: "updated", Status: "received", LeaseOwner: "worker", FencingToken: 1}
}

func integrationEventWorkerEdgeService(repository *integrationEventWorkerEdgeRepository, handler EventHandler, faults workerplatform.FaultInjector, audits *[]string) *IntegrationApplicationService {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	if handler != nil {
		registry.RegisterEventHandler("provider", handler)
	}
	service := NewIntegrationApplicationService(ApplicationDependencies{
		WorkerRepository: repository,
		Registry:         registry,
		Worker: workerplatform.Dependencies{
			Clock:  integrationFixedClock{now: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)},
			Faults: faults,
		},
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, _ map[string]any) {
			*audits = append(*audits, event)
		},
	})
	return service
}

func TestProcessDueEventClaimAndMissingHandlerEdges(t *testing.T) {
	event := integrationEventWorkerEdgeEvent()
	principal := integrationRotationPrincipal(PermissionRetry)
	audits := []string{}
	repository := &integrationEventWorkerEdgeRepository{claim: event, claimOK: true, claimErr: errors.New("claim failed")}
	service := integrationEventWorkerEdgeService(repository, nil, nil, &audits)
	service.worker.Faults = workertestkit.NewScriptedFaultInjector(workertestkit.FaultEffect{Point: workerplatform.FaultWorkerClaim, Err: errors.New("claim fault")})
	if _, bucket := service.processDueEvent(t.Context(), event, principal); bucket != "skipped" {
		t.Fatalf("claim fault bucket=%q", bucket)
	}
	service.worker.Faults = nil
	if processed, bucket := service.processDueEvent(t.Context(), event, principal); bucket != "skipped" || processed.ID != event.ID || audits[len(audits)-1] != "integration_event_worker_skipped" {
		t.Fatalf("claim failure processed=%#v bucket=%q audits=%#v", processed, bucket, audits)
	}

	repository.claimErr, repository.claimOK = nil, false
	if processed, bucket := service.processDueEvent(t.Context(), event, principal); bucket != "skipped" || processed.ID != event.ID {
		t.Fatalf("claim miss processed=%#v bucket=%q", processed, bucket)
	}
	repository.claimOK = true
	if processed, bucket := service.processDueEvent(t.Context(), event, principal); bucket != "skipped" || processed.Status != "failed" || repository.lastError != "backend.integration.event.handler_not_found" {
		t.Fatalf("handler miss processed=%#v bucket=%q error=%q", processed, bucket, repository.lastError)
	}
	repository.updateErr = errors.New("status write failed")
	if processed, bucket := service.processDueEvent(t.Context(), event, principal); bucket != "skipped" || processed.Status != event.Status {
		t.Fatalf("handler status failure processed=%#v bucket=%q", processed, bucket)
	}
}

func TestProcessIntegrationEventTargetsExactCommittedEvent(t *testing.T) {
	event := integrationEventWorkerEdgeEvent()
	audits := []string{}
	repository := &integrationEventWorkerEdgeRepository{claim: event, claimOK: true}
	handler := integrationEventWorkerHandlerFunc(func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
		return EventProcessDecision{Status: "processed"}, nil
	})
	service := integrationEventWorkerEdgeService(repository, handler, nil, &audits)
	service.eventRepo = &integrationManagementEventRepo{event: event, found: true}
	result, err := service.ProcessIntegrationEvent(t.Context(), IntegrationEventLocator{WorkspaceID: event.WorkspaceID, EventID: event.ID})
	if err != nil || result.Processed != 1 || repository.lastStatus != "processed" {
		t.Fatalf("result=%#v status=%q err=%v", result, repository.lastStatus, err)
	}
}

func TestProcessDueEventCompletionFailureWindows(t *testing.T) {
	event := integrationEventWorkerEdgeEvent()
	principal := integrationRotationPrincipal(PermissionRetry)

	t.Run("cancelled handler", func(t *testing.T) {
		audits := []string{}
		repository := &integrationEventWorkerEdgeRepository{claim: event, claimOK: true}
		ctx, cancel := context.WithCancel(t.Context())
		handler := integrationEventWorkerHandlerFunc(func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
			cancel()
			return EventProcessDecision{}, context.Canceled
		})
		service := integrationEventWorkerEdgeService(repository, handler, nil, &audits)
		if processed, bucket := service.processDueEvent(ctx, event, principal); bucket != "skipped" || processed.ID != event.ID || repository.updateCalls != 0 {
			t.Fatalf("processed=%#v bucket=%q updateCalls=%d", processed, bucket, repository.updateCalls)
		}
	})

	t.Run("invalid decision", func(t *testing.T) {
		audits := []string{}
		repository := &integrationEventWorkerEdgeRepository{claim: event, claimOK: true}
		handler := integrationEventWorkerHandlerFunc(func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
			return EventProcessDecision{Status: "invalid"}, nil
		})
		service := integrationEventWorkerEdgeService(repository, handler, nil, &audits)
		if processed, bucket := service.processDueEvent(t.Context(), event, principal); bucket != "dead_lettered" || processed.Status != "dead_letter" {
			t.Fatalf("processed=%#v bucket=%q", processed, bucket)
		}
	})

	t.Run("before complete fault", func(t *testing.T) {
		audits := []string{}
		repository := &integrationEventWorkerEdgeRepository{claim: event, claimOK: true}
		faults := workertestkit.NewScriptedFaultInjector(workertestkit.StaleOwner())
		handler := integrationEventWorkerHandlerFunc(func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
			return EventProcessDecision{Status: "processed"}, nil
		})
		service := integrationEventWorkerEdgeService(repository, handler, faults, &audits)
		if processed, bucket := service.processDueEvent(t.Context(), event, principal); bucket != "retried" || processed.Status != "failed" {
			t.Fatalf("processed=%#v bucket=%q", processed, bucket)
		}
	})

	t.Run("lease lost on complete", func(t *testing.T) {
		audits := []string{}
		repository := &integrationEventWorkerEdgeRepository{claim: event, claimOK: true, updateErr: mutation.MutationConflict("event", event.ID, mutation.MutationConflictLeaseLost, nil)}
		handler := integrationEventWorkerHandlerFunc(func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
			return EventProcessDecision{}, nil
		})
		service := integrationEventWorkerEdgeService(repository, handler, nil, &audits)
		if processed, bucket := service.processDueEvent(t.Context(), event, principal); bucket != "skipped" || processed.ID != event.ID {
			t.Fatalf("processed=%#v bucket=%q", processed, bucket)
		}
	})

	t.Run("storage failure on complete", func(t *testing.T) {
		audits := []string{}
		repository := &integrationEventWorkerEdgeRepository{claim: event, claimOK: true, updateErr: errors.New("write failed")}
		handler := integrationEventWorkerHandlerFunc(func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
			return EventProcessDecision{}, nil
		})
		service := integrationEventWorkerEdgeService(repository, handler, nil, &audits)
		if processed, bucket := service.processDueEvent(t.Context(), event, principal); bucket != "retried" || processed.ID != event.ID {
			t.Fatalf("processed=%#v bucket=%q", processed, bucket)
		}
	})

	t.Run("after complete fault", func(t *testing.T) {
		audits := []string{}
		repository := &integrationEventWorkerEdgeRepository{claim: event, claimOK: true}
		faults := workertestkit.NewScriptedFaultInjector(workertestkit.FaultEffect{Point: workerplatform.FaultWorkerAfterComplete, Err: errors.New("crash after complete")})
		handler := integrationEventWorkerHandlerFunc(func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
			return EventProcessDecision{}, nil
		})
		service := integrationEventWorkerEdgeService(repository, handler, faults, &audits)
		if processed, bucket := service.processDueEvent(t.Context(), event, principal); bucket != "skipped" || processed.Status != "processed" {
			t.Fatalf("processed=%#v bucket=%q", processed, bucket)
		}
	})
}

func TestProcessDueEventExecutionEvidenceEdges(t *testing.T) {
	base := integrationEventWorkerEdgeEvent()
	base.Payload = map[string]any{EventContextKey: map[string]any{"connection_key": "connection", "connector_key": "connector", "provider_key": "provider"}}
	config := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{"connection": {Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active"}}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	for _, test := range []struct {
		name        string
		handlerErr  error
		evidenceErr error
		bucket      string
	}{
		{name: "handler failure", handlerErr: errors.New("provider failed"), bucket: "retried"},
		{name: "evidence failure", evidenceErr: errors.New("evidence failed"), bucket: "retried"},
		{name: "success", bucket: "processed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			workerRepo := &integrationEventWorkerEdgeRepository{claim: base, claimOK: true}
			delivery := &independentDeliveryRepository{insertInvocationErr: test.evidenceErr}
			registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
			registry.RegisterEventHandler("provider", integrationEventWorkerHandlerFunc(func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
				return EventProcessDecision{}, test.handlerErr
			}))
			service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: delivery, WorkerRepository: workerRepo, Registry: registry})
			if processed, bucket := service.processDueEvent(t.Context(), base, integrationManagementPrincipal(PermissionRetry)); bucket != test.bucket || processed.ID != base.ID {
				t.Fatalf("processed=%#v bucket=%q", processed, bucket)
			}
		})
	}
}

func TestProcessDueEventMappingExecutorConditionEdges(t *testing.T) {
	event := integrationEventWorkerEdgeEvent()
	event.Provider = "mapped"
	for _, test := range []struct {
		name    string
		handled bool
		err     error
	}{
		{name: "handled", handled: true},
		{name: "mapping error", err: errors.New("mapping failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &integrationEventWorkerEdgeRepository{claim: event, claimOK: true}
			service := NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{}), EventMappingExecutor: func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, bool, error) {
				return EventProcessDecision{}, test.handled, test.err
			}})
			_, bucket := service.processDueEvent(t.Context(), event, integrationManagementPrincipal(PermissionRetry))
			if test.handled && bucket != "processed" || test.err != nil && bucket != "retried" {
				t.Fatalf("bucket=%q handled=%t err=%v", bucket, test.handled, test.err)
			}
		})
	}
}

func TestFailDueEventLeaseLossAndStorageFailure(t *testing.T) {
	event := integrationEventWorkerEdgeEvent()
	principal := integrationRotationPrincipal(PermissionRetry)
	audits := []string{}
	repository := &integrationEventWorkerEdgeRepository{claim: event, claimOK: true, scheduleErr: mutation.MutationConflict("event", event.ID, mutation.MutationConflictLeaseLost, nil)}
	service := integrationEventWorkerEdgeService(repository, nil, nil, &audits)
	if processed, bucket := service.failDueEvent(t.Context(), event, principal, errors.New("temporary")); bucket != "skipped" || processed.ID != event.ID {
		t.Fatalf("retry lease loss processed=%#v bucket=%q", processed, bucket)
	}
	repository.scheduleErr = errors.New("schedule failed")
	if processed, bucket := service.failDueEvent(t.Context(), event, principal, errors.New("temporary")); bucket != "retried" || processed.ID != event.ID {
		t.Fatalf("retry storage failure processed=%#v bucket=%q", processed, bucket)
	}
	event.AttemptCount = 100
	repository.updateErr = mutation.MutationConflict("event", event.ID, mutation.MutationConflictLeaseLost, nil)
	if processed, bucket := service.failDueEvent(t.Context(), event, principal, errors.New("terminal")); bucket != "skipped" || processed.ID != event.ID {
		t.Fatalf("dead-letter lease loss processed=%#v bucket=%q", processed, bucket)
	}
	repository.updateErr = errors.New("dead-letter write failed")
	if processed, bucket := service.failDueEvent(t.Context(), event, principal, errors.New("terminal")); bucket != "dead_lettered" || processed.ID != event.ID {
		t.Fatalf("dead-letter storage failure processed=%#v bucket=%q", processed, bucket)
	}
}
