package deployment

import (
	"context"
	"time"

	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *DeploymentRuntimeStatusApplicationService) MonitoringSchedulerStatus(ctx context.Context) (map[string]any, error) {
	return s.scheduler.Status(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "collect scheduler monitoring status"))
}

func (s *DeploymentRuntimeStatusApplicationService) MonitoringLifecycleStatus(ctx context.Context) (map[string]any, error) {
	if s.lifecycleHealth == nil {
		return map[string]any{}, nil
	}
	return s.lifecycleHealth.Health(ctx, lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeGlobal, "collect lifecycle monitoring status"), time.Now().UTC())
}

// MonitoringMetricSections exposes owner-produced observations without the
// Monitoring envelope. The external Monitoring Module owns that envelope.
func (s *DeploymentRuntimeStatusApplicationService) MonitoringMetricSections(ctx context.Context) (map[string]any, map[string]string) {
	workflowMetrics, workflowErr := s.workflowMetrics(ctx)
	auditMetrics, auditErr := s.auditMetrics(ctx)
	recordCounts, recordErr := s.businessRecordCounts(ctx)
	actionMetrics, actionErr := s.businessActionMetrics(ctx)
	storageStatus, storageErr := s.storageStatus(ctx)
	migrationStatus, migrationErr := s.migrationStatus(ctx)
	idempotencyScope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "collect idempotency metrics status")
	idempotencyStatus, idempotencyStatusErr := s.IdempotencyOperationalStatusForSystem(ctx, idempotencyScope)
	snapshot := s.schema.SchemaForPrincipal(ctx, principalmodel.Principal{})
	schedulerScope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "collect scheduler metrics")
	schedulerStatus, schedulerErr := s.scheduler.Status(ctx, schedulerScope)
	if schedulerStatus == nil {
		schedulerStatus = map[string]any{}
	}
	payload := map[string]any{
		"objects":   len(snapshot.Objects),
		"storage":   storageStatus,
		"migration": migrationStatus,
		"workflows": workflowMetrics,
		"scheduler": schedulerStatus,
		"audit":     auditMetrics,
		"domain": map[string]any{
			"record_counts": recordCounts,
			"actions":       actionMetrics,
		},
	}
	if provider, ok := s.repository.(IdempotencyMetricsProvider); ok {
		payload["idempotency"] = provider.IdempotencyMetrics(ctx)
	}
	payload["idempotency_status"] = idempotencyStatus
	errors := map[string]string{}
	if workflowErr != nil {
		errors["workflow"] = workflowErr.Error()
	}
	if auditErr != nil {
		errors["audit"] = auditErr.Error()
	}
	if storageErr != nil {
		errors["storage"] = storageErr.Error()
	}
	if migrationErr != nil {
		errors["migration"] = migrationErr.Error()
	}
	if idempotencyStatusErr != nil {
		errors["idempotency"] = idempotencyStatusErr.Error()
	}
	if schedulerErr != nil {
		errors["scheduler"] = schedulerErr.Error()
	}
	if recordErr != nil {
		errors["domain"] = recordErr.Error()
	}
	if actionErr != nil {
		errors["business_actions"] = actionErr.Error()
	}
	return payload, errors
}
