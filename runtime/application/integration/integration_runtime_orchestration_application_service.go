package integration

import (
	"context"
	"strings"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *IntegrationApplicationService) IntegrationConnectionReferences(ctx context.Context, connectionKey string, principal principalmodel.Principal) ([]ConnectionReference, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if s.schema == nil {
		return nil, nil
	}
	snapshot := s.schema(ctx, principal)
	resources := make([]ConnectionReferenceResource, 0, len(snapshot.Actions)+len(snapshot.AutomationRules)+len(snapshot.Workflows)+len(snapshot.Integrations.EventMappings))
	for _, action := range snapshot.Actions {
		resources = append(resources, ConnectionReferenceResource{Kind: "action", Key: action.Key, Value: action})
	}
	for _, rule := range snapshot.AutomationRules {
		resources = append(resources, ConnectionReferenceResource{Kind: "automation", Key: rule.Key, Value: rule})
	}
	for _, workflow := range snapshot.Workflows {
		resources = append(resources, ConnectionReferenceResource{Kind: "workflow", Key: workflow.Key, Value: workflow})
	}
	for _, mapping := range snapshot.Integrations.EventMappings {
		resources = append(resources, ConnectionReferenceResource{Kind: "event_mapping", Key: mapping.Key, Value: mapping})
	}
	return FindConnectionReferences(ctx, resources, connectionKey)
}

func (s *IntegrationApplicationService) ResolveIntegrationDeliveryProvider(ctx context.Context, connectorKey, connectionKey, requestedProvider, workspaceID string) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if err := integrationAuthorizeWorkspaceQuery(workspaceID); err != nil {
		return "", err
	}
	connector, ok := s.ConnectorDefinition(connectorKey)
	if !ok {
		return "", notFound("backend.integration.connector.not_found")
	}
	providerKey := requestedProvider
	if connectionKey != "" {
		connection, exists := s.LookupConnection(ctx, connectionKey, workspaceID)
		if !exists || connection.ConnectorKey != connectorKey {
			return "", badRequest("backend.integration.binding.connection_connector_mismatch", "field_path", "connection_key", "connection", connectionKey, "expected", connectorKey)
		}
		providerKey = connection.ProviderKey
	}
	return ResolveConnectorProvider(connector, providerKey)
}

func (s *IntegrationApplicationService) RegisterSharedIntegrationOutboxSenders() {
	for _, connectorKey := range []string{"webhook", "http_webhook", "outbound_webhook", "email", "collaboration", "feishu_collaboration", "whatsapp", "file_storage", "sso", "notification"} {
		if s.ConnectorExists(connectorKey) {
			s.RegisterIntegrationOutboxSender(connectorKey, s)
		}
	}
}

func (s *IntegrationApplicationService) RegisterDefaultIntegrationOutboxSenders() {
	s.RegisterIntegrationOutboxSender("__automation__", s)
	for _, connectorKey := range []string{"webhook", "http_webhook", "outbound_webhook"} {
		if s.ConnectorExists(connectorKey) {
			s.RegisterIntegrationOutboxSender(connectorKey, s)
		}
	}
}

// RegisterProviderIntegrationOutboxSenders routes durable messages for every
// connector backed by a frozen provider adapter through the canonical Runtime
// delivery path. The worker still obtains work exclusively from committed
// Outbox rows; this registration only makes project-owned providers eligible
// after a row has been claimed.
func (s *IntegrationApplicationService) RegisterProviderIntegrationOutboxSenders() {
	if s == nil || s.registry == nil {
		return
	}
	for _, connector := range s.registry.Schema().Connectors {
		if s.registry.AdapterReady(connector) {
			s.RegisterIntegrationOutboxSender(connector.Key, s)
		}
	}
}

func (s *IntegrationApplicationService) ValidateAutomationOperationOutput(action automationmodel.AutomationInstructionSchema, output map[string]any) error {
	if s.automation == nil {
		return nil
	}
	return s.automation.ValidateIntegrationOutput(action, output)
}
