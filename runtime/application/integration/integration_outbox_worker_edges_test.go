package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
	workertestkit "github.com/domainry/domainry-runtime/runtime/platform/worker/testkit"
)

type integrationOutboxWorkerEdgeRepository struct {
	integrationrepository.IntegrationWorkerRepository
	claim        integrationmodel.IntegrationOutboxMessage
	claimOK      bool
	claimErr     error
	heartbeatErr error
	updateErr    error
	scheduleErr  error
	updateCalls  int
	lastStatus   string
}

func (r *integrationOutboxWorkerEdgeRepository) ClaimOutbox(context.Context, string, string, string, string) (integrationmodel.IntegrationOutboxMessage, bool, error) {
	return r.claim, r.claimOK, r.claimErr
}
func (r *integrationOutboxWorkerEdgeRepository) HeartbeatOutbox(context.Context, string, string, string, int64, string) (integrationmodel.IntegrationOutboxMessage, error) {
	return r.claim, r.heartbeatErr
}

func TestIntegrationOutboxHeartbeatEdges(t *testing.T) {
	message := integrationOutboxWorkerMessage()
	repository := &integrationOutboxWorkerEdgeRepository{claim: message, heartbeatErr: errors.New("heartbeat failed")}
	service := integrationOutboxWorkerService(repository, nil)
	if err := service.outboxHeartbeat(message)(t.Context()); !errors.Is(err, repository.heartbeatErr) {
		t.Fatalf("heartbeat repository error=%v", err)
	}
	fault := errors.New("heartbeat fault")
	service.worker.Faults = workertestkit.NewScriptedFaultInjector(workertestkit.FaultEffect{Point: workerplatform.FaultWorkerHeartbeat, Err: fault})
	if err := service.outboxHeartbeat(message)(t.Context()); !errors.Is(err, fault) {
		t.Fatalf("heartbeat fault error=%v", err)
	}
}
func (r *integrationOutboxWorkerEdgeRepository) UpdateOutboxStatus(_ context.Context, _, _ string, _ string, _ int64, status, responseRef, errorText, ackDeadlineAt, _ string) (integrationmodel.IntegrationOutboxMessage, error) {
	r.updateCalls++
	r.lastStatus = status
	if r.updateErr != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.updateErr
	}
	saved := r.claim
	saved.Status, saved.ResponseRef, saved.Error, saved.AckDeadlineAt, saved.LeaseOwner = status, responseRef, errorText, ackDeadlineAt, ""
	return saved, nil
}
func (r *integrationOutboxWorkerEdgeRepository) ScheduleOutboxRetry(_ context.Context, _, _ string, _ string, _ int64, _ int, errorText, _ string) (integrationmodel.IntegrationOutboxMessage, error) {
	if r.scheduleErr != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.scheduleErr
	}
	saved := r.claim
	saved.Status, saved.Error, saved.NextAttemptAt, saved.LeaseOwner = "failed", errorText, "later", ""
	return saved, nil
}

type integrationOutboxSenderFunc func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error)

func (fn integrationOutboxSenderFunc) SendIntegrationOutboxMessage(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, principal principalmodel.Principal) (OutboxSendResult, error) {
	return fn(ctx, message, principal)
}

func integrationOutboxWorkerMessage() integrationmodel.IntegrationOutboxMessage {
	return integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "workspace", ConnectorKey: "connector", Operation: "send", Status: "queued", LeaseOwner: "worker", FencingToken: 1, Payload: map[string]any{}}
}

func integrationOutboxWorkerService(repository *integrationOutboxWorkerEdgeRepository, sender OutboxSender) *IntegrationApplicationService {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	if sender != nil {
		registry.RegisterOutboxSender("connector", sender)
	}
	return NewIntegrationApplicationService(ApplicationDependencies{
		WorkerRepository: repository,
		Registry:         registry,
		Worker:           workerplatform.Dependencies{Clock: integrationFixedClock{now: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)}},
	})
}

