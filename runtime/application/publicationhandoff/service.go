// Package publicationhandoff owns Runtime's application boundary for durable
// outbound messages. Connector configuration and provider execution belong to
// the Integration owner behind integrationsdk.Delivery.
package publicationhandoff

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const queueKind = "runtime_publication_outbox"
const maxDeliveryAttempts = 10

type Locator struct {
	WorkspaceID string
	MessageID   string
}

type Service struct {
	repository integrationrepository.RuntimePublicationRepository
	workerRepo integrationrepository.RuntimePublicationWorkerRepository
	delivery   integrationsdk.Delivery
	worker     workerplatform.Dependencies
	wakeups    <-chan workerplatform.DurableTaskLocator
	publish    func(workerplatform.DurableTaskLocator)
	prepare    PayloadPreparer
}

type PayloadPreparer func(context.Context, integrationmodel.IntegrationOutboxMessage, map[string]any) (map[string]any, error)

type Dependencies struct {
	Repository       integrationrepository.RuntimePublicationRepository
	WorkerRepository integrationrepository.RuntimePublicationWorkerRepository
	Delivery         integrationsdk.Delivery
	Worker           workerplatform.Dependencies
	Wakeups          *workerplatform.WakeupBroker
	PreparePayload   PayloadPreparer
}

// Accept persists a Runtime-owned handoff before any Integration owner call.
// The repository enforces message/deduplication-key idempotency.
func (s *Service) Accept(ctx context.Context, request integrationsdk.DeliveryRequest, createdBy string) (integrationsdk.DeliveryReceipt, error) {
	if s == nil || s.repository == nil {
		return integrationsdk.DeliveryReceipt{}, apperror.New(apperror.KindUnavailable, "backend.runtime.publication.repository_unavailable", nil, nil)
	}
	if err := request.Validate(); err != nil {
		return integrationsdk.DeliveryReceipt{}, err
	}
	payload := map[string]any{}
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return integrationsdk.DeliveryReceipt{}, err
	}
	fingerprint := sha256.Sum256(request.Payload)
	message, err := s.repository.InsertOutbox(ctx, request.WorkspaceID, integrationmodel.IntegrationOutboxMessage{
		ID: request.MessageID, WorkspaceID: request.WorkspaceID, ConnectorKey: request.ConnectorKey,
		ConnectionKey: request.ConnectionKey, Operation: request.Operation, Status: "queued", Payload: payload,
		DedupKey: request.DeduplicationKey, RequestFingerprint: fmt.Sprintf("%x", fingerprint[:]), CreatedBy: strings.TrimSpace(createdBy),
	})
	if err != nil {
		return integrationsdk.DeliveryReceipt{}, err
	}
	s.Wake(Locator{WorkspaceID: message.WorkspaceID, MessageID: message.ID})
	return integrationsdk.DeliveryReceipt{MessageID: message.ID, Status: integrationsdk.DeliveryStatusAccepted}, nil
}

func New(deps Dependencies) *Service {
	wakeups, publish := wakeupBinding(deps.Wakeups)
	return &Service{repository: deps.Repository, workerRepo: deps.WorkerRepository, delivery: deps.Delivery, worker: workerplatform.NormalizeDependencies(deps.Worker), wakeups: wakeups, publish: publish, prepare: deps.PreparePayload}
}

// ValidateActionDurableIntent validates only Runtime-owned envelope facts.
// Connector, connection, operation and provider capability validation happens
// at the Integration owner boundary when Delivery.Accept is called.
func (s *Service) ValidateActionDurableIntent(_ context.Context, intent runtimeext.DurableIntent, _ principalmodel.Principal) error {
	if !intent.Valid() {
		return apperror.New(apperror.KindBadRequest, "backend.action.durable_intent_invalid", nil, nil)
	}
	return nil
}

