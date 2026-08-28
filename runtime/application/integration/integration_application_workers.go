package integration

import integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"

import (
	"context"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	"strings"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-foundation/telemetry"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
)

func (s *IntegrationApplicationService) StartEventWorker(ctx context.Context, interval time.Duration, limit int) <-chan struct{} {
	if s.registry == nil || !s.registry.HasEventWork() || s.workerRepo == nil {
		return workerplatform.Stopped()
	}
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	if limit <= 0 {
		limit = 25
	}
	return workerplatform.StartWakeableRecoveryLoop(ctx, "integration_event", interval, IntegrationEventWakeups(s), func() {
		s.worker.Control.RunIfAccepting(func() { _ = s.processEventWorkerTick(ctx, limit) })
	}, func(locator IntegrationEventLocator) {
		s.worker.Control.RunIfAccepting(func() { _ = s.processEventWorkerLocator(ctx, locator) })
	})
}

func (s *IntegrationApplicationService) StartOutboxWorker(ctx context.Context, interval time.Duration, limit int) <-chan struct{} {
	if s.registry == nil || !s.registry.HasOutboxSenders() || s.workerRepo == nil {
		return workerplatform.Stopped()
	}
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	if limit <= 0 {
		limit = 25
	}
	return workerplatform.StartWakeableDispatcherWithExecutors(ctx, workerplatform.DispatcherExecutorConfig{Name: "integration_outbox", Concurrency: min(limit, 8)}, interval, IntegrationOutboxWakeups(s), s.recoverIntegrationOutboxLocators, func(executeCtx context.Context, locator workerplatform.DurableTaskLocator) {
		s.worker.Control.RunIfAccepting(func() {
			_ = s.processOutboxWorkerLocator(executeCtx, IntegrationOutboxLocator{WorkspaceID: locator.WorkspaceID, MessageID: locator.TaskID})
		})
	})
}

func (s *IntegrationApplicationService) processEventWorkerTick(ctx context.Context, limit int) bool {
	result, err := s.ProcessDueIntegrationEvents(ctx, limit, integrationruntime.IntegrationWorkerPrincipal(principalmodel.InstallationWorkspaceID))
	if err != nil {
		logging.FromContext(ctx).Error("integration event worker failed", zap.String("error_code", stableIntegrationFailureCode(err, "backend.integration.event.worker_failed")))
		return false
	}
	if result.Processed+result.Retried+result.DeadLettered+result.Skipped > 0 {
		logging.FromContext(ctx).Info("integration event worker completed", zap.Int("processed", result.Processed), zap.Int("retried", result.Retried), zap.Int("dead_lettered", result.DeadLettered), zap.Int("skipped", result.Skipped))
	}
	return result.Processed+result.Retried+result.DeadLettered+result.Skipped > 0
}

func (s *IntegrationApplicationService) ProcessDueIntegrationEvents(ctx context.Context, limit int, principal principalmodel.Principal) (EventProcessBatchResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return EventProcessBatchResult{}, err
	}
	if !HasPermission(principal, PermissionRetry) {
		return EventProcessBatchResult{}, forbidden("auth.permission_denied")
	}
	if limit <= 0 {
		limit = 25
	} else if limit > 200 {
		limit = 200
	}
	workerScope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "poll due integration events")
	due, err := s.workerRepo.ListDueEvents(ctx, workerScope, capacityplatform.OverscanLimit(limit, 4, 800), s.worker.Clock.Now().Format(time.RFC3339))
	if err != nil {
		return EventProcessBatchResult{}, err
	}
	due = s.filterProcessableEvents(due)
	retryQuota, manualQuota := max(1, limit/4), max(1, limit/4)
	due = capacityplatform.QuotaFairOrder(due, limit, func(event integrationmodel.IntegrationEvent) string {
		return event.WorkspaceID + "\x00" + event.Provider
	}, integrationEventPriorityClass, []string{"retry", "manual_replay", "new"}, map[string]int{"retry": retryQuota, "manual_replay": manualQuota, "new": max(0, limit-retryQuota-manualQuota)})
	result := EventProcessBatchResult{Events: []integrationmodel.IntegrationEvent{}}
	for _, event := range due {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		processed, bucket := s.processDueEvent(ctx, event, integrationruntime.IntegrationWorkerPrincipal(event.WorkspaceID))
		result.Events = append(result.Events, processed)
		switch bucket {
		case "processed":
			result.Processed++
		case "retried":
			result.Retried++
		case "dead_lettered":
			result.DeadLettered++
		default:
			result.Skipped++
		}
	}
	return result, nil
}