func TestProcessDueOutboxMessageClaimAndCompletionEdges(t *testing.T) {
	message := integrationOutboxWorkerMessage()
	principal := integrationRotationPrincipal(PermissionRetry)
	repository := &integrationOutboxWorkerEdgeRepository{claim: message, claimOK: true}
	service := integrationOutboxWorkerService(repository, nil)
	if processed, bucket := service.processDueOutboxMessage(t.Context(), message, principal); bucket != "skipped" || processed.Error != "backend.integration.outbox.sender_not_found" {
		t.Fatalf("missing sender processed=%#v bucket=%q", processed, bucket)
	}
	senderResult, senderErr := OutboxSendResult{}, error(nil)
	sender := integrationOutboxSenderFunc(func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
		return senderResult, senderErr
	})
	service = integrationOutboxWorkerService(repository, sender)
	repository.claimErr = errIntegrationManagementTest
	if processed, bucket := service.processDueOutboxMessage(t.Context(), message, principal); bucket != "skipped" || processed.ID != message.ID {
		t.Fatalf("claim error processed=%#v bucket=%q", processed, bucket)
	}
	repository.claimErr, repository.claimOK = nil, false
	if processed, bucket := service.processDueOutboxMessage(t.Context(), message, principal); bucket != "skipped" || processed.ID != message.ID {
		t.Fatalf("claim miss processed=%#v bucket=%q", processed, bucket)
	}
	repository.claimOK = true
	if processed, bucket := service.processDueOutboxMessage(t.Context(), message, principal); bucket != "sent" || processed.Status != "sent" {
		t.Fatalf("sent processed=%#v bucket=%q", processed, bucket)
	}
	senderResult = OutboxSendResult{Status: "sent", ResponseRef: "response-with-ack", AckTimeoutSeconds: 90}
	if processed, bucket := service.processDueOutboxMessage(t.Context(), message, principal); bucket != "sent" || processed.AckDeadlineAt != "2026-07-20T12:01:30Z" {
		t.Fatalf("ack deadline processed=%#v bucket=%q", processed, bucket)
	}
	senderResult = OutboxSendResult{Status: "delivered", AckTimeoutSeconds: 90}
	_, _ = service.processDueOutboxMessage(t.Context(), message, principal)
	senderResult = OutboxSendResult{Status: "invalid"}
	if processed, bucket := service.processDueOutboxMessage(t.Context(), message, principal); bucket != "dead_lettered" || processed.Status != "dead_letter" {
		t.Fatalf("invalid status processed=%#v bucket=%q", processed, bucket)
	}
	senderResult = OutboxSendResult{Status: "sent", ResponseRef: "response"}
	repository.updateErr = mutation.MutationConflict("outbox", message.ID, mutation.MutationConflictLeaseLost, nil)
	if processed, bucket := service.processDueOutboxMessage(t.Context(), message, principal); bucket != "skipped" || processed.ID != message.ID {
		t.Fatalf("complete lease loss processed=%#v bucket=%q", processed, bucket)
	}
	repository.updateErr = errors.New("write failed")
	if processed, bucket := service.processDueOutboxMessage(t.Context(), message, principal); bucket != "retried" || processed.ID != message.ID {
		t.Fatalf("complete write failure processed=%#v bucket=%q", processed, bucket)
	}
	repository.updateErr = nil
	ctx, cancel := context.WithCancel(t.Context())
	senderErr = context.Canceled
	cancel()
	if processed, bucket := service.processDueOutboxMessage(ctx, message, principal); bucket != "skipped" || processed.ID != message.ID {
		t.Fatalf("cancelled send processed=%#v bucket=%q", processed, bucket)
	}
}

func TestProcessDueOutboxMessageWithoutOperationalMetrics(t *testing.T) {
	message := integrationOutboxWorkerMessage()
	repository := &integrationOutboxWorkerEdgeRepository{claim: message, claimOK: true}
	sender := integrationOutboxSenderFunc(func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
		return OutboxSendResult{Status: "sent"}, nil
	})
	service := integrationOutboxWorkerService(repository, sender)
	service.operationalMetrics = nil
	if _, bucket := service.processDueOutboxMessage(t.Context(), message, integrationManagementPrincipal(PermissionRetry)); bucket != "sent" {
		t.Fatalf("sent bucket=%q", bucket)
	}
	repository.updateErr = mutation.MutationConflict("outbox", message.ID, mutation.MutationConflictLeaseLost, nil)
	if _, bucket := service.processDueOutboxMessage(t.Context(), message, integrationManagementPrincipal(PermissionRetry)); bucket != "skipped" {
		t.Fatalf("lease lost bucket=%q", bucket)
	}
}

