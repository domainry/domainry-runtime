package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type integrationWorkerBatchRepository struct {
	integrationrepository.IntegrationWorkerRepository
	events       []integrationmodel.IntegrationEvent
	messages     []integrationmodel.IntegrationOutboxMessage
	eventErr     error
	outboxErr    error
	eventEdge    *integrationEventWorkerEdgeRepository
	outboxEdge   *integrationOutboxWorkerEdgeRepository
	eventListed  chan struct{}
	outboxListed chan struct{}
}

func TestIntegrationOutboxBatchOutcomeBuckets(t *testing.T) {
	message := integrationOutboxWorkerMessage()
	principal := integrationManagementPrincipal(PermissionRetry)
	for _, test := range []struct {
		name   string
		result OutboxSendResult
		err    error
		bucket string
	}{
		{name: "sent", result: OutboxSendResult{Status: "sent"}, bucket: "sent"},
		{name: "retried", err: errors.New("temporary"), bucket: "retried"},
		{name: "dead letter", err: integrationpolicy.NewProviderError(integrationpolicy.ErrorProviderRejected, "provider.rejected", nil), bucket: "dead_lettered"},
		{name: "reconciliation", result: OutboxSendResult{ResponseRef: "receipt"}, err: errors.New("provider timeout"), bucket: "reconciliation_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			edge := &integrationOutboxWorkerEdgeRepository{claim: message, claimOK: true}
			repository := &integrationWorkerBatchRepository{outboxEdge: edge, eventEdge: &integrationEventWorkerEdgeRepository{}, messages: []integrationmodel.IntegrationOutboxMessage{message}}
			registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
			registry.RegisterOutboxSender("connector", integrationOutboxSenderFunc(func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
				return test.result, test.err
			}))
			service := NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: registry})
			result, err := service.ProcessDueIntegrationOutbox(t.Context(), 1, principal)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]int{"sent": result.Sent, "retried": result.Retried, "dead_lettered": result.DeadLettered, "reconciliation_required": result.ReconciliationRequired}[test.bucket]
			if got != 1 {
				t.Fatalf("bucket=%q result=%#v", test.bucket, result)
			}
		})
	}

	edge := &integrationOutboxWorkerEdgeRepository{claim: message, claimOK: true}
	repository := &integrationWorkerBatchRepository{outboxEdge: edge, eventEdge: &integrationEventWorkerEdgeRepository{}, messages: []integrationmodel.IntegrationOutboxMessage{{ID: "failed", WorkspaceID: "workspace", ConnectorKey: "missing", Status: "failed"}}}
	service := NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})})
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ProcessDueIntegrationOutbox(cancelled, 1, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled outbox batch error=%v", err)
	}
}

