package automation

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AutomationDefinitionValidationApplicationService struct {
	Catalog         func() automationvalidation.AutomationDefinitionCatalog
	ListConnections func(context.Context, string) ([]integrationmodel.IntegrationConnection, error)
}

func (s AutomationDefinitionValidationApplicationService) Validate(ctx context.Context, rule automationmodel.AutomationRuleSchema) error {
	catalog := automationvalidation.AutomationDefinitionCatalog{}
	if s.Catalog != nil {
		catalog = s.Catalog()
	}
	if s.ListConnections != nil {
		connections, err := s.ListConnections(ctx, principalmodel.InstallationWorkspaceID)
		if err != nil {
			return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": "list automation connections"}, Err: err}
		}
		catalog.Connections = connections
	}
	return (automationvalidation.AutomationDefinitionValidator{Catalog: catalog}).Validate(ctx, rule)
}
