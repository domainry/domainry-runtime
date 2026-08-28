package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type integrationEventWorkerRepository struct {
	integrationrepository.IntegrationWorkerRepository
	event   integrationmodel.IntegrationEvent
	listErr error
}

func (repository *integrationEventWorkerRepository) ListDueEvents(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationEvent, error) {
	if repository.listErr != nil {
		return nil, repository.listErr
	}
	if repository.event.ID == "" || repository.event.Status == "processed" || repository.event.Status == "dead_letter" {
		return nil, nil
	}
	return []integrationmodel.IntegrationEvent{repository.event}, nil
}

func (repository *integrationEventWorkerRepository) ClaimEvent(context.Context, string, string, string, string) (integrationmodel.IntegrationEvent, bool, error) {
	if repository.event.LeaseOwner != "" {
		return repository.event, false, nil
	}
	repository.event.LeaseOwner = "worker-a"
	repository.event.FencingToken++
	repository.event.AttemptCount++
	return repository.event, true, nil
}

func (repository *integrationEventWorkerRepository) HeartbeatEvent(context.Context, string, string, string, int64, string) (integrationmodel.IntegrationEvent, error) {
	return repository.event, nil
}

func (repository *integrationEventWorkerRepository) UpdateEventStatus(_ context.Context, _, _, _ string, _ int64, status, errorText, _ string) (integrationmodel.IntegrationEvent, error) {
	repository.event.Status = status
	repository.event.Error = errorText
	repository.event.LeaseOwner = ""
	return repository.event, nil
}

func (repository *integrationEventWorkerRepository) ScheduleEventRetry(_ context.Context, _, _, _ string, _ int64, _ int, errorText, _ string) (integrationmodel.IntegrationEvent, error) {
	repository.event.Status = "failed"
	repository.event.Error = errorText
	repository.event.NextRetryAt = "2026-07-19T12:01:00Z"
	repository.event.LeaseOwner = ""
	return repository.event, nil
}

func (repository *integrationEventWorkerRepository) ListDueOutbox(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationOutboxMessage, error) {
	return nil, repository.listErr
}

type integrationEventWorkerHandler struct {
	decision EventProcessDecision
	err      error
}

func (handler integrationEventWorkerHandler) ProcessIntegrationEvent(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
	return handler.decision, handler.err
}

type integrationWorkerNoopSender struct{}

func (integrationWorkerNoopSender) SendIntegrationOutboxMessage(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
	return OutboxSendResult{Status: "sent"}, nil
}

func TestProcessDueIntegrationEventsSuccessRetryAndDeadLetter(t *testing.T) {
	principal := integrationruntime.IntegrationWorkerPrincipal("workspace-a")
	newService := func(handler integrationEventWorkerHandler, attempt int) (*IntegrationApplicationService, *integrationEventWorkerRepository) {
		repository := &integrationEventWorkerRepository{event: integrationmodel.IntegrationEvent{
			ID: "event-1", WorkspaceID: "workspace-a", Provider: "probe", EventType: "updated", ExternalID: "external-1", Status: "received", AttemptCount: attempt,
		}}
		service := NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})})
		service.RegisterIntegrationEventHandler("probe", handler)
		return service, repository
	}

	service, repository := newService(integrationEventWorkerHandler{decision: EventProcessDecision{Status: "processed"}}, 0)
	result, err := service.ProcessDueIntegrationEvents(t.Context(), 999, principal)
	if err != nil || result.Processed != 1 || repository.event.Status != "processed" {
		t.Fatalf("success result=%+v event=%+v error=%v", result, repository.event, err)
	}

	service, repository = newService(integrationEventWorkerHandler{err: errors.New("temporary provider error")}, 0)
	result, err = service.ProcessDueIntegrationEvents(t.Context(), 0, principal)
	if err != nil || result.Retried != 1 || repository.event.Status != "failed" || repository.event.NextRetryAt == "" {
		t.Fatalf("retry result=%+v event=%+v error=%v", result, repository.event, err)
	}

	terminal := integrationpolicy.NewProviderError(integrationpolicy.ErrorProviderRejected, "provider.rejected", nil)
	service, repository = newService(integrationEventWorkerHandler{err: terminal}, integrationruntime.IntegrationEventMaxAttempts-1)
	result, err = service.ProcessDueIntegrationEvents(t.Context(), 1, principal)
	if err != nil || result.DeadLettered != 1 || repository.event.Status != "dead_letter" {
		t.Fatalf("dead letter result=%+v event=%+v error=%v", result, repository.event, err)
	}

	repository.listErr = errors.New("worker unavailable")
	if _, err := service.ProcessDueIntegrationEvents(t.Context(), 1, principal); !errors.Is(err, repository.listErr) {
		t.Fatalf("list error=%v", err)
	}
}

func TestIntegrationEventFilteringPriorityAndWorkerLifecycle(t *testing.T) {
	repository := &integrationEventWorkerRepository{}
	service := NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})})
	service.RegisterIntegrationEventHandler("probe", integrationEventWorkerHandler{})
	events := []integrationmodel.IntegrationEvent{
		{ID: "handled", Provider: "probe", Status: "received"},
		{ID: "unhandled", Provider: "unknown", Status: "received"},
		{ID: "failed", Provider: "unknown", Status: "failed"},
	}
	filtered := service.filterProcessableEvents(events)
	if len(filtered) != 2 || filtered[0].ID != "handled" || filtered[1].ID != "failed" || service.filterProcessableEvents(nil) != nil {
		t.Fatalf("filtered=%+v", filtered)
	}
	for _, test := range []struct {
		event integrationmodel.IntegrationEvent
		want  string
	}{
		{integrationmodel.IntegrationEvent{Status: "failed"}, "retry"},
		{integrationmodel.IntegrationEvent{Error: "backend.integration.event.manual_replay_queued"}, "manual_replay"},
		{integrationmodel.IntegrationEvent{}, "new"},
	} {
		if got := integrationEventPriorityClass(test.event); got != test.want {
			t.Fatalf("priority=%q want=%q", got, test.want)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := service.StartEventWorker(ctx, 0, 0)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event worker did not stop")
	}
	service.RegisterIntegrationOutboxSender("probe", integrationWorkerNoopSender{})
	ctx, cancel = context.WithCancel(t.Context())
	done = service.StartOutboxWorker(ctx, 0, 0)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("outbox worker did not stop")
	}

	repository.listErr = errors.New("tick failure")
	service.processEventWorkerTick(t.Context(), 1)
	service.processOutboxWorkerTick(t.Context(), 1)
	stopped := NewIntegrationApplicationService(ApplicationDependencies{})
	select {
	case <-stopped.StartEventWorker(t.Context(), time.Second, 1):
	default:
		t.Fatal("unconfigured event worker was not stopped")
	}
	select {
	case <-stopped.StartOutboxWorker(t.Context(), time.Second, 1):
	default:
		t.Fatal("unconfigured outbox worker was not stopped")
	}
}
