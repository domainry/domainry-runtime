package transport

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type technicalMetricsRuntimeStatusRepository struct {
	status deploymentmodel.MigrationStatus
	err    error
}

func (r technicalMetricsRuntimeStatusRepository) Ping(context.Context) error { return nil }
func (r technicalMetricsRuntimeStatusRepository) MigrationStatus(context.Context) (deploymentmodel.MigrationStatus, error) {
	return r.status, r.err
}

func TestRuntimeTechnicalMetricsExposeBoundedDatabaseAndWorkerOutcomes(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "metrics.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	store.IdempotencyMetrics(t.Context()).Observe("workspace-secret", "workflow.claim", idempotency.OutcomeAcquired)
	store.IdempotencyMetrics(t.Context()).Observe("workspace-secret", "workflow.claim", idempotency.OutcomeLeaseLost)
	store.SQLMetrics().ObserveQuery("runtime", "update", time.Millisecond, mutation.TransactionTransient("workflow", "secret-id", mutation.TransactionTransientSerializationFailure, nil))
	store.SQLMetrics().ObserveQuery("runtime", "insert", time.Millisecond, mutation.MutationConflict("workflow", "secret-id", mutation.MutationConflictUnique, nil))
	store.OperationalMetrics().ObserveMigrationLock(10*time.Millisecond, errors.New("secret lock detail"))

	output := runtimeTechnicalOpenMetrics(t.Context(), store, nil)
	for _, expected := range []string{
		`domainry_runtime_idempotency_decisions_total{outcome="acquired"} 1`,
		`domainry_runtime_idempotency_decisions_total{outcome="lease_lost"} 1`,
		`operation="update",outcome="serialization_failure"`,
		`operation="insert",outcome="conflict"`,
		// Opening the store acquires the successful schema-migration lock; the
		// explicit failed observation above is the second bounded attempt.
		`domainry_runtime_migration_lock_attempts_total 2`,
		`domainry_runtime_migration_lock_failures_total 1`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("technical metrics missing %q:\n%s", expected, output)
		}
	}
	for _, forbidden := range []string{"workspace-secret", "secret-id", "secret lock detail"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("technical metrics leaked %q: %s", forbidden, output)
		}
	}
}

func TestRuntimeTechnicalMetricsCoverAbsentAndMigrationStatusOutcomes(t *testing.T) {
	if output := runtimeTechnicalOpenMetrics(t.Context(), nil, nil); strings.Contains(output, "domainry_runtime_db_pool_connections") || strings.Contains(output, "domainry_runtime_migration_pending") {
		t.Fatalf("nil dependencies output=%s", output)
	}
	tests := []struct {
		name       string
		status     deploymentmodel.MigrationStatus
		err        error
		compatible string
	}{
		{name: "current", status: deploymentmodel.MigrationStatus{Current: true, Pending: 0}, compatible: "1"},
		{name: "not-current", status: deploymentmodel.MigrationStatus{Current: false, Pending: 2}, compatible: "0"},
		{name: "error", status: deploymentmodel.MigrationStatus{Current: true, Pending: 3}, err: errors.New("migration unavailable"), compatible: "0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := deploymentapplication.NewDeploymentRuntimeStatusApplicationService("runtime", "1", nil, nil, technicalMetricsRuntimeStatusRepository{status: test.status, err: test.err}, nil, nil, nil, nil)
			output := runtimeTechnicalOpenMetrics(t.Context(), nil, service)
			if !strings.Contains(output, "domainry_runtime_migration_pending ") || !strings.Contains(output, "domainry_runtime_migration_compatible "+test.compatible) {
				t.Fatalf("output=%s", output)
			}
		})
	}
}