func (s *Service) ListIntegrationOutboxMessages(ctx context.Context, connectorKey, status string, limit int, principal principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error) {
	if s == nil || s.repository == nil {
		return nil, apperror.New(apperror.KindUnavailable, "backend.runtime.publication.repository_unavailable", nil, nil)
	}
	workspaceID := strings.TrimSpace(principal.WorkspaceID)
	if workspaceID == "" {
		return nil, apperror.New(apperror.KindBadRequest, "backend.workspace_scope_required", nil, nil)
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return s.repository.ListOutbox(ctx, workspaceID, strings.TrimSpace(connectorKey), strings.TrimSpace(status), limit)
}

func (s *Service) InspectIntegrationOutboxMessage(ctx context.Context, messageID string, principal principalmodel.Principal) (integrationmodel.IntegrationOutboxMessage, error) {
	reader, ok := s.repository.(integrationrepository.IntegrationOutboxReader)
	if !ok {
		return integrationmodel.IntegrationOutboxMessage{}, apperror.New(apperror.KindUnavailable, "backend.runtime.publication.reader_unavailable", nil, nil)
	}
	message, found, err := reader.GetOutbox(ctx, strings.TrimSpace(principal.WorkspaceID), strings.TrimSpace(messageID))
	if err != nil {
		return message, err
	}
	if !found {
		return message, apperror.New(apperror.KindNotFound, "backend.integration.outbox.not_found", nil, nil)
	}
	return message, nil
}

func (s *Service) UpdateIntegrationOutboxStatus(ctx context.Context, messageID string, request integrationmodel.IntegrationOutboxStatusRequest, principal principalmodel.Principal) (integrationmodel.IntegrationOutboxMessage, error) {
	status := strings.TrimSpace(request.Status)
	if status != "cancelled" && status != "queued" && status != "sent" && status != "failed" && status != "dead_letter" {
		return integrationmodel.IntegrationOutboxMessage{}, apperror.New(apperror.KindBadRequest, "backend.integration.outbox.invalid_status", nil, nil)
	}
	return s.repository.UpdateOutboxStatus(ctx, strings.TrimSpace(principal.WorkspaceID), strings.TrimSpace(messageID), status, strings.TrimSpace(request.ResponseRef), strings.TrimSpace(request.Error))
}

func (s *Service) ScheduleIntegrationOutboxRetry(ctx context.Context, messageID string, request integrationmodel.IntegrationOutboxRetryRequest, principal principalmodel.Principal) (integrationmodel.IntegrationOutboxMessage, error) {
	message, err := s.InspectIntegrationOutboxMessage(ctx, messageID, principal)
	if err != nil {
		return message, err
	}
	if message.Status != "failed" && message.Status != "dead_letter" && message.Status != "cancelled" {
		return message, apperror.New(apperror.KindBadRequest, "backend.integration.outbox.not_retryable", nil, nil)
	}
	return s.repository.ScheduleOutboxRetry(ctx, message.WorkspaceID, message.ID, 0, strings.TrimSpace(request.Error))
}

func (s *Service) Wake(locator Locator) {
	if s == nil || s.publish == nil || strings.TrimSpace(locator.MessageID) == "" {
		return
	}
	workspace, err := principalmodel.NewWorkspaceID(locator.WorkspaceID)
	if err != nil {
		return
	}
	s.publish(workerplatform.DurableTaskLocator{QueueKind: queueKind, WorkspaceID: workspace.String(), TaskID: strings.TrimSpace(locator.MessageID)})
}

func (s *Service) StartWorker(ctx context.Context, interval time.Duration, limit int) <-chan struct{} {
	if s == nil || s.workerRepo == nil || s.delivery == nil {
		return workerplatform.Stopped()
	}
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	if limit <= 0 {
		limit = 25
	}
	return workerplatform.StartWakeableDispatcherWithExecutors(ctx, workerplatform.DispatcherExecutorConfig{Name: queueKind, Concurrency: min(limit, 8)}, interval, s.wakeups, s.recoverLocators, func(executeCtx context.Context, locator workerplatform.DurableTaskLocator) {
		s.worker.Control.RunIfAccepting(func() {
			_, _ = s.process(executeCtx, Locator{WorkspaceID: locator.WorkspaceID, MessageID: locator.TaskID})
		})
	})
}

func (s *Service) recoverLocators(ctx context.Context, limit int) ([]workerplatform.DurableTaskLocator, error) {
	if limit <= 0 {
		limit = 25
	}
	due, err := s.workerRepo.ListDueOutbox(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "poll Runtime publication outbox"), limit, s.worker.Clock.Now().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	result := make([]workerplatform.DurableTaskLocator, 0, len(due))
	for _, message := range due {
		result = append(result, workerplatform.DurableTaskLocator{QueueKind: queueKind, WorkspaceID: message.WorkspaceID, TaskID: message.ID})
	}
	return result, nil
}

func (s *Service) process(ctx context.Context, locator Locator) (integrationmodel.IntegrationOutboxMessage, error) {
	reader, ok := s.repository.(integrationrepository.IntegrationOutboxReader)
	if !ok {
		return integrationmodel.IntegrationOutboxMessage{}, errors.New("Runtime publication reader unavailable")
	}
	message, found, err := reader.GetOutbox(ctx, locator.WorkspaceID, locator.MessageID)
	if err != nil || !found {
		return message, err
	}
	now := s.worker.Clock.Now()
	claimed, claimedOK, err := s.workerRepo.ClaimOutbox(ctx, message.WorkspaceID, message.ID, s.worker.WorkerID.String(), now.Format(time.RFC3339))
	if err != nil || !claimedOK {
		return claimed, err
	}
	workCtx, stopHeartbeat := workerplatform.WithHeartbeat(ctx, 90*time.Second, func(heartbeatCtx context.Context) error {
		_, heartbeatErr := s.workerRepo.HeartbeatOutbox(heartbeatCtx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, s.worker.Clock.Now().Format(time.RFC3339))
		return heartbeatErr
	})
	prepared := claimed.Payload
	if s.prepare != nil {
		prepared, err = s.prepare(workCtx, claimed, prepared)
	}
	var payload []byte
	if err == nil {
		payload, err = json.Marshal(prepared)
	}
	if err == nil {
		dedup := strings.TrimSpace(claimed.DedupKey)
		if dedup == "" {
			dedup = claimed.ID
		}
		var receipt integrationsdk.DeliveryReceipt
		receipt, err = s.delivery.Accept(workCtx, integrationsdk.DeliveryRequest{MessageID: claimed.ID, DeduplicationKey: dedup, WorkspaceID: claimed.WorkspaceID, ConnectorKey: claimed.ConnectorKey, ConnectionKey: claimed.ConnectionKey, Operation: claimed.Operation, Payload: payload})
		if err == nil {
			err = stopHeartbeat()
			if err == nil {
				return s.workerRepo.UpdateOutboxStatus(ctx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, "sent", strings.TrimSpace(receipt.InvocationID), "", "", s.worker.Clock.Now().Format(time.RFC3339))
			}
		}
	}
	if heartbeatErr := stopHeartbeat(); err == nil {
		err = heartbeatErr
	}
	if ctx.Err() != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return s.workerRepo.UpdateOutboxStatus(cleanupCtx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, "queued", "", "", "", s.worker.Clock.Now().Format(time.RFC3339))
	}
	if claimed.AttemptCount >= maxDeliveryAttempts {
		return s.workerRepo.UpdateOutboxStatus(ctx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, "dead_letter", "", stableError(err), "", s.worker.Clock.Now().Format(time.RFC3339))
	}
	// Delivery.Accept is idempotent by message/deduplication key, so an unknown
	// transport outcome is safely retried without Runtime interpreting provider state.
	retry, retryErr := s.workerRepo.ScheduleOutboxRetry(ctx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, retryDelay(claimed.AttemptCount), stableError(err), s.worker.Clock.Now().Format(time.RFC3339))
	if retryErr != nil {
		return claimed, retryErr
	}
	return retry, err
}

func stableError(err error) string {
	if err == nil {
		return "backend.runtime.publication.delivery_failed"
	}
	if code := apperror.CodeOf(err); strings.TrimSpace(code) != "" {
		return code
	}
	return "backend.runtime.publication.delivery_failed"
}

func retryDelay(attempt int) int {
	delay := 5
	for i := 0; i < attempt && delay < 300; i++ {
		delay *= 2
	}
	if delay > 300 {
		return 300
	}
	return delay
}

func wakeupBinding(broker *workerplatform.WakeupBroker) (<-chan workerplatform.DurableTaskLocator, func(workerplatform.DurableTaskLocator)) {
	if broker != nil {
		return broker.Subscribe(queueKind, 256), broker.Publish
	}
	wakeups := make(chan workerplatform.DurableTaskLocator, 256)
	return wakeups, func(locator workerplatform.DurableTaskLocator) {
		select {
		case wakeups <- locator:
		default:
		}
	}
}
