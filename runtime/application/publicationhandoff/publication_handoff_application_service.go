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
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	publicationrepository "github.com/domainry/domainry-runtime/runtime/domain/publication/repository"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const queueKind = "runtime_publication_outbox"
const maxHandoffAttempts = 10

type Locator struct {
	WorkspaceID string
	MessageID   string
}

type PublicationHandoffApplicationService struct {
	repository publicationrepository.Repository
	workerRepo publicationrepository.WorkerRepository
	delivery   integrationsdk.Delivery
	worker     workerplatform.Dependencies
	wakeups    <-chan workerplatform.DurableTaskLocator
	publish    func(workerplatform.DurableTaskLocator)
	prepare    PayloadPreparer
}

type PayloadPreparer func(context.Context, publicationmodel.Message, map[string]any) (map[string]any, error)

type Dependencies struct {
	Repository       publicationrepository.Repository
	WorkerRepository publicationrepository.WorkerRepository
	Delivery         integrationsdk.Delivery
	Worker           workerplatform.Dependencies
	Wakeups          *workerplatform.WakeupBroker
	PreparePayload   PayloadPreparer
}

// Accept persists a Runtime-owned handoff before any Integration owner call.
// The repository enforces message/deduplication-key idempotency.
func (s *PublicationHandoffApplicationService) Accept(ctx context.Context, request integrationsdk.DeliveryRequest, createdBy string) (integrationsdk.DeliveryReceipt, error) {
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
	message, err := s.repository.InsertOutbox(ctx, request.WorkspaceID, publicationmodel.Message{
		ID: request.MessageID, WorkspaceID: request.WorkspaceID, ConnectorKey: request.ConnectorKey,
		ConnectionKey: request.ConnectionKey, Operation: request.Operation, Status: "queued", Payload: payload,
		DedupKey: request.DeduplicationKey, RequestFingerprint: fmt.Sprintf("%x", fingerprint[:]), CreatedBy: strings.TrimSpace(createdBy),
	})
	if err != nil {
		return integrationsdk.DeliveryReceipt{}, err
	}
	s.Wake(ctx, Locator{WorkspaceID: message.WorkspaceID, MessageID: message.ID})
	return integrationsdk.DeliveryReceipt{MessageID: message.ID, Status: integrationsdk.DeliveryStatusAccepted}, nil
}

func NewPublicationHandoffApplicationService(deps Dependencies) *PublicationHandoffApplicationService {
	wakeups, publish := wakeupBinding(deps.Wakeups)
	return &PublicationHandoffApplicationService{repository: deps.Repository, workerRepo: deps.WorkerRepository, delivery: deps.Delivery, worker: workerplatform.NormalizeDependencies(deps.Worker), wakeups: wakeups, publish: publish, prepare: deps.PreparePayload}
}

// ValidateActionDurableIntent validates only Runtime-owned envelope facts.
// Connector, connection, operation and provider capability validation happens
// at the Integration owner boundary when Delivery.Accept is called.
func (s *PublicationHandoffApplicationService) ValidateActionDurableIntent(_ context.Context, intent runtimeext.DurableIntent, _ principalmodel.Principal) error {
	if !intent.Valid() {
		return apperror.New(apperror.KindBadRequest, "backend.action.durable_intent_invalid", nil, nil)
	}
	return nil
}

