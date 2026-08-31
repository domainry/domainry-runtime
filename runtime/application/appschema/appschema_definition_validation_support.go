package appschema

import (
	"context"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemavalidation "github.com/domainry/domainry-runtime/runtime/domain/appschema/validation"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

func newApplicationDefinitionValidationIssue(code, fieldPath, stepKey, operationKey string, params map[string]string) appschemamodel.ApplicationDefinitionValidationIssue {
	return appschemavalidation.NewApplicationDefinitionValidationIssue(code, fieldPath, stepKey, operationKey, params)
}

func firstApplicationDefinitionIssueError(issues []appschemamodel.ApplicationDefinitionValidationIssue) error {
	return appschemavalidation.ApplicationSchemaFirstDefinitionIssueError(issues)
}

func stringSet(values []string) map[string]bool {
	return appschemavalidation.ApplicationSchemaStringSet(values)
}

func normalizedDefinitionValue(value any) string {
	return appschemavalidation.ApplicationSchemaNormalizedDefinitionValue(value)
}

func (s *ApplicationSchemaApplicationService) validateReportDefinitionIssues(ctx context.Context, report reportmodel.ReportSchema) []appschemamodel.ApplicationDefinitionValidationIssue {
	return appschemavalidation.ApplicationSchemaValidateReportDefinitionIssues(ctx, principalmodel.InstallationWorkspaceID, s.runtime.Schema(), s.records, report)
}

func validateReportDefinitionForSnapshot(ctx context.Context, snapshot appschemamodel.ApplicationSchemaSnapshot, records recordrepository.RecordRepository, report reportmodel.ReportSchema) error {
	return appschemavalidation.ApplicationSchemaValidateReportDefinition(ctx, principalmodel.InstallationWorkspaceID, snapshot, records, report)
}

func (s *ApplicationSchemaApplicationService) ValidateAutomationRuleDefinition(ctx context.Context, rule automationmodel.AutomationRuleSchema) error {
	return (automationapplication.AutomationDefinitionValidationApplicationService{
		Catalog: func() automationvalidation.AutomationDefinitionCatalog {
			snapshot := s.runtime.Schema()
			return automationvalidation.AutomationDefinitionCatalog{Objects: snapshot.Objects, Actions: snapshot.Actions, Workflows: snapshot.Workflows, Connectors: snapshot.Integrations.Connectors}
		},
		ListConnections: func(ctx context.Context, scope string) ([]integrationmodel.IntegrationConnection, error) {
			if s.integrations == nil {
				return []integrationmodel.IntegrationConnection{}, nil
			}
			return s.integrations.ListConnections(ctx, scope)
		},
	}).Validate(ctx, rule)
}

func validateAutomationConditionGroup(group automationmodel.AutomationConditionGroup, fieldPath string, depth int) error {
	return automationvalidation.AutomationValidateConditionGroup(group, fieldPath, depth)
}

func validateAutomationTriggerFilters(trigger automationmodel.AutomationTriggerSchema, object definitionmodel.ObjectSchema) error {
	return automationvalidation.AutomationValidateTriggerFilters(trigger, object)
}

func automationConnectorOperation(connector integrationmodel.ConnectorSchema, operationKey string) *integrationmodel.ConnectorOperationSchema {
	return automationvalidation.AutomationConnectorOperation(connector, operationKey)
}

func validateAutomationOperationInput(operation integrationmodel.ConnectorOperationSchema, input map[string]any, object definitionmodel.ObjectSchema, outputs map[string]map[string]string) error {
	return automationvalidation.AutomationValidateOperationInput(operation, input, object, outputs)
}

func automationMappingValueType(value any, object definitionmodel.ObjectSchema, outputs map[string]map[string]string) (string, bool) {
	return automationvalidation.AutomationMappingValueType(value, object, outputs)
}

func automationProtocolTypesCompatible(sourceType, targetType string) bool {
	return automationvalidation.AutomationProtocolTypesCompatible(sourceType, targetType)
}

func validateAutomationInstructionReferences(action automationmodel.AutomationInstructionSchema, outputs map[string]map[string]string) error {
	return automationvalidation.AutomationValidateInstructionReferences(action, outputs)
}
