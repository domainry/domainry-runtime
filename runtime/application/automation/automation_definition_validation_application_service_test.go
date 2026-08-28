package automation

import (
	"context"
	"errors"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestAutomationDefinitionValidationApplicationServiceWrapsConnectionCatalogFailure(t *testing.T) {
	want := errors.New("catalog unavailable")
	service := AutomationDefinitionValidationApplicationService{
		Catalog: func() automationvalidation.AutomationDefinitionCatalog {
			return automationvalidation.AutomationDefinitionCatalog{Objects: []definitionmodel.ObjectSchema{{Key: "order"}}}
		},
		ListConnections: func(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
			return nil, want
		},
	}
	err := service.Validate(t.Context(), automationmodel.AutomationRuleSchema{Key: "notify", Name: "Notify", ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "create"}})
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindInternal || appErr.Code != "backend.internal" || !errors.Is(err, want) {
		t.Fatalf("unexpected error: %#v", err)
	}
	if appErr.Params["operation"] != "list automation connections" {
		t.Fatalf("unexpected operation: %#v", appErr.Params)
	}
}
