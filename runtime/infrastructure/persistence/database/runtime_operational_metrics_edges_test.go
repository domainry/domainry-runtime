package database

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRuntimeOperationalMetricsNilAndAgeEdges(t *testing.T) {
	var metrics *RuntimeOperationalMetrics
	if metrics.AgeSnapshot() != (OperationalAgeSnapshot{}) || metrics.OpenMetrics(t.Context(), time.Time{}) != "" {
		t.Fatal("nil metrics returned state")
	}
	metrics.ObserveMigrationLock(time.Second, errors.New("ignored"))
	metrics.ObserveMigration(time.Second, errors.New("ignored"))
	metrics.ObserveBackupSuccess(time.Now())

	metrics = NewRuntimeOperationalMetrics("", "")
	metrics.ObserveBackupSuccess(time.Time{})
	metrics.ObserveMigrationLock(time.Millisecond, errors.New("lock"))
	metrics.ObserveMigration(time.Nanosecond, nil)
	snapshot := metrics.AgeSnapshot()
	if snapshot.MigrationLastSuccess.IsZero() {
		t.Fatal("migration success was not recorded")
	}
	metrics.ObserveBackupSuccess(time.Now().UTC().Add(time.Hour))
	output := metrics.OpenMetrics(t.Context(), time.Time{})
	if !strings.Contains(output, "domainry_runtime_backup_age_seconds 0.000000000") {
		t.Fatalf("future backup age was not clamped: %s", output)
	}
}
