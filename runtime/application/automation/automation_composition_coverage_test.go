package automation

import (
	"context"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type automationDeliveryRepositoryProbe struct {
	integrationrepository.IntegrationDeliveryRepository
}

func (automationDeliveryRepositoryProbe) ListInvocations(context.Context, string, string, string, string, string, int) ([]integrationmodel.IntegrationInvocation, error) {
	return []integrationmodel.IntegrationInvocation{{ID: "invocation-1"}}, nil
}

func (automationDeliveryRepositoryProbe) ListOutbox(context.Context, string, string, string, int) ([]integrationmodel.IntegrationOutboxMessage, error) {
	return []integrationmodel.IntegrationOutboxMessage{{ID: "outbox-1"}}, nil
}

type automationConfigRepositoryProbe struct {
	integrationrepository.IntegrationConfigRepository
}

func (automationConfigRepositoryProbe) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	return []integrationmodel.IntegrationConnection{{Key: "connection-1"}}, nil
}

func TestAutomationCompositionRepositoriesForwardToManagement(t *testing.T) {
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{}}
	service := NewAutomationApplicationService(AutomationApplicationDependencies{
		Rules: registry, DeliveryRepository: automationDeliveryRepositoryProbe{},
		Schema: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
			return appschemamodel.ApplicationSchemaSnapshot{Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "email"}}}}
		},
	})
	principal := automationFacadePrincipal()
	if catalog, err := service.AutomationCapabilities(t.Context(), principal); err != nil || len(catalog.Connections) != 0 || len(catalog.Connectors) != 1 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	if history, err := service.AutomationExecutions(t.Context(), automationmodel.AutomationExecutionFilter{}, principal); err != nil || history.Count != 0 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}
