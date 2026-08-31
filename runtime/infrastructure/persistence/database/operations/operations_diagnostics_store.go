package operations

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/domainry/domainry-foundation/pagination"
	"github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

var _ operationsrepository.OperationsDiagnosticsRepository = OperationsStore{}

func (s OperationsStore) OperationsDiagnosticsSnapshot(ctx context.Context, request operationsmodel.OperationsDiagnosticsRequest) (operationsmodel.OperationsDiagnosticsSnapshot, error) {
	if s.database() == nil {
		return operationsmodel.OperationsDiagnosticsSnapshot{}, fmt.Errorf("operations diagnostics unavailable")
	}
	snapshot := operationsmodel.OperationsDiagnosticsSnapshot{CapturedAt: request.Now.UTC(), Redacted: true, Page: request.Page, PageSize: request.PageSize, CostUnits: len(request.Sections) * request.PageSize, Sections: map[string]operationsmodel.OperationsDiagnosticsSection{}}
	for _, section := range request.Sections {
		switch section {
		case "schema_migration":
			snapshot.Sections[section] = s.operationsMigrationDiagnostics(ctx)
		case "db_pool":
			snapshot.Sections[section] = s.operationsPoolDiagnostics()
		case "worker_lease":
			snapshot.Sections[section] = s.operationsLeaseDiagnostics(ctx, request)
		case "queue_lag":
			snapshot.Sections[section] = s.operationsQueueDiagnostics(ctx, request)
		case "dlq":
			snapshot.Sections[section] = s.operationsDLQDiagnostics(ctx, request)
		case "backup_age":
			snapshot.Sections[section] = s.operationsBackupDiagnostics(request.Now)
		}
	}
	return snapshot, nil
}

func (s OperationsStore) operationsMigrationDiagnostics(ctx context.Context) operationsmodel.OperationsDiagnosticsSection {
	items := []map[string]any{}
	status := "ready"
	for _, table := range []string{"_schema_migrations"} {
		var total, dirty int64
		dirtyCount := query.Coalesce(query.Sum(query.CaseWhen(query.Equal("dirty", true), 1).Else(0)), query.Value(0))
		queryValue, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, table).Projections(query.Project(query.CountAll()), query.Project(dirtyCount)).Build()
		if buildErr != nil {
			return diagnosticFailure("backend.operations.diagnostics.migration_unavailable")
		}
		var dirtyValue sql.NullInt64
		if err := s.database().QueryRowContext(ctx, queryValue, args...).Scan(&total, &dirtyValue); err != nil {
			return diagnosticFailure("backend.operations.diagnostics.migration_unavailable")
		}
		dirty = dirtyValue.Int64
		if dirty > 0 {
			status = "blocked"
		}
		items = append(items, map[string]any{"ledger": table, "applied": total, "dirty": dirty})
	}
	readiness := s.databaseReadiness()
	return operationsmodel.OperationsDiagnosticsSection{Status: status, Summary: map[string]any{"migration_compatible": readiness.MigrationCompatible}, Items: items, RunbookURL: "/operations/runbooks/migration"}
}

func (s OperationsStore) operationsPoolDiagnostics() operationsmodel.OperationsDiagnosticsSection {
	stats, readiness := s.database().Stats(), s.databaseReadiness()
	status := "ready"
	if !readiness.Ready {
		status = "degraded"
	}
	return operationsmodel.OperationsDiagnosticsSection{Status: status, Summary: map[string]any{"driver": s.store.Driver(), "open_connections": stats.OpenConnections, "in_use": stats.InUse, "idle": stats.Idle, "max_open_connections": stats.MaxOpenConnections, "wait_count": stats.WaitCount, "pool_degraded": readiness.PoolDegraded}, RunbookURL: "/operations/runbooks/database"}
}

func (s OperationsStore) operationsLeaseDiagnostics(ctx context.Context, request operationsmodel.OperationsDiagnosticsRequest) operationsmodel.OperationsDiagnosticsSection {
	snapshot, err := s.OperationsLeaseSnapshot(ctx, request.InstanceID, request.Now)
	if err != nil {
		return diagnosticFailure("backend.operations.diagnostics.lease_unavailable")
	}
	page := pagination.NewNumbered(request.Page, request.PageSize, pagination.NumberedOptions{DefaultPageSize: 1})
	owners, window := pagination.SliceNumbered(page, snapshot.Owners)
	items := []map[string]any{}
	for _, owner := range owners {
		items = append(items, map[string]any{"owner": owner.Owner, "live": owner.Live, "expired": owner.Expired})
	}
	return operationsmodel.OperationsDiagnosticsSection{Status: "ready", Summary: map[string]any{"instance_id": request.InstanceID, "live": snapshot.Live, "expired": snapshot.Expired}, Items: items, NextPage: window.NextPage, RunbookURL: "/operations/runbooks/worker-lease"}
}

