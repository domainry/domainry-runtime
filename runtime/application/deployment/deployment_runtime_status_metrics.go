package deployment

import (
	"context"
	"sort"
	"time"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentprojection "github.com/domainry/domainry-runtime/runtime/domain/deployment/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func (s *DeploymentRuntimeStatusApplicationService) workflowMetrics(ctx context.Context) (map[string]any, error) {
	configured := len(s.schema.SchemaForPrincipal(ctx, principalmodel.Principal{}).Workflows)
	executions, err := s.workflow.ListExecutions(ctx, principalmodel.InstallationWorkspaceID, 500)
	if err != nil {
		return deploymentprojection.DeploymentWorkflowMetrics(configured, nil, time.Now().UTC()), err
	}
	return deploymentprojection.DeploymentWorkflowMetrics(configured, executions, time.Now().UTC()), nil
}

func (s *DeploymentRuntimeStatusApplicationService) businessActionMetrics(ctx context.Context) (map[string]any, error) {
	const limit = 200
	actions := s.schema.SchemaForPrincipal(ctx, principalmodel.Principal{}).Actions
	configured := deploymentprojection.DeploymentBusinessActionConfiguredCount(actions)
	invocations, err := s.delivery.ListInvocations(ctx, principalmodel.InstallationWorkspaceID, "", "", "", "", limit)
	if err != nil {
		return deploymentprojection.DeploymentBusinessActionMetrics(configured, nil, limit), err
	}
	return deploymentprojection.DeploymentBusinessActionMetrics(configured, invocations, limit), nil
}

func (s *DeploymentRuntimeStatusApplicationService) auditMetrics(ctx context.Context) (map[string]any, error) {
	const limit = 1000
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "collect deployment audit metrics")
	events, err := s.audit.ListAuditEventsForSystem(ctx, scope, auditmodel.AuditEventQuery{Limit: limit})
	if err != nil {
		return deploymentprojection.DeploymentAuditMetrics(nil, limit), err
	}
	return deploymentprojection.DeploymentAuditMetrics(events, limit), nil
}

func (s *DeploymentRuntimeStatusApplicationService) businessRecordCounts(ctx context.Context) (map[string]int, error) {
	counts := map[string]int{}
	var firstErr error
	schemaObjects := append([]definitionmodel.ObjectSchema(nil), s.schema.SchemaForPrincipal(ctx, principalmodel.Principal{}).Objects...)
	sort.Slice(schemaObjects, func(i, j int) bool { return schemaObjects[i].Key < schemaObjects[j].Key })
	for _, object := range schemaObjects {
		page, err := s.records.ListRecords(ctx, principalmodel.InstallationWorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 1})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		counts[object.Key] = page.Total
	}
	return counts, firstErr
}
