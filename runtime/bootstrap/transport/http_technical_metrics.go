package transport

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func runtimeTechnicalOpenMetrics(ctx context.Context, store *persistence.RuntimeStore, status *deploymentapplication.DeploymentRuntimeStatusApplicationService) string {
	var output strings.Builder
	output.WriteString(workerplatform.OpenMetrics(ctx))
	if store != nil {
		stats := store.DB().Stats()
		output.WriteString("# HELP domainry_runtime_db_pool_connections Database connection pool state.\n# TYPE domainry_runtime_db_pool_connections gauge\n")
		fmt.Fprintf(&output, "domainry_runtime_db_pool_connections{state=\"open\"} %d\n", stats.OpenConnections)
		fmt.Fprintf(&output, "domainry_runtime_db_pool_connections{state=\"in_use\"} %d\n", stats.InUse)
		fmt.Fprintf(&output, "domainry_runtime_db_pool_connections{state=\"idle\"} %d\n", stats.Idle)
		output.WriteString("# HELP domainry_runtime_db_pool_wait_total Database connection pool waits.\n# TYPE domainry_runtime_db_pool_wait_total counter\n")
		fmt.Fprintf(&output, "domainry_runtime_db_pool_wait_total %d\n", stats.WaitCount)
		output.WriteString("# HELP domainry_runtime_db_pool_wait_duration_seconds_total Time spent waiting for database connections.\n# TYPE domainry_runtime_db_pool_wait_duration_seconds_total counter\n")
		fmt.Fprintf(&output, "domainry_runtime_db_pool_wait_duration_seconds_total %.9f\n", stats.WaitDuration.Seconds())
		collector := store.IdempotencyMetrics(ctx)
		snapshot := collector.Snapshot()
		output.WriteString("# HELP domainry_runtime_idempotency_decisions_total Idempotency decisions by stable outcome.\n# TYPE domainry_runtime_idempotency_decisions_total counter\n")
		for _, outcome := range []idempotency.Outcome{idempotency.OutcomeAcquired, idempotency.OutcomeReplayed, idempotency.OutcomeInProgress, idempotency.OutcomeConflict, idempotency.OutcomeReclaimed, idempotency.OutcomeLeaseLost, idempotency.OutcomeDuplicateSideEffect} {
			fmt.Fprintf(&output, "domainry_runtime_idempotency_decisions_total{outcome=%q} %d\n", outcome, snapshot.Totals[outcome])
		}
		fmt.Fprintf(&output, "domainry_runtime_telemetry_dropped_series_total{signal=\"idempotency\"} %d\n", snapshot.DroppedSeries)
		output.WriteString(store.SQLMetrics().OpenMetrics(ctx))
		output.WriteString(store.OperationalMetrics().OpenMetrics(ctx, time.Now().UTC()))
	}
	if status != nil {
		pending, current, err := status.MigrationTelemetry(ctx)
		output.WriteString("# HELP domainry_runtime_migration_pending Pending Runtime migrations.\n# TYPE domainry_runtime_migration_pending gauge\n")
		fmt.Fprintf(&output, "domainry_runtime_migration_pending %d\n", pending)
		compatible := 0
		if err == nil && current {
			compatible = 1
		}
		output.WriteString("# HELP domainry_runtime_migration_compatible Whether the Runtime schema is compatible.\n# TYPE domainry_runtime_migration_compatible gauge\n")
		fmt.Fprintf(&output, "domainry_runtime_migration_compatible %d\n", compatible)
	}
	return output.String()
}