type operationsQueueSpec struct {
	owner, table, timestamp string
	states                  []any
}

func (s OperationsStore) operationsQueueDiagnostics(ctx context.Context, request operationsmodel.OperationsDiagnosticsRequest) operationsmodel.OperationsDiagnosticsSection {
	specs := []operationsQueueSpec{{"runtime_publication_outbox", "_publication_outbox", "created_at", []any{"queued", "sending", "failed"}}, {"workflow_execution", "_workflow_executions", "created_at", []any{"pending", "running", "failed"}}}
	items := []map[string]any{}
	for _, spec := range specs {
		count, oldest, err := s.operationsQueueCount(ctx, spec, request.WorkspaceID)
		if err != nil {
			return diagnosticFailure("backend.operations.diagnostics.queue_unavailable")
		}
		lag := ageSeconds(oldest, request.Now)
		items = append(items, map[string]any{"owner": spec.owner, "depth": count, "oldest_age_seconds": lag})
	}
	return operationsmodel.OperationsDiagnosticsSection{Status: "ready", Items: items, RunbookURL: "/operations/runbooks/queue-lag"}
}

func (s OperationsStore) operationsDLQDiagnostics(ctx context.Context, request operationsmodel.OperationsDiagnosticsRequest) operationsmodel.OperationsDiagnosticsSection {
	specs := []operationsQueueSpec{{"runtime_publication_outbox", "_publication_outbox", "updated_at", []any{"dead_letter", "quarantined"}}, {"workflow_execution", "_workflow_executions", "updated_at", []any{"dead_letter"}}}
	items := []map[string]any{}
	for _, spec := range specs {
		count, oldest, err := s.operationsQueueCount(ctx, spec, request.WorkspaceID)
		if err != nil {
			return diagnosticFailure("backend.operations.diagnostics.dlq_unavailable")
		}
		items = append(items, map[string]any{"owner": spec.owner, "count": count, "oldest_age_seconds": ageSeconds(oldest, request.Now)})
	}
	return operationsmodel.OperationsDiagnosticsSection{Status: "ready", Items: items, RunbookURL: "/operations/runbooks/dead-letter"}
}

func (s OperationsStore) operationsQueueCount(ctx context.Context, spec operationsQueueSpec, workspaceID string) (int64, string, error) {
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, spec.table, workspaceID).Projections(query.Project(query.CountAll()), query.Project(query.Min(query.Column(spec.timestamp)))).Where(query.In("status", spec.states...)).Build()
	if buildErr != nil {
		return 0, "", buildErr
	}
	var count int64
	var oldest sql.NullString
	err := s.database().QueryRowContext(ctx, queryValue, args...).Scan(&count, &oldest)
	return count, oldest.String, err
}

func (s OperationsStore) operationsBackupDiagnostics(now time.Time) operationsmodel.OperationsDiagnosticsSection {
	age := s.store.OperationalMetrics().AgeSnapshot()
	return operationsmodel.OperationsDiagnosticsSection{Status: "ready", Summary: map[string]any{"backup_age_seconds": durationAge(age.BackupLastSuccess, now), "restore_drill_age_seconds": durationAge(age.RestoreLastSuccess, now), "unknown_age_value": -1}, RunbookURL: "/operations/runbooks/backup-restore"}
}

func diagnosticFailure(code string) operationsmodel.OperationsDiagnosticsSection {
	return operationsmodel.OperationsDiagnosticsSection{Status: "unavailable", ErrorCode: code, RunbookURL: "/operations/runbooks/diagnostics"}
}
func ageSeconds(value string, now time.Time) float64 {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, value)
	}
	if err != nil {
		return -1
	}
	return durationAge(parsed, now)
}
func durationAge(value, now time.Time) float64 {
	if value.IsZero() {
		return -1
	}
	age := now.Sub(value).Seconds()
	if age < 0 {
		return 0
	}
	return age
}