func TestProcessDueOutboxMessageQuarantinesInvalidBusinessDeliveryProjection(t *testing.T) {
	for _, test := range []struct{ connector, operation string }{
		{"google_workspace", "gmail_send_message"},
		{"appointment_scheduling", "enqueue_booking"},
	} {
		t.Run(test.connector, func(t *testing.T) {
			message := integrationOutboxWorkerMessage()
			message.ConnectorKey, message.Operation = test.connector, test.operation
			repository := &integrationOutboxWorkerEdgeRepository{claim: message, claimOK: true}
			sender := integrationOutboxSenderFunc(func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
				return OutboxSendResult{Status: "sent", ResponseRef: "receipt", Response: map[string]any{}}, nil
			})
			service := integrationOutboxWorkerService(repository, nil)
			service.registry.(*ConnectorRegistry).RegisterOutboxSender(test.connector, sender)
			if _, bucket := service.processDueOutboxMessage(t.Context(), message, integrationRotationPrincipal(PermissionRetry)); bucket != "reconciliation_required" {
				t.Fatalf("bucket=%s", bucket)
			}
		})
	}
}

func TestFailDueOutboxMessageEdges(t *testing.T) {
	message := integrationOutboxWorkerMessage()
	principal := integrationRotationPrincipal(PermissionRetry)
	repository := &integrationOutboxWorkerEdgeRepository{claim: message, claimOK: true}
	service := integrationOutboxWorkerService(repository, integrationOutboxSenderFunc(func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
		return OutboxSendResult{}, nil
	}))

	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, errors.New("provider failed"), "receipt"); bucket != "reconciliation_required" || processed.Status != "quarantined" {
		t.Fatalf("uncertain processed=%#v bucket=%q", processed, bucket)
	}
	repository.updateErr = mutation.MutationConflict("outbox", message.ID, mutation.MutationConflictLeaseLost, nil)
	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, errors.New("provider failed"), "receipt"); bucket != "skipped" || processed.ID != message.ID {
		t.Fatalf("uncertain lease loss processed=%#v bucket=%q", processed, bucket)
	}
	repository.updateErr = errors.New("quarantine failed")
	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, errors.New("provider failed"), "receipt"); bucket != "reconciliation_required" || processed.ID != message.ID {
		t.Fatalf("uncertain write failure processed=%#v bucket=%q", processed, bucket)
	}
	repository.updateErr = nil
	terminal := integrationpolicy.NewProviderError(integrationpolicy.ErrorProviderRejected, "provider.rejected", nil)
	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, terminal); bucket != "dead_lettered" || processed.Status != "dead_letter" {
		t.Fatalf("terminal processed=%#v bucket=%q", processed, bucket)
	}
	repository.updateErr = mutation.MutationConflict("outbox", message.ID, mutation.MutationConflictLeaseLost, nil)
	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, terminal); bucket != "skipped" || processed.ID != message.ID {
		t.Fatalf("terminal lease loss processed=%#v bucket=%q", processed, bucket)
	}
	repository.updateErr = errors.New("dead letter failed")
	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, terminal); bucket != "dead_lettered" || processed.ID != message.ID {
		t.Fatalf("terminal write failure processed=%#v bucket=%q", processed, bucket)
	}
	repository.updateErr = nil
	repository.scheduleErr = mutation.MutationConflict("outbox", message.ID, mutation.MutationConflictLeaseLost, nil)
	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, errors.New("temporary")); bucket != "skipped" || processed.ID != message.ID {
		t.Fatalf("retry lease loss processed=%#v bucket=%q", processed, bucket)
	}
	repository.scheduleErr = errors.New("retry failed")
	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, errors.New("temporary")); bucket != "retried" || processed.ID != message.ID {
		t.Fatalf("retry write failure processed=%#v bucket=%q", processed, bucket)
	}
	repository.scheduleErr = nil
	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, errors.New("temporary")); bucket != "retried" || processed.NextAttemptAt == "" {
		t.Fatalf("retry processed=%#v bucket=%q", processed, bucket)
	}
	maxed := message
	maxed.AttemptCount = integrationruntime.IntegrationOutboxMaxAttempts
	if processed, bucket := service.failDueOutboxMessage(t.Context(), maxed, principal, errors.New("temporary")); bucket != "dead_lettered" || processed.Status != "dead_letter" {
		t.Fatalf("max-attempt processed=%#v bucket=%q", processed, bucket)
	}
	repository.claim.Operation = "integration.alert.dead_letter"
	alert := message
	alert.Operation = repository.claim.Operation
	if processed, bucket := service.failDueOutboxMessage(t.Context(), alert, principal, terminal); bucket != "dead_lettered" || processed.Status != "dead_letter" {
		t.Fatalf("alert dead-letter processed=%#v bucket=%q", processed, bucket)
	}
	repository.claim = message

	message.Payload = map[string]any{"notification_fallback_plan": []any{map[string]any{"payload": "invalid"}}}
	repository.scheduleErr = nil
	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, terminal); bucket != "retried" || processed.NextAttemptAt == "" {
		t.Fatalf("fallback retry processed=%#v bucket=%q", processed, bucket)
	}
	repository.scheduleErr = errors.New("fallback retry failed")
	if processed, bucket := service.failDueOutboxMessage(t.Context(), message, principal, terminal); bucket != "dead_lettered" || processed.ID != message.ID {
		t.Fatalf("fallback retry failure processed=%#v bucket=%q", processed, bucket)
	}
}