func (r *integrationWorkerBatchRepository) ListDueEvents(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationEvent, error) {
	if r.eventListed != nil {
		select {
		case r.eventListed <- struct{}{}:
		default:
		}
	}
	return r.events, r.eventErr
}
func (r *integrationWorkerBatchRepository) ListDueOutbox(context.Context, principalmodel.SystemScope, int, string) ([]integrationmodel.IntegrationOutboxMessage, error) {
	if r.outboxListed != nil {
		select {
		case r.outboxListed <- struct{}{}:
		default:
		}
	}
	return r.messages, r.outboxErr
}
func (r *integrationWorkerBatchRepository) ClaimEvent(ctx context.Context, workspaceID, id, owner, at string) (integrationmodel.IntegrationEvent, bool, error) {
	return r.eventEdge.ClaimEvent(ctx, workspaceID, id, owner, at)
}
func (r *integrationWorkerBatchRepository) HeartbeatEvent(ctx context.Context, workspaceID, id, owner string, token int64, at string) (integrationmodel.IntegrationEvent, error) {
	return r.eventEdge.HeartbeatEvent(ctx, workspaceID, id, owner, token, at)
}
func (r *integrationWorkerBatchRepository) UpdateEventStatus(ctx context.Context, workspaceID, id, owner string, token int64, status, errorText, at string) (integrationmodel.IntegrationEvent, error) {
	return r.eventEdge.UpdateEventStatus(ctx, workspaceID, id, owner, token, status, errorText, at)
}
func (r *integrationWorkerBatchRepository) ScheduleEventRetry(ctx context.Context, workspaceID, id, owner string, token int64, delay int, errorText, at string) (integrationmodel.IntegrationEvent, error) {
	return r.eventEdge.ScheduleEventRetry(ctx, workspaceID, id, owner, token, delay, errorText, at)
}
func (r *integrationWorkerBatchRepository) ClaimOutbox(ctx context.Context, workspaceID, id, owner, at string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	return r.outboxEdge.ClaimOutbox(ctx, workspaceID, id, owner, at)
}
func (r *integrationWorkerBatchRepository) HeartbeatOutbox(ctx context.Context, workspaceID, id, owner string, token int64, at string) (integrationmodel.IntegrationOutboxMessage, error) {
	return r.outboxEdge.HeartbeatOutbox(ctx, workspaceID, id, owner, token, at)
}
func (r *integrationWorkerBatchRepository) UpdateOutboxStatus(ctx context.Context, workspaceID, id, owner string, token int64, status, responseRef, errorText, ackDeadlineAt, at string) (integrationmodel.IntegrationOutboxMessage, error) {
	return r.outboxEdge.UpdateOutboxStatus(ctx, workspaceID, id, owner, token, status, responseRef, errorText, ackDeadlineAt, at)
}
func (r *integrationWorkerBatchRepository) ScheduleOutboxRetry(ctx context.Context, workspaceID, id, owner string, token int64, delay int, errorText, at string) (integrationmodel.IntegrationOutboxMessage, error) {
	return r.outboxEdge.ScheduleOutboxRetry(ctx, workspaceID, id, owner, token, delay, errorText, at)
}

