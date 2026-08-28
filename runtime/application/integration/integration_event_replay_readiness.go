package integration

import (
	"context"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *IntegrationApplicationService) validateIntegrationEventReplayReadiness(ctx context.Context, event integrationmodel.IntegrationEvent, principal principalmodel.Principal) error {
	if strings.TrimSpace(event.WorkspaceID) != principalWorkspaceID(principal) {
		return forbidden("auth.permission_denied")
	}
	_, contextDeclared := event.Payload[EventContextKey]
	connection, executionEvent, connectionReady := s.EventExecutionContext(ctx, event)
	if contextDeclared && !connectionReady {
		return badRequest("backend.integration.event.replay_connection_unavailable")
	}
	if connectionReady {
		if !connectionCanSend(connection) {
			return badRequest("backend.integration.event.replay_connection_unavailable")
		}
		if err := s.ValidateAdapterConfig(connection); err != nil {
			return err
		}
		if _, err := s.ResolveAdapterSecrets(ctx, connection); err != nil {
			return err
		}
	}
	if _, ok := s.IntegrationEventHandler(event.Provider); ok {
		return nil
	}
	_, mapped, err := s.PlanEventMappingExecution(executionEvent)
	if err != nil {
		return err
	}
	if !mapped {
		return badRequest("backend.integration.event.replay_handler_not_ready")
	}
	return nil
}
