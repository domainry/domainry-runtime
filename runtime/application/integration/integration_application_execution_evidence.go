package integration

import (
	"context"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
)

type ExecutionEvidence struct {
	Connection  integrationmodel.IntegrationConnection
	Operation   string
	Status      string
	StartedAt   time.Time
	RequestRef  string
	ResponseRef string
	Error       string
	EventID     string
	Request     map[string]any
	Response    map[string]any
	Source      string
	SourceID    string
	ErrorCode   string
}

func (s *IntegrationApplicationService) RecordIntegrationExecutionEvidence(ctx context.Context, evidence ExecutionEvidence, principal principalmodel.Principal) (integrationmodel.IntegrationInvocation, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	status := valueOrDefault(strings.TrimSpace(evidence.Status), "succeeded")
	duration := time.Since(evidence.StartedAt).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	metadata := integrationpolicy.RedactSensitiveMap(map[string]any{
		"source": evidence.Source, "source_id": evidence.SourceID,
		"actor_id": principal.UserID, "role_key": principal.RoleKey,
		"request": cloneMap(evidence.Request), "response": cloneMap(evidence.Response),
		"provider_error_code": evidence.ErrorCode,
	})
	if principal.RequestID != "" {
		metadata["request_id"] = principal.RequestID
	}
	invocation := integrationmodel.IntegrationInvocation{
		WorkspaceID:  valueOrDefault(strings.TrimSpace(evidence.Connection.WorkspaceID), principalWorkspaceID(principal)),
		ConnectorKey: evidence.Connection.ConnectorKey, ProviderKey: evidence.Connection.ProviderKey,
		ConnectionKey: evidence.Connection.Key, Operation: strings.TrimSpace(evidence.Operation), Status: status,
		DurationMS: duration, RequestRef: strings.TrimSpace(evidence.RequestRef), ResponseRef: strings.TrimSpace(evidence.ResponseRef),
		Error: strings.TrimSpace(evidence.Error), EventID: strings.TrimSpace(evidence.EventID), Metadata: metadata,
	}
	saved, err := s.invocationRepo.InsertInvocation(ctx, invocation.WorkspaceID, invocation)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	s.audit(ctx, "integration_invocation_recorded", "integration_invocation", saved.ID, principal, "Recorded "+evidence.Source+" integration invocation "+saved.Operation, nil, integrationprojection.IntegrationInvocationAuditShape(saved), map[string]any{
		"workspace_id": saved.WorkspaceID, "connector_key": saved.ConnectorKey, "provider_key": saved.ProviderKey,
		"connection_key": saved.ConnectionKey, "operation": saved.Operation, "status": saved.Status,
		"event_id": saved.EventID, "source": evidence.Source, "source_id": evidence.SourceID,
	})
	return saved, nil
}
