package metadata

import (
	"context"

	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatavalidation "github.com/domainry/domainry-runtime/runtime/domain/metadata/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func newMetadataDefinitionValidationIssue(code, fieldPath, stepKey, operationKey string, params map[string]string) metadatamodel.MetadataDefinitionValidationIssue {
	return metadatavalidation.NewMetadataDefinitionValidationIssue(code, fieldPath, stepKey, operationKey, params)
}

func firstMetadataDefinitionIssueError(issues []metadatamodel.MetadataDefinitionValidationIssue) error {
	return metadatavalidation.MetadataFirstDefinitionIssueError(issues)
}

func stringSet(values []string) map[string]bool { return metadatavalidation.MetadataStringSet(values) }

func normalizedDefinitionValue(value any) string {
	return metadatavalidation.MetadataNormalizedDefinitionValue(value)
}

func (s *ApplicationSchemaService) validateReportDefinitionIssues(ctx context.Context, report reportmodel.ReportSchema) []metadatamodel.MetadataDefinitionValidationIssue {
	return metadatavalidation.MetadataValidateReportDefinitionIssues(ctx, principalmodel.InstallationWorkspaceID, s.runtime.Schema(), s.records, report)
}

func validateReportDefinitionForSnapshot(ctx context.Context, snapshot metadatamodel.ApplicationSchemaSnapshot, records recordrepository.RecordRepository, report reportmodel.ReportSchema) error {
	return metadatavalidation.MetadataValidateReportDefinition(ctx, principalmodel.InstallationWorkspaceID, snapshot, records, report)
}

func (s *ApplicationSchemaService) ValidateAutomationRuleDefinition(ctx context.Context, rule automationmodel.AutomationRuleSchema) error {
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