func (s *PublicationHandoffApplicationService) ListPublicationMessages(ctx context.Context, connectorKey, status string, limit int, principal principalmodel.Principal) ([]publicationmodel.Message, error) {
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

func (s *PublicationHandoffApplicationService) InspectPublicationMessage(ctx context.Context, messageID string, principal principalmodel.Principal) (publicationmodel.Message, error) {
	reader, ok := s.repository.(publicationrepository.Reader)
	if !ok {
		return publicationmodel.Message{}, apperror.New(apperror.KindUnavailable, "backend.runtime.publication.reader_unavailable", nil, nil)
	}
	message, found, err := reader.GetOutbox(ctx, strings.TrimSpace(principal.WorkspaceID), strings.TrimSpace(messageID))
	if err != nil {
		return message, err
	}
	if !found {
		return message, apperror.New(apperror.KindNotFound, "backend.runtime.publication.not_found", nil, nil)
	}
	return message, nil
}

// GetBusinessPublicationHandoff projects only the caller-owned Runtime handoff.
// Provider execution evidence remains Integration-owned and is referenced by
// response_ref rather than joined through owner tables.
func (s *PublicationHandoffApplicationService) GetBusinessPublicationHandoff(ctx context.Context, messageID string, principal principalmodel.Principal) (publicationmodel.Handoff, error) {
	workspaceID, userID := strings.TrimSpace(principal.WorkspaceID), strings.TrimSpace(principal.UserID)
	if workspaceID == "" || userID == "" {
		return publicationmodel.Handoff{}, apperror.New(apperror.KindBadRequest, "backend.workspace_scope_required", nil, nil)
	}
	reader, ok := s.repository.(publicationrepository.Reader)
	if !ok {
		return publicationmodel.Handoff{}, apperror.New(apperror.KindUnavailable, "backend.runtime.publication.reader_unavailable", nil, nil)
	}
	message, found, err := reader.GetOutbox(ctx, workspaceID, strings.TrimSpace(messageID))
	if err != nil {
		return publicationmodel.Handoff{}, err
	}
	if !found || strings.TrimSpace(message.CreatedBy) == "" || strings.TrimSpace(message.CreatedBy) != userID {
		return publicationmodel.Handoff{}, apperror.New(apperror.KindNotFound, "backend.runtime.publication.handoff_not_found", nil, nil)
	}
	return publicationmodel.Handoff{
		ID: message.ID, ConnectorKey: message.ConnectorKey, ConnectionKey: message.ConnectionKey,
		Operation: message.Operation, Status: message.Status, ResponseRef: message.ResponseRef,
		Error: message.Error, AttemptCount: message.AttemptCount, CreatedAt: message.CreatedAt, UpdatedAt: message.UpdatedAt,
	}, nil
}

func (s *PublicationHandoffApplicationService) UpdatePublicationStatus(ctx context.Context, messageID string, request publicationmodel.StatusRequest, principal principalmodel.Principal) (publicationmodel.Message, error) {
	status := strings.TrimSpace(request.Status)
	if status != "cancelled" && status != "queued" && status != "accepted" && status != "failed" && status != "dead_letter" {
		return publicationmodel.Message{}, apperror.New(apperror.KindBadRequest, "backend.runtime.publication.invalid_status", nil, nil)
	}
	return s.repository.UpdateOutboxStatus(ctx, strings.TrimSpace(principal.WorkspaceID), strings.TrimSpace(messageID), status, strings.TrimSpace(request.ResponseRef), strings.TrimSpace(request.Error))
}

func (s *PublicationHandoffApplicationService) SchedulePublicationRetry(ctx context.Context, messageID string, request publicationmodel.RetryRequest, principal principalmodel.Principal) (publicationmodel.Message, error) {
	message, err := s.InspectPublicationMessage(ctx, messageID, principal)
	if err != nil {
		return message, err
	}
	if message.Status != "failed" && message.Status != "dead_letter" && message.Status != "cancelled" {
		return message, apperror.New(apperror.KindBadRequest, "backend.runtime.publication.not_retryable", nil, nil)
	}
	return s.repository.ScheduleOutboxRetry(ctx, message.WorkspaceID, message.ID, 0, strings.TrimSpace(request.Error))
}

func (s *PublicationHandoffApplicationService) Wake(_ context.Context, locator Locator) {
	if s == nil || s.publish == nil || strings.TrimSpace(locator.MessageID) == "" {
		return
	}
	workspace, err := principalmodel.NewWorkspaceID(locator.WorkspaceID)
	if err != nil {
		return
	}
	s.publish(workerplatform.DurableTaskLocator{QueueKind: queueKind, WorkspaceID: workspace.String(), TaskID: strings.TrimSpace(locator.MessageID)})
}

func (s *PublicationHandoffApplicationService) StartWorker(ctx context.Context, interval time.Duration, limit int) <-chan struct{} {
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

func (s *PublicationHandoffApplicationService) recoverLocators(ctx context.Context, limit int) ([]workerplatform.DurableTaskLocator, error) {
	if limit <= 0 {
		limit = 25
	}
	due, err := s.workerRepo.ListDueOutbox(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "poll Runtime publication outbox"), limit, s.worker.Clock.Now().Format(time.RFC3339))
	if err != nil {
		workerplatform.ObserveOutcome(queueKind, "failed")
		return nil, err
	}
	workerplatform.SetQueueMetrics(queueKind, len(due), oldestPublicationLag(due, s.worker.Clock.Now()))
	result := make([]workerplatform.DurableTaskLocator, 0, len(due))
	for _, message := range due {
		result = append(result, workerplatform.DurableTaskLocator{QueueKind: queueKind, WorkspaceID: message.WorkspaceID, TaskID: message.ID})
	}
	return result, nil
}

func (s *PublicationHandoffApplicationService) process(ctx context.Context, locator Locator) (publicationmodel.Message, error) {
	reader, ok := s.repository.(publicationrepository.Reader)
	if !ok {
		return publicationmodel.Message{}, errors.New("Runtime publication reader unavailable")
	}
	message, found, err := reader.GetOutbox(ctx, locator.WorkspaceID, locator.MessageID)
	if err != nil || !found {
		return message, err
	}
	if err = workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerClaim); err != nil {
		return message, err
	}
	now := s.worker.Clock.Now()
	claimed, claimedOK, err := s.workerRepo.ClaimOutbox(ctx, message.WorkspaceID, message.ID, s.worker.WorkerID.String(), now.Format(time.RFC3339))
	if err != nil || !claimedOK {
		if err != nil {
			workerplatform.ObserveOutcome(queueKind, "failed")
		} else {
			workerplatform.ObserveOutcome(queueKind, "skipped")
		}
		return claimed, err
	}
	workerplatform.ObserveOutcome(queueKind, "claimed")
	workCtx, stopHeartbeat := workerplatform.WithHeartbeat(ctx, 90*time.Second, func(heartbeatCtx context.Context) error {
		if heartbeatErr := workerplatform.CheckFault(heartbeatCtx, s.worker.Faults, workerplatform.FaultWorkerHeartbeat); heartbeatErr != nil {
			return heartbeatErr
		}
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
		err = workerplatform.CheckFault(workCtx, s.worker.Faults, workerplatform.FaultProviderBeforeSend)
	}
	if err == nil {
		dedup := strings.TrimSpace(claimed.DedupKey)
		if dedup == "" {
			dedup = claimed.ID
		}
		var receipt integrationsdk.DeliveryReceipt
		receipt, err = s.delivery.Accept(workCtx, integrationsdk.DeliveryRequest{MessageID: claimed.ID, DeduplicationKey: dedup, WorkspaceID: claimed.WorkspaceID, ConnectorKey: claimed.ConnectorKey, ConnectionKey: claimed.ConnectionKey, Operation: claimed.Operation, Payload: payload})
		if err == nil {
			err = workerplatform.CheckFault(workCtx, s.worker.Faults, workerplatform.FaultProviderAfterSend)
		}
		if err == nil {
			err = stopHeartbeat()
			if err == nil {
				if err = workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerBeforeComplete); err == nil {
					var completed publicationmodel.Message
					completed, err = s.workerRepo.UpdateOutboxStatus(ctx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, "accepted", strings.TrimSpace(receipt.InvocationID), "", s.worker.Clock.Now().Format(time.RFC3339))
					if err == nil {
						err = workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerAfterComplete)
					}
					if err == nil {
						workerplatform.ObserveOutcome(queueKind, "completed")
					} else {
						workerplatform.ObserveOutcome(queueKind, "failed")
					}
					return completed, err
				}
			}
		}
	}
	if heartbeatErr := stopHeartbeat(); err == nil {
		err = heartbeatErr
	}
	if ctx.Err() != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		queued, updateErr := s.workerRepo.UpdateOutboxStatus(cleanupCtx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, "queued", "", "", s.worker.Clock.Now().Format(time.RFC3339))
		workerplatform.ObserveOutcome(queueKind, "cancelled")
		return queued, updateErr
	}
	if claimed.AttemptCount >= maxHandoffAttempts {
		dead, updateErr := s.workerRepo.UpdateOutboxStatus(ctx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, "dead_letter", "", stableError(err), s.worker.Clock.Now().Format(time.RFC3339))
		if updateErr == nil {
			workerplatform.ObserveOutcome(queueKind, "dead_letter")
		} else {
			workerplatform.ObserveOutcome(queueKind, "failed")
		}
		return dead, updateErr
	}
	// Delivery.Accept is idempotent by message/deduplication key, so an unknown
	// transport outcome is safely retried without Runtime interpreting provider state.
	retry, retryErr := s.workerRepo.ScheduleOutboxRetry(ctx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, retryDelay(claimed.AttemptCount), stableError(err), s.worker.Clock.Now().Format(time.RFC3339))
	if retryErr != nil {
		workerplatform.ObserveOutcome(queueKind, "failed")
		return claimed, retryErr
	}
	workerplatform.ObserveOutcome(queueKind, "retry")
	return retry, err
}

func oldestPublicationLag(messages []publicationmodel.Message, now time.Time) time.Duration {
	var lag time.Duration
	for _, message := range messages {
		createdAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(message.CreatedAt))
		if err != nil {
			createdAt, err = time.Parse(time.RFC3339, strings.TrimSpace(message.CreatedAt))
		}
		if err != nil || !now.After(createdAt) {
			continue
		}
		if candidate := now.Sub(createdAt); candidate > lag {
			lag = candidate
		}
	}
	return lag
}

func stableError(err error) string {
	if err == nil {
		return "backend.runtime.publication.handoff_failed"
	}
	if code := apperror.CodeOf(err); strings.TrimSpace(code) != "" {
		return code
	}
	return "backend.runtime.publication.handoff_failed"
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
