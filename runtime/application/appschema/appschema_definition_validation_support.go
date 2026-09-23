package appschema

import (
	"context"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
)

func (s *ApplicationSchemaApplicationService) ValidateAutomationRuleDefinition(ctx context.Context, rule automationmodel.AutomationRuleSchema) error {
	return (automationapplication.AutomationDefinitionValidationApplicationService{
		Catalog: func() automationvalidation.AutomationDefinitionCatalog {
			snapshot := s.runtime.Schema()
			return automationvalidation.AutomationDefinitionCatalog{Objects: snapshot.Objects, Actions: snapshot.Actions, Workflows: snapshot.Workflows, Connectors: snapshot.Integrations.Connectors}
		},
		ListConnections: func(ctx context.Context, scope string) ([]integrationsdk.Connection, error) {
			if s.integrations == nil {
				return []integrationsdk.Connection{}, nil
			}
			return s.integrations.ListConnections(ctx, scope)
		},
	}).Validate(ctx, rule)
}
