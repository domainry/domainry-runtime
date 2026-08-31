package businesssystem

import appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
import deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"

// This file projects Runtime execution state into the business-system snapshot.

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	reportmodel "github.com/domainry/domainry-report-sdk/model"

	"context"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

// businessRuntimeProjectionPorts contains only the cross-owner reads needed to
// project live Runtime state. BusinessSystem remains the coordinator while the
// source owners retain their concrete application services.
type businessRuntimeProjectionPorts struct {
	workflowProcesses      func(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error)
	automationRules        func(context.Context, principalmodel.Principal) ([]automationmodel.AutomationRuleSchema, error)
	automationExecutions   func(context.Context, automationmodel.AutomationExecutionFilter, principalmodel.Principal) (automationprojection.AutomationExecutionHistory, error)
	connectorCatalog       func(context.Context, principalmodel.Principal) ([]integrationmodel.ConnectorSchema, error)
	integrationConnections func(context.Context, principalmodel.Principal) ([]integrationmodel.IntegrationConnection, error)
	integrationOutbox      func(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error)
	schedulerDefinitions   func(context.Context, principalmodel.Principal) ([]recordmodel.Record, error)
	schemaForPrincipal     func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
	schemaObjectMap        func(context.Context) map[string]definitionmodel.ObjectSchema
	listRecords            func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
	idempotencyStatus      func(context.Context, string) (deploymentmodel.IdempotencyOperationalStatus, error)
}

type BusinessSystemRuntimeProjectionDependencies struct {
	WorkflowProcesses      func(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error)
	AutomationRules        func(context.Context, principalmodel.Principal) ([]automationmodel.AutomationRuleSchema, error)
	AutomationExecutions   func(context.Context, automationmodel.AutomationExecutionFilter, principalmodel.Principal) (automationprojection.AutomationExecutionHistory, error)
	ConnectorCatalog       func(context.Context, principalmodel.Principal) ([]integrationmodel.ConnectorSchema, error)
	IntegrationConnections func(context.Context, principalmodel.Principal) ([]integrationmodel.IntegrationConnection, error)
	IntegrationOutbox      func(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error)
	SchedulerDefinitions   func(context.Context, principalmodel.Principal) ([]recordmodel.Record, error)
	SchemaForPrincipal     func(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
	SchemaObjectMap        func(context.Context) map[string]definitionmodel.ObjectSchema
	ListRecords            func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
	IdempotencyStatus      func(context.Context, string) (deploymentmodel.IdempotencyOperationalStatus, error)
}

func newBusinessSystemRuntimeProjectionPorts(dependencies BusinessSystemRuntimeProjectionDependencies) businessRuntimeProjectionPorts {
	return businessRuntimeProjectionPorts{
		workflowProcesses: dependencies.WorkflowProcesses, automationRules: dependencies.AutomationRules,
		automationExecutions: dependencies.AutomationExecutions, connectorCatalog: dependencies.ConnectorCatalog,
		integrationConnections: dependencies.IntegrationConnections, integrationOutbox: dependencies.IntegrationOutbox,
		schedulerDefinitions: dependencies.SchedulerDefinitions,
		schemaForPrincipal:   dependencies.SchemaForPrincipal, schemaObjectMap: dependencies.SchemaObjectMap, listRecords: dependencies.ListRecords, idempotencyStatus: dependencies.IdempotencyStatus,
	}
}

func (s *BusinessSystemApplicationService) RuntimeStateSnapshot(ctx context.Context, principal principalmodel.Principal) (changeplanprojection.BusinessRuntimeStateSnapshot, error) {
	processes, err := s.runtimeProjection.workflowProcesses(ctx, principal, workflowmodel.WorkflowProcessFilter{Limit: 200})
	if err != nil {
		return changeplanprojection.BusinessRuntimeStateSnapshot{}, err
	}
	rules, err := s.runtimeProjection.automationRules(ctx, principal)
	if err != nil {
		return changeplanprojection.BusinessRuntimeStateSnapshot{}, err
	}
	automation, err := s.runtimeProjection.automationExecutions(ctx, automationmodel.AutomationExecutionFilter{Limit: 100}, principal)
	if err != nil {
		return changeplanprojection.BusinessRuntimeStateSnapshot{}, err
	}
	connectors, err := s.runtimeProjection.connectorCatalog(ctx, principal)
	if err != nil {
		return changeplanprojection.BusinessRuntimeStateSnapshot{}, err
	}
	connections, err := s.runtimeProjection.integrationConnections(ctx, principal)
	if err != nil {
		return changeplanprojection.BusinessRuntimeStateSnapshot{}, err
	}
	outbox, err := s.runtimeProjection.integrationOutbox(ctx, "", "", 100, principal)
	if err != nil {
		return changeplanprojection.BusinessRuntimeStateSnapshot{}, err
	}
	scheduler, err := s.schedulerStateSnapshot(ctx, principal)
	if err != nil {
		return changeplanprojection.BusinessRuntimeStateSnapshot{}, err
	}
	idempotencyStatus := deploymentmodel.IdempotencyOperationalStatus{Backlog: map[string]int{}}
	if s.runtimeProjection.idempotencyStatus != nil {
		idempotencyStatus, err = s.runtimeProjection.idempotencyStatus(ctx, businessSystemPrincipalWorkspaceID(principal))
		if err != nil {
			return changeplanprojection.BusinessRuntimeStateSnapshot{}, err
		}
	}
	return changeplanprojection.BusinessRuntimeStateSnapshot{
		RunningWorkflowProcesses: changeplanprojection.ProjectWorkflowProcesses(processes), AutomationRules: rules,
		RecentAutomationRuns: automation.Items, Scheduler: scheduler, Reports: append([]reportmodel.ReportSchema(nil), s.runtimeProjection.schemaForPrincipal(ctx, principal).Reports...),
		Connectors: connectors, Connections: changeplanprojection.ProjectIntegrationConnections(connections, func(connection integrationmodel.IntegrationConnection) bool {
			return connection.Status == "active" || connection.Status == "verified" || connection.Status == "degraded"
		}), RecentOutboxMessages: changeplanprojection.ProjectIntegrationOutbox(outbox),
		Idempotency: idempotencyStatus,
	}, nil
}

func (s *BusinessSystemApplicationService) schedulerStateSnapshot(ctx context.Context, principal principalmodel.Principal) (changeplanprojection.SchedulerStateSnapshot, error) {
	definitions := []recordmodel.Record{}
	if s.runtimeProjection.schedulerDefinitions != nil {
		var err error
		definitions, err = s.runtimeProjection.schedulerDefinitions(ctx, principal)
		if err != nil {
			return changeplanprojection.SchedulerStateSnapshot{}, err
		}
	}
	return changeplanprojection.SchedulerStateSnapshot{Definitions: definitions, RecentRuns: []recordmodel.Record{}, DeadLetters: []recordmodel.Record{}}, nil
}

func (s *BusinessSystemApplicationService) SnapshotObjectRecords(ctx context.Context, objectKey string, principal principalmodel.Principal, limit int) ([]recordmodel.Record, error) {
	_, exists := s.runtimeProjection.schemaObjectMap(ctx)[objectKey]
	if !exists {
		return []recordmodel.Record{}, nil
	}
	page, err := s.runtimeProjection.listRecords(ctx, objectKey, recordmodel.RecordListQuery{Page: 1, PageSize: limit, Sort: []recordmodel.RecordSortRule{{Field: "updated_at", Direction: "desc"}}}, principal)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (s *BusinessSystemApplicationService) RuntimeProjectionConfigured() bool {
	return s != nil && s.runtimeProjection.listRecords != nil
}