func TestIntegrationWorkerBatchGuardAndTickEdges(t *testing.T) {
	previousInstallationWorkspaceID := principalmodel.InstallationWorkspaceID
	if err := principalmodel.ConfigureInstallationWorkspaceID("workspace-primary"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { principalmodel.InstallationWorkspaceID = previousInstallationWorkspaceID })
	event := integrationEventWorkerEdgeEvent()
	repository := &integrationWorkerBatchRepository{eventEdge: &integrationEventWorkerEdgeRepository{claim: event, claimOK: true}, outboxEdge: &integrationOutboxWorkerEdgeRepository{}, eventListed: make(chan struct{}, 2), outboxListed: make(chan struct{}, 2)}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	registry.RegisterEventHandler("provider", integrationEventWorkerHandlerFunc(func(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
		return EventProcessDecision{}, nil
	}))
	registry.RegisterOutboxSender("registered", integrationOutboxSenderFunc(func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
		return OutboxSendResult{}, nil
	}))
	service := NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: registry})
	assertIntegrationWorkerStopped(t, NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})}).StartEventWorker(t.Context(), time.Second, 1))
	assertIntegrationWorkerStopped(t, NewIntegrationApplicationService(ApplicationDependencies{Registry: registry}).StartEventWorker(t.Context(), time.Second, 1))
	assertIntegrationWorkerStopped(t, NewIntegrationApplicationService(ApplicationDependencies{WorkerRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})}).StartOutboxWorker(t.Context(), time.Second, 1))
	assertIntegrationWorkerStopped(t, NewIntegrationApplicationService(ApplicationDependencies{Registry: registry}).StartOutboxWorker(t.Context(), time.Second, 1))
	if _, err := service.ProcessDueIntegrationEvents(t.Context(), 1, principalmodel.Principal{}); err == nil {
		t.Fatal("event authorization accepted")
	}
	if _, err := service.ProcessDueIntegrationEvents(t.Context(), 1, integrationManagementPrincipal()); err == nil {
		t.Fatal("event permission accepted")
	}
	repository.eventErr = errors.New("event list failed")
	service.processEventWorkerTick(t.Context(), 1)
	repository.eventErr = nil
	repository.events = nil
	service.processEventWorkerTick(t.Context(), 1)
	repository.events = []integrationmodel.IntegrationEvent{event}
	service.processEventWorkerTick(t.Context(), 1)
	service.registry.ReplaceSchema(integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{{Key: "mapped", Provider: "mapped", EventType: "created", TargetType: "owner_task"}}})
	mapped := event
	mapped.Provider, mapped.EventType = "mapped", "created"
	if filtered := service.filterProcessableEvents([]integrationmodel.IntegrationEvent{{Status: "failed"}, mapped, {Provider: "ignored", Status: "received"}}); len(filtered) != 2 {
		t.Fatalf("processable events=%#v", filtered)
	}
	repository.eventEdge.claim = mapped
	repository.events = []integrationmodel.IntegrationEvent{mapped}
	if result, err := service.ProcessDueIntegrationEvents(t.Context(), 1, integrationManagementPrincipal(PermissionRetry)); err != nil || result.Skipped != 1 {
		t.Fatalf("mapped skip result=%#v err=%v", result, err)
	}
	cancelled, cancelBatch := context.WithCancel(t.Context())
	cancelBatch()
	repository.events = []integrationmodel.IntegrationEvent{{ID: "cancelled", WorkspaceID: "workspace", Provider: "provider", Status: "failed"}}
	if _, err := service.ProcessDueIntegrationEvents(cancelled, 1, integrationManagementPrincipal(PermissionRetry)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled event batch error=%v", err)
	}

	if _, err := service.ProcessDueIntegrationOutbox(t.Context(), 1, principalmodel.Principal{}); err == nil {
		t.Fatal("outbox authorization accepted")
	}
	if _, err := service.ProcessDueIntegrationOutbox(t.Context(), 1, integrationManagementPrincipal()); err == nil {
		t.Fatal("outbox permission accepted")
	}
	repository.outboxErr = errors.New("outbox list failed")
	service.processOutboxWorkerTick(t.Context(), 1)
	repository.outboxErr = nil
	repository.messages = nil
	service.processOutboxWorkerTick(t.Context(), 1)
	repository.messages = []integrationmodel.IntegrationOutboxMessage{{ID: "message", WorkspaceID: "workspace", ConnectorKey: "missing", Status: "queued"}}
	service.UseQueueBackpressureThresholds(t.Context(), 1, time.Second)
	service.processOutboxWorkerTick(t.Context(), 1)
	repository.messages = nil
	if _, err := service.ProcessDueIntegrationOutbox(t.Context(), 0, integrationManagementPrincipal(PermissionRetry)); err != nil {
		t.Fatalf("default outbox limit error=%v", err)
	}
	if _, err := service.ProcessDueIntegrationOutbox(t.Context(), 201, integrationManagementPrincipal(PermissionRetry)); err != nil {
		t.Fatalf("capped outbox limit error=%v", err)
	}
	service.operationalMetrics = nil
	if _, err := service.ProcessDueIntegrationOutbox(t.Context(), 1, integrationManagementPrincipal(PermissionRetry)); err != nil {
		t.Fatalf("nil metrics outbox error=%v", err)
	}

	for len(repository.eventListed) > 0 {
		<-repository.eventListed
	}
	ctx, cancel := context.WithCancel(t.Context())
	doneEvents := service.StartEventWorker(ctx, time.Millisecond, 1)
	select {
	case <-repository.eventListed:
	case <-time.After(2 * time.Second):
		t.Fatal("event worker did not tick")
	}
	cancel()
	select {
	case <-doneEvents:
	case <-time.After(2 * time.Second):
		t.Fatal("event worker did not stop")
	}
	for len(repository.outboxListed) > 0 {
		<-repository.outboxListed
	}
	ctx, cancel = context.WithCancel(t.Context())
	doneOutbox := service.StartOutboxWorker(ctx, time.Millisecond, 1)
	select {
	case <-repository.outboxListed:
	case <-time.After(2 * time.Second):
		t.Fatal("outbox worker did not tick")
	}
	cancel()
	select {
	case <-doneOutbox:
	case <-time.After(2 * time.Second):
		t.Fatal("outbox worker did not stop")
	}
}
