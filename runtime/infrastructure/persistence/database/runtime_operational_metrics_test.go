package database

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRuntimeOperationalMetricsExposeMigrationBackupAndRestoreState(t *testing.T) {
	metrics := NewRuntimeOperationalMetrics("2026-07-18T12:00:00Z", "2026-07-10T12:00:00Z")
	metrics.ObserveMigration(125*time.Millisecond, nil)
	metrics.ObserveMigration(2*time.Second, errors.New("migration SQL and DSN must not escape"))
	metrics.ObserveMigrationLock(25*time.Millisecond, nil)
	metrics.ObserveMigrationLock(75*time.Millisecond, errors.New("lock detail must not escape"))
	metrics.ObserveBackupSuccess(time.Date(2026, 7, 19, 11, 0, 0, 0, time.UTC))
	output := metrics.OpenMetrics(t.Context(), time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC))
	for _, expected := range []string{
		"domainry_runtime_migration_duration_seconds_count 2",
		"domainry_runtime_migration_failures_total 1",
		"domainry_runtime_migration_lock_attempts_total 2",
		"domainry_runtime_migration_lock_failures_total 1",
		"domainry_runtime_migration_lock_wait_seconds_total 0.100000000",
		"domainry_runtime_backup_age_seconds 3600.000000000",
		"domainry_runtime_restore_drill_age_seconds 777600.000000000",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "migration SQL") || strings.Contains(output, "DSN") || strings.Contains(output, "lock detail") {
		t.Fatalf("migration failure detail leaked: %s", output)
	}
}

func TestRuntimeOperationalMetricsUseUnknownAgeForMissingEvidence(t *testing.T) {
	metrics := NewRuntimeOperationalMetrics("", "invalid")
	output := metrics.OpenMetrics(t.Context(), time.Now().UTC())
	if !strings.Contains(output, "domainry_runtime_backup_age_seconds -1.000000000") || !strings.Contains(output, "domainry_runtime_restore_drill_age_seconds -1.000000000") {
		t.Fatalf("missing evidence was not represented as unknown: %s", output)
	}
}
