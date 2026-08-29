package integration

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/logging"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"go.uber.org/zap"

	"strings"
)

func (s *IntegrationApplicationService) SendIntegrationOutboxMessage(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, principal principalmodel.Principal) (OutboxSendResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return OutboxSendResult{}, err
	}
	if err := integrationAuthorizeWorkspaceCommand(message.WorkspaceID); err != nil {
		return OutboxSendResult{}, err
	}
	if strings.TrimSpace(message.WorkspaceID) != principalWorkspaceID(principal) {
		return OutboxSendResult{}, forbidden("auth.permission_denied")
	}
	if strings.TrimSpace(message.ConnectorKey) == "__automation__" {
		if err := s.executeAutomationOutbox(ctx, message); err != nil {
			return OutboxSendResult{}, err
		}
		return OutboxSendResult{Status: "sent", ResponseRef: "automation:" + message.Operation}, nil
	}
	return s.sendAdapterOutbox(ctx, message, principal)
}

type IntegrationOutboxLocator struct {
	WorkspaceID string
	MessageID   string
}

func newIntegrationOutboxWakeupBinding(broker *workerplatform.WakeupBroker) (<-chan workerplatform.DurableTaskLocator, func(workerplatform.DurableTaskLocator)) {
	if broker != nil {
		return broker.Subscribe("integration_outbox", 256), broker.Publish
	}
	wakeups := make(chan workerplatform.DurableTaskLocator, 256)
	publish := func(locator workerplatform.DurableTaskLocator) {
		select {
		case wakeups <- locator:
		default:
		}
	}
	return wakeups, publish
}

func IntegrationOutboxWakeups(service *IntegrationApplicationService) <-chan workerplatform.DurableTaskLocator {
	if service == nil {
		return nil
	}
	return service.outboxWakeups
}

func WakeIntegrationOutbox(service *IntegrationApplicationService, locator IntegrationOutboxLocator) {
	if service == nil || service.publishOutboxWakeup == nil || strings.TrimSpace(locator.MessageID) == "" {
		return
	}
	workspace, err := principalmodel.NewWorkspaceID(locator.WorkspaceID)
	if err != nil {
		return
	}
	service.publishOutboxWakeup(workerplatform.DurableTaskLocator{QueueKind: "integration_outbox", WorkspaceID: workspace.String(), TaskID: strings.TrimSpace(locator.MessageID)})
}

func (s *IntegrationApplicationService) wakeIntegrationOutbox(message integrationmodel.IntegrationOutboxMessage) {
	if s == nil || strings.TrimSpace(message.Status) != "queued" {
		return
	}
	WakeIntegrationOutbox(s, IntegrationOutboxLocator{WorkspaceID: message.WorkspaceID, MessageID: message.ID})
}

func (s *IntegrationApplicationService) ProcessIntegrationOutbox(ctx context.Context, locator IntegrationOutboxLocator) (OutboxProcessBatchResult, error) {
	if s == nil || s.deliveryRepo == nil || s.workerRepo == nil {
		return OutboxProcessBatchResult{}, apperror.New(apperror.KindUnavailable, "backend.integration.outbox.worker_unavailable", nil, nil)
	}
	workspace, err := principalmodel.NewWorkspaceID(locator.WorkspaceID)
	if err != nil || strings.TrimSpace(locator.MessageID) == "" {
		return OutboxProcessBatchResult{}, apperror.New(apperror.KindBadRequest, "backend.integration.outbox.locator_invalid", err, nil)
	}
	reader, ok := s.deliveryRepo.(integrationrepository.IntegrationOutboxReader)
	if !ok {
		return OutboxProcessBatchResult{}, apperror.New(apperror.KindUnavailable, "backend.integration.outbox.reader_unavailable", nil, nil)
	}
	message, found, err := reader.GetOutbox(ctx, workspace.String(), strings.TrimSpace(locator.MessageID))
	if err != nil {
		return OutboxProcessBatchResult{}, err
	}
	if !found {
		return OutboxProcessBatchResult{}, nil
	}
	processed, bucket := s.processDueOutboxMessage(ctx, message, integrationruntime.IntegrationWorkerPrincipal(workspace.String()))
	result := OutboxProcessBatchResult{Messages: []integrationmodel.IntegrationOutboxMessage{processed}}
	observeOutboxBatchBucket(&result, bucket)
	return result, nil
}

func (s *IntegrationApplicationService) processOutboxWorkerLocator(ctx context.Context, locator IntegrationOutboxLocator) bool {
	result, err := s.ProcessIntegrationOutbox(ctx, locator)
	if err != nil {
		logging.FromContext(ctx).Error("integration outbox worker failed", zap.String("error_code", stableIntegrationFailureCode(err, "backend.integration.outbox.worker_failed")))
		return false
	}
	if result.Sent+result.Retried+result.DeadLettered+result.ReconciliationRequired > 0 {
		logging.FromContext(ctx).Info("integration outbox worker completed", zap.String("message_id", locator.MessageID), zap.Int("sent", result.Sent), zap.Int("retried", result.Retried), zap.Int("dead_lettered", result.DeadLettered), zap.Int("reconciliation_required", result.ReconciliationRequired))
	}
	return result.Sent+result.Retried+result.DeadLettered+result.ReconciliationRequired+result.Skipped > 0
}

func (s *IntegrationApplicationService) processOutboxWorkerTick(ctx context.Context, limit int) bool {
	result, err := s.ProcessDueIntegrationOutbox(ctx, limit, integrationruntime.IntegrationWorkerPrincipal(principalmodel.InstallationWorkspaceID))
	if err != nil {
		logging.FromContext(ctx).Error("integration outbox worker failed", zap.String("error_code", stableIntegrationFailureCode(err, "backend.integration.outbox.worker_failed")))
		return false
	}
	if result.Sent+result.Retried+result.DeadLettered+result.ReconciliationRequired+result.Skipped > 0 {
		logging.FromContext(ctx).Info("integration outbox worker completed", zap.Int("sent", result.Sent), zap.Int("retried", result.Retried), zap.Int("dead_lettered", result.DeadLettered), zap.Int("reconciliation_required", result.ReconciliationRequired), zap.Int("skipped", result.Skipped))
	}
	return result.Sent+result.Retried+result.DeadLettered+result.ReconciliationRequired+result.Skipped > 0
}

func (s *IntegrationApplicationService) recoverIntegrationOutboxLocators(ctx context.Context, limit int) ([]workerplatform.DurableTaskLocator, error) {
	messages, err := s.listDueIntegrationOutbox(ctx, limit)
	if err != nil {
		return nil, err
	}
	locators := make([]workerplatform.DurableTaskLocator, 0, len(messages))
	for _, message := range messages {
		locators = append(locators, workerplatform.DurableTaskLocator{QueueKind: "integration_outbox", WorkspaceID: message.WorkspaceID, TaskID: message.ID})
	}
	return locators, nil
}
