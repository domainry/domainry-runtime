package automation

import (
	"context"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	publicationrepository "github.com/domainry/domainry-runtime/runtime/domain/publication/repository"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type automationDeliveryRepositoryProbe struct {
	publicationrepository.Repository
}

func (automationDeliveryRepositoryProbe) ListOutbox(context.Context, string, string, string, int) ([]publicationmodel.Message, error) {
	return []publicationmodel.Message{{ID: "outbox-1"}}, nil
}

func TestAutomationCompositionRepositoriesForwardToManagement(t *testing.T) {
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{}}
	service := NewAutomationApplicationService(AutomationApplicationDependencies{
		Rules: registry, DeliveryRepository: automationDeliveryRepositoryProbe{},
		Schema: func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
			return appschemamodel.ApplicationSchemaSnapshot{Integrations: connectormodel.IntegrationSchema{Connectors: []connectormodel.ConnectorSchema{{Key: "email"}}}}
		},
	})
	principal := automationFacadePrincipal()
	if catalog, err := service.AutomationExecutionCatalog(t.Context(), principal); err != nil || len(catalog.Connections) != 0 || len(catalog.Connectors) != 1 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	if history, err := service.AutomationExecutions(t.Context(), automationmodel.AutomationExecutionFilter{}, principal); err != nil || history.Count != 0 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}
