package integration

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/logging"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"go.uber.org/zap"
)

const EventContextKey = "_integration_context"

func (s *IntegrationApplicationService) EventExecutionContext(ctx context.Context, event integrationmodel.IntegrationEvent) (integrationmodel.IntegrationConnection, integrationmodel.IntegrationEvent, bool) {
	executionEvent := event
	executionEvent.Payload = cloneMap(event.Payload)
	raw, _ := executionEvent.Payload[EventContextKey].(map[string]any)
	delete(executionEvent.Payload, EventContextKey)
	connectionKey, _ := raw["connection_key"].(string)
	connectorKey, _ := raw["connector_key"].(string)
	providerKey, _ := raw["provider_key"].(string)
	connectionKey, connectorKey, providerKey = strings.TrimSpace(connectionKey), strings.TrimSpace(connectorKey), strings.TrimSpace(providerKey)
	if connectionKey == "" || connectorKey == "" || providerKey == "" {
		return integrationmodel.IntegrationConnection{}, executionEvent, false
	}
	connection, ok := s.LookupConnection(ctx, connectionKey, event.WorkspaceID)
	if !ok || connection.ConnectorKey != connectorKey || connection.ProviderKey != providerKey {
		return integrationmodel.IntegrationConnection{}, executionEvent, false
	}
	executionEvent.Payload[eventRoutingConnectionKey] = connectionKey
	return connection, executionEvent, true
}

type IntegrationEventLocator struct {
	WorkspaceID string
	EventID     string
}

func IntegrationEventWakeups(service *IntegrationApplicationService) <-chan IntegrationEventLocator {
	if service == nil {
		return nil
	}
	return service.eventWakeups
}

func WakeIntegrationEvent(service *IntegrationApplicationService, locator IntegrationEventLocator) {
	if service == nil || service.eventWakeups == nil || strings.TrimSpace(locator.EventID) == "" {
		return
	}
	workspace, err := principalmodel.NewWorkspaceID(locator.WorkspaceID)
	if err != nil {
		return
	}
	select {
	case service.eventWakeups <- IntegrationEventLocator{WorkspaceID: workspace.String(), EventID: strings.TrimSpace(locator.EventID)}:
	default:
		// Wakeups are bounded acceleration hints. Durable recovery owns progress.
	}
}

func (s *IntegrationApplicationService) wakeIntegrationEvent(event integrationmodel.IntegrationEvent) {
	if s == nil || strings.TrimSpace(event.Status) != "received" {
		return
	}
	WakeIntegrationEvent(s, IntegrationEventLocator{WorkspaceID: event.WorkspaceID, EventID: event.ID})
}

func (s *IntegrationApplicationService) ProcessIntegrationEvent(ctx context.Context, locator IntegrationEventLocator) (EventProcessBatchResult, error) {
	if s == nil || s.eventRepo == nil || s.publicationWorkerRepo == nil {
		return EventProcessBatchResult{}, apperror.New(apperror.KindUnavailable, "backend.integration.event.worker_unavailable", nil, nil)
	}
	workspace, err := principalmodel.NewWorkspaceID(locator.WorkspaceID)
	if err != nil || strings.TrimSpace(locator.EventID) == "" {
		return EventProcessBatchResult{}, apperror.New(apperror.KindBadRequest, "backend.integration.event.locator_invalid", err, nil)
	}
	event, found, err := s.eventRepo.GetEvent(ctx, workspace.String(), strings.TrimSpace(locator.EventID))
	if err != nil {
		return EventProcessBatchResult{}, err
	}
	if !found || len(s.filterProcessableEvents([]integrationmodel.IntegrationEvent{event})) == 0 {
		return EventProcessBatchResult{}, nil
	}
	processed, bucket := s.processDueEvent(ctx, event, integrationWorkerPrincipalForWorkspace(workspace.String()))
	result := EventProcessBatchResult{Events: []integrationmodel.IntegrationEvent{processed}}
	switch bucket {
	case "processed":
		result.Processed = 1
	case "retried":
		result.Retried = 1
	case "dead_lettered":
		result.DeadLettered = 1
	default:
		result.Skipped = 1
	}
	return result, nil
}

func (s *IntegrationApplicationService) processEventWorkerLocator(ctx context.Context, locator IntegrationEventLocator) bool {
	result, err := s.ProcessIntegrationEvent(ctx, locator)
	if err != nil {
		logging.FromContext(ctx).Error("integration event worker failed", zap.String("error_code", stableIntegrationFailureCode(err, "backend.integration.event.worker_failed")))
		return false
	}
	if result.Processed+result.Retried+result.DeadLettered+result.Skipped > 0 {
		logging.FromContext(ctx).Info("integration event worker completed", zap.Int("processed", result.Processed), zap.Int("retried", result.Retried), zap.Int("dead_lettered", result.DeadLettered), zap.Int("skipped", result.Skipped))
	}
	return result.Processed+result.Retried+result.DeadLettered+result.Skipped > 0
}

func integrationWorkerPrincipalForWorkspace(workspaceID string) principalmodel.Principal {
	return integrationruntime.IntegrationWorkerPrincipal(workspaceID)
}