func TestProcessDueOutboxMessageFaultWindows(t *testing.T) {
	message := integrationOutboxWorkerMessage()
	principal := integrationRotationPrincipal(PermissionRetry)
	sender := integrationOutboxSenderFunc(func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
		return OutboxSendResult{Status: "sent", ResponseRef: "response"}, nil
	})
	for _, test := range []struct {
		name   string
		point  workerplatform.FaultPoint
		bucket string
	}{
		{"before provider", workerplatform.FaultProviderBeforeSend, "retried"},
		{"after provider", workerplatform.FaultProviderAfterSend, "reconciliation_required"},
		{"before complete", workerplatform.FaultWorkerBeforeComplete, "reconciliation_required"},
		{"after complete", workerplatform.FaultWorkerAfterComplete, "skipped"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &integrationOutboxWorkerEdgeRepository{claim: message, claimOK: true}
			service := integrationOutboxWorkerService(repository, sender)
			service.worker.Faults = workertestkit.NewScriptedFaultInjector(workertestkit.FaultEffect{Point: test.point, Err: errors.New("injected fault")})
			if processed, bucket := service.processDueOutboxMessage(t.Context(), message, principal); bucket != test.bucket || processed.ID != message.ID {
				t.Fatalf("processed=%#v bucket=%q want=%q", processed, bucket, test.bucket)
			}
		})
	}
}

func TestProcessDueOutboxMessageClaimFault(t *testing.T) {
	message := integrationOutboxWorkerMessage()
	repository := &integrationOutboxWorkerEdgeRepository{claim: message, claimOK: true}
	service := integrationOutboxWorkerService(repository, integrationOutboxSenderFunc(func(context.Context, integrationmodel.IntegrationOutboxMessage, principalmodel.Principal) (OutboxSendResult, error) {
		return OutboxSendResult{Status: "sent"}, nil
	}))
	service.worker.Faults = workertestkit.NewScriptedFaultInjector(workertestkit.FaultEffect{Point: workerplatform.FaultWorkerClaim, Err: errors.New("claim fault")})
	if processed, bucket := service.processDueOutboxMessage(t.Context(), message, integrationManagementPrincipal(PermissionRetry)); bucket != "skipped" || processed.ID != message.ID {
		t.Fatalf("claim fault processed=%#v bucket=%q", processed, bucket)
	}
}