func (s *IntegrationApplicationService) filterProcessableEvents(events []integrationmodel.IntegrationEvent) []integrationmodel.IntegrationEvent {
	if len(events) == 0 {
		return events
	}
	out := make([]integrationmodel.IntegrationEvent, 0, len(events))
	for _, event := range events {
		if strings.TrimSpace(event.Status) != "received" {
			out = append(out, event)
			continue
		}
		if _, ok := s.IntegrationEventHandler(event.Provider); ok {
			out = append(out, event)
			continue
		}
		if _, ok := s.EventMappingForEvent(event); ok {
			out = append(out, event)
		}
	}
	return out
}

func integrationEventPriorityClass(event integrationmodel.IntegrationEvent) string {
	if strings.TrimSpace(event.Status) == "failed" {
		return "retry"
	}
	if strings.TrimSpace(event.Error) == "backend.integration.event.manual_replay_queued" {
		return "manual_replay"
	}
	return "new"
}

func (s *IntegrationApplicationService) processDueEvent(ctx context.Context, event integrationmodel.IntegrationEvent, principal principalmodel.Principal) (integrationmodel.IntegrationEvent, string) {
	started := s.worker.Clock.Now()
	var claimed integrationmodel.IntegrationEvent
	var claimedOK bool
	err := workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerClaim)
	if err == nil {
		claimed, claimedOK, err = s.workerRepo.ClaimEvent(ctx, event.WorkspaceID, event.ID, s.worker.WorkerID.String(), started.Format(time.RFC3339))
	}
	if err != nil {
		s.audit(ctx, "integration_event_worker_skipped", "integration_event", event.ID, principal, "Skipped integration event "+event.ID, integrationprojection.IntegrationEventAuditShape(event), nil, map[string]any{"workspace_id": event.WorkspaceID, "provider": event.Provider, "reason": "claim_failed", "error_code": stableIntegrationFailureCode(err, "backend.integration.event.claim_failed")})
		return event, "skipped"
	}
	if !claimedOK {
		s.audit(ctx, "integration_event_worker_skipped", "integration_event", event.ID, principal, "Skipped integration event "+event.ID, integrationprojection.IntegrationEventAuditShape(event), integrationprojection.IntegrationEventAuditShape(claimed), map[string]any{"workspace_id": event.WorkspaceID, "provider": event.Provider, "reason": "already_claimed_or_not_due"})
		return claimed, "skipped"
	}
	connection, executionEvent, hasExecutionContext := s.EventExecutionContext(ctx, claimed)
	workCtx, stopHeartbeat := workerplatform.WithHeartbeat(ctx, 90*time.Second, s.eventHeartbeat(claimed))
	/*
		Keep heartbeat I/O in a separately testable callback factory. The worker
		library still owns scheduling and cancellation of that callback.
	*/
	var decision EventProcessDecision
	if handler, ok := s.IntegrationEventHandler(claimed.Provider); ok {
		decision, err = handler.ProcessIntegrationEvent(workCtx, executionEvent, principal)
	} else {
		var handled bool
		decision, handled, err = s.executeEventMapping(workCtx, executionEvent, principal)
		if err == nil && !handled {
			saved, updateErr := s.workerRepo.UpdateEventStatus(ctx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, "failed", "backend.integration.event.handler_not_found", s.worker.Clock.Now().Format(time.RFC3339))
			if updateErr != nil {
				saved = claimed
			}
			s.audit(ctx, "integration_event_worker_skipped", "integration_event", claimed.ID, principal, "Skipped integration event "+claimed.ID, integrationprojection.IntegrationEventAuditShape(claimed), integrationprojection.IntegrationEventAuditShape(saved), map[string]any{"workspace_id": claimed.WorkspaceID, "provider": claimed.Provider, "reason": "handler_not_found"})
			return saved, "skipped"
		}
	}
	err = mergeIntegrationHeartbeatError(err, stopHeartbeat())
	if hasExecutionContext {
		status, errorText, providerErrorCode := "succeeded", "", ""
		if err != nil {
			status = "failed"
			normalized, code := integrationpolicy.NormalizeProviderError(err)
			providerErrorCode, errorText = code, normalized.Error()
		}
		_, evidenceErr := s.RecordIntegrationExecutionEvidence(ctx, ExecutionEvidence{Connection: connection, Operation: "receive_event", Status: status, StartedAt: started, RequestRef: claimed.ExternalID, Error: errorText, EventID: claimed.ID, Request: executionEvent.Payload, Response: map[string]any{"status": decision.Status, "error": decision.Error}, Source: "event_handler", SourceID: claimed.ID, ErrorCode: providerErrorCode}, principal)
		if err == nil && evidenceErr != nil {
			err = evidenceErr
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return claimed, "skipped"
		}
		return s.failDueEvent(ctx, claimed, principal, err)
	}
	status := strings.TrimSpace(decision.Status)
	if status == "" {
		status = "processed"
	}
	if _, err := NormalizeEventStatus(status); err != nil {
		return s.failDueEvent(ctx, claimed, principal, err)
	}
	if err := workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerBeforeComplete); err != nil {
		return s.failDueEvent(ctx, claimed, principal, err)
	}
	saved, err := s.workerRepo.UpdateEventStatus(ctx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, status, strings.TrimSpace(decision.Error), s.worker.Clock.Now().Format(time.RFC3339))
	if err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
			return claimed, "skipped"
		}
		return s.failDueEvent(ctx, claimed, principal, err)
	}
	if workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerAfterComplete) != nil {
		return saved, "skipped"
	}
	s.audit(ctx, "integration_event_worker_processed", "integration_event", saved.ID, principal, "Processed integration event "+saved.ID, integrationprojection.IntegrationEventAuditShape(claimed), integrationprojection.IntegrationEventAuditShape(saved), map[string]any{"workspace_id": claimed.WorkspaceID, "provider": claimed.Provider, "event_type": claimed.EventType, "external_id": claimed.ExternalID, "status": saved.Status})
	return saved, "processed"
}

