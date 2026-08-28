package deployment

import (
	"context"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *DeploymentRuntimeStatusApplicationService) Health(ctx context.Context) map[string]any {
	storageStatus, storageErr := s.storageStatus(ctx)
	migrationStatus, migrationErr := s.migrationStatus(ctx)
	schedulerScope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "collect scheduler health status")
	schedulerStatus, schedulerErr := s.scheduler.Status(ctx, schedulerScope)
	lifecycleStatus := map[string]any{}
	var lifecycleErr error
	if s.lifecycleHealth != nil {
		lifecycleStatus, lifecycleErr = s.lifecycleHealth.HealthForSystem(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "collect lifecycle health status"), time.Now().UTC())
	}
	if schedulerStatus == nil {
		schedulerStatus = map[string]any{}
	}
	status := "ok"
	checks := map[string]string{
		"scheduler": "ok",
		"storage":   "ok",
		"migration": "ok",
		"lifecycle": "ok",
	}
	warnings := map[string]any{}
	if storageErr != nil {
		status = "degraded"
		checks["storage"] = "error"
	}
	if migrationErr != nil {
		status = "degraded"
		checks["migration"] = "error"
	} else if !migrationStatus.Current {
		status = "degraded"
		checks["migration"] = "outdated"
	}
	if schedulerErr != nil {
		status = "degraded"
		checks["scheduler"] = "error"
	}
	if lifecycleErr != nil {
		status = "degraded"
		checks["lifecycle"] = "error"
	} else if warning, _ := lifecycleStatus["warning"].(bool); warning {
		status = "degraded"
		checks["lifecycle"] = "warning"
		warnings["lifecycle"] = lifecycleStatus
	}
	if schedulerWarnings := schedulerHealthWarnings(schedulerStatus); schedulerErr == nil && len(schedulerWarnings) > 0 {
		status = "degraded"
		checks["scheduler"] = "warning"
		warnings["scheduler"] = schedulerWarnings
	}
	payload := map[string]any{
		"status":           status,
		"template_id":      s.templateID,
		"template_version": s.templateVersion,
		"checks":           checks,
		"storage":          storageStatus,
		"migration":        migrationStatus,
		"scheduler":        schedulerStatus,
		"lifecycle":        lifecycleStatus,
	}
	if len(warnings) > 0 {
		payload["warnings"] = warnings
	}
	return payload
}

func schedulerHealthWarnings(status map[string]any) map[string]any {
	warnings := map[string]any{}
	if !boolMetric(status["runtime_available"]) {
		return warnings
	}
	if unresolved := intMetric(status["unresolved_dead_letters"]); unresolved > 0 {
		warnings["unresolved_dead_letters"] = unresolved
	}
	if expired := intMetric(status["lease_expirations"]); expired > 0 {
		warnings["stale_leased_runs"] = expired
	}
	return warnings
}

func boolMetric(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	default:
		return false
	}
}

func intMetric(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func (s *DeploymentRuntimeStatusApplicationService) Metrics(ctx context.Context) map[string]any {
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
		"template_id":      s.templateID,
		"template_version": s.templateVersion,
		"objects":          len(snapshot.Objects),
		"storage":          storageStatus,
		"migration":        migrationStatus,
		"workflows":        workflowMetrics,
		"scheduler":        schedulerStatus,
		"audit":            auditMetrics,
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
	if len(errors) > 0 {
		payload["errors"] = errors
	}
	return payload
}