func (s *IntegrationApplicationService) processDueOutboxMessage(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, principal principalmodel.Principal) (processed integrationmodel.IntegrationOutboxMessage, bucket string) {
	claimStarted := s.worker.Clock.Now()
	var claimed integrationmodel.IntegrationOutboxMessage
	var claimedOK bool
	err := workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerClaim)
	if err == nil {
		claimed, claimedOK, err = s.workerRepo.ClaimOutbox(ctx, message.WorkspaceID, message.ID, s.worker.WorkerID.String(), claimStarted.Format(time.RFC3339))
	}
	if s.operationalMetrics != nil {
		s.operationalMetrics.observeClaim(s.worker.Clock.Now().Sub(claimStarted))
	}
	if err != nil {
		s.audit(ctx, "integration_outbox_worker_skipped", "integration_outbox", message.ID, principal, "Skipped integration outbox message "+message.ID, integrationprojection.IntegrationOutboxAuditShape(message), nil, map[string]any{"workspace_id": message.WorkspaceID, "connector_key": message.ConnectorKey, "operation": message.Operation, "reason": "claim_failed", "error_code": stableIntegrationFailureCode(err, "backend.integration.outbox.claim_failed")})
		return message, "skipped"
	}
	if !claimedOK {
		s.audit(ctx, "integration_outbox_worker_skipped", "integration_outbox", message.ID, principal, "Skipped integration outbox message "+message.ID, integrationprojection.IntegrationOutboxAuditShape(message), integrationprojection.IntegrationOutboxAuditShape(claimed), map[string]any{"workspace_id": message.WorkspaceID, "connector_key": message.ConnectorKey, "operation": message.Operation, "reason": "already_claimed_or_not_due"})
		return claimed, "skipped"
	}
	sender, ok := s.IntegrationOutboxSender(claimed.ConnectorKey)
	if !ok {
		const errorCode = "backend.integration.outbox.sender_not_found"
		retried, retryErr := s.workerRepo.ScheduleOutboxRetry(ctx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, 60, errorCode, s.worker.Clock.Now().Format(time.RFC3339))
		if retryErr == nil {
			return retried, "skipped"
		}
		claimed.Error = errorCode
		return claimed, "skipped"
	}
	if s.operationalMetrics != nil {
		s.operationalMetrics.beginWork()
		defer func() { s.operationalMetrics.endWork(bucket) }()
	}
	telemetryValues, _ := claimed.Payload[telemetry.AsyncPayloadKey].(map[string]any)
	link := telemetry.ParseAsyncLink(telemetryValues)
	ctx, span := telemetry.StartLinkedSpan(ctx, "domainry.runtime.integration", "integration.outbox.send", link)
	ctx = requestcontext.WithCorrelationID(ctx, link.CorrelationID)
	ctx = requestcontext.WithWorkspaceID(ctx, claimed.WorkspaceID)
	ctx = requestcontext.WithActorID(ctx, claimed.CreatedBy)
	ctx = requestcontext.WithOwnerExecutionID(ctx, claimed.ID)
	span.SetAttributes(
		attribute.String("integration.connector", claimed.ConnectorKey),
		attribute.String("integration.operation", claimed.Operation),
		attribute.String("integration.outbox_id", claimed.ID),
	)
	defer func() {
		span.SetAttributes(attribute.String("integration.result", bucket))
		if bucket != "sent" {
			span.SetStatus(codes.Error, bucket)
		}
		span.End()
	}()
	workCtx, stopHeartbeat := workerplatform.WithHeartbeat(ctx, 90*time.Second, s.outboxHeartbeat(claimed))
	var sendResult OutboxSendResult
	faultErr := workerplatform.CheckFault(workCtx, s.worker.Faults, workerplatform.FaultProviderBeforeSend)
	if faultErr != nil {
		err = &apperror.AppError{Kind: apperror.KindUnavailable, Code: "backend.integration.provider.not_attempted"}
	} else {
		sendResult, err = sender.SendIntegrationOutboxMessage(workCtx, claimed, principal)
	}
	if err == nil {
		err = workerplatform.CheckFault(workCtx, s.worker.Faults, workerplatform.FaultProviderAfterSend)
	}
	err = mergeIntegrationHeartbeatError(err, stopHeartbeat())
	if err != nil {
		if ctx.Err() != nil {
			// A managed shutdown is not a delivery failure. Return the durable
			// claim to queued immediately so the replacement process does not
			// have to wait for the long provider lease to expire. The cleanup
			// keeps the original request values (including workspace scope) while
			// deliberately detaching from the cancelled worker context.
			cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancelCleanup()
			released, releaseErr := s.workerRepo.UpdateOutboxStatus(
				cleanupCtx,
				claimed.WorkspaceID,
				claimed.ID,
				claimed.LeaseOwner,
				claimed.FencingToken,
				"queued",
				"",
				"",
				"",
				s.worker.Clock.Now().Format(time.RFC3339),
			)
			if releaseErr == nil {
				return released, "skipped"
			}
			if mutation.IsMutationConflict(releaseErr, mutation.MutationConflictLeaseLost) && s.operationalMetrics != nil {
				s.operationalMetrics.observeLeaseLost()
			}
			return claimed, "skipped"
		}
		return s.failDueOutboxMessage(ctx, claimed, principal, err, sendResult.ResponseRef)
	}
	status := strings.TrimSpace(sendResult.Status)
	if status == "" {
		status = "sent"
	}
	if _, err := NormalizeOutboxStatus(status); err != nil {
		return s.failDueOutboxMessage(ctx, claimed, principal, err)
	}
	if err := workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerBeforeComplete); err != nil {
		return s.failDueOutboxMessage(ctx, claimed, principal, err, sendResult.ResponseRef)
	}
	completedAt := s.worker.Clock.Now()
	ackDeadlineAt := ""
	if status == "sent" && sendResult.AckTimeoutSeconds > 0 {
		ackDeadlineAt = completedAt.Add(time.Duration(sendResult.AckTimeoutSeconds) * time.Second).Format(time.RFC3339)
	}
	saved, err := s.workerRepo.UpdateOutboxStatus(ctx, claimed.WorkspaceID, claimed.ID, claimed.LeaseOwner, claimed.FencingToken, status, strings.TrimSpace(sendResult.ResponseRef), "", ackDeadlineAt, completedAt.Format(time.RFC3339))
	if err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
			if s.operationalMetrics != nil {
				s.operationalMetrics.observeLeaseLost()
			}
			return claimed, "skipped"
		}
		return s.failDueOutboxMessage(ctx, claimed, principal, err)
	}
	if workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerAfterComplete) != nil {
		return saved, "skipped"
	}
	if err := s.emitSuccessfulGmailDeliveryEvent(ctx, saved, sendResult, principal); err != nil {
		s.audit(ctx, "integration_outbox_delivery_projection_failed", "integration_outbox", saved.ID, principal, "Failed to project sent Gmail outbox message "+saved.ID, integrationprojection.IntegrationOutboxAuditShape(claimed), integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{"workspace_id": saved.WorkspaceID, "connector_key": saved.ConnectorKey, "operation": saved.Operation, "status": saved.Status, "error_code": stableIntegrationFailureCode(err, "backend.integration.gmail_send.delivery_projection_failed")})
		return saved, "reconciliation_required"
	}
	if err := s.emitSuccessfulAppointmentBookingEvent(ctx, saved, sendResult, principal); err != nil {
		s.audit(ctx, "integration_outbox_delivery_projection_failed", "integration_outbox", saved.ID, principal, "Failed to project sent appointment outbox message "+saved.ID, integrationprojection.IntegrationOutboxAuditShape(claimed), integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{"workspace_id": saved.WorkspaceID, "connector_key": saved.ConnectorKey, "operation": saved.Operation, "status": saved.Status, "error_code": stableIntegrationFailureCode(err, "backend.integration.appointment_scheduling.delivery_projection_failed")})
		return saved, "reconciliation_required"
	}
	s.audit(ctx, "integration_outbox_worker_sent", "integration_outbox", saved.ID, principal, "Sent integration outbox message "+saved.ID, integrationprojection.IntegrationOutboxAuditShape(claimed), integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{"workspace_id": saved.WorkspaceID, "connector_key": saved.ConnectorKey, "operation": saved.Operation, "status": saved.Status, "response_ref": saved.ResponseRef})
	return saved, "sent"
}

func (s *IntegrationApplicationService) eventHeartbeat(event integrationmodel.IntegrationEvent) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerHeartbeat); err != nil {
			return err
		}
		_, err := s.workerRepo.HeartbeatEvent(ctx, event.WorkspaceID, event.ID, event.LeaseOwner, event.FencingToken, s.worker.Clock.Now().Format(time.RFC3339))
		return err
	}
}

func (s *IntegrationApplicationService) outboxHeartbeat(message integrationmodel.IntegrationOutboxMessage) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := workerplatform.CheckFault(ctx, s.worker.Faults, workerplatform.FaultWorkerHeartbeat); err != nil {
			return err
		}
		_, err := s.workerRepo.HeartbeatOutbox(ctx, message.WorkspaceID, message.ID, message.LeaseOwner, message.FencingToken, s.worker.Clock.Now().Format(time.RFC3339))
		return err
	}
}

func mergeIntegrationHeartbeatError(current, heartbeat error) error {
	if current == nil && heartbeat != nil {
		return heartbeat
	}
	return current
}
