package database

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

var migrationDurationBuckets = [...]float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300}

type RuntimeOperationalMetrics struct {
	mu                       sync.RWMutex
	migrationCount           uint64
	migrationFailures        uint64
	migrationDurationSum     float64
	migrationDurationBuckets []uint64
	migrationLastSuccess     time.Time
	migrationLockAttempts    uint64
	migrationLockFailures    uint64
	migrationLockWaitSeconds float64
	backupLastSuccess        time.Time
	restoreDrillLastSuccess  time.Time
}

type OperationalAgeSnapshot struct {
	MigrationLastSuccess time.Time `json:"migration_last_success,omitempty"`
	BackupLastSuccess    time.Time `json:"backup_last_success,omitempty"`
	RestoreLastSuccess   time.Time `json:"restore_drill_last_success,omitempty"`
}

func (m *RuntimeOperationalMetrics) AgeSnapshot() OperationalAgeSnapshot {
	if m == nil {
		return OperationalAgeSnapshot{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return OperationalAgeSnapshot{MigrationLastSuccess: m.migrationLastSuccess, BackupLastSuccess: m.backupLastSuccess, RestoreLastSuccess: m.restoreDrillLastSuccess}
}

func (m *RuntimeOperationalMetrics) ObserveMigrationLock(wait time.Duration, err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.migrationLockAttempts++
	m.migrationLockWaitSeconds += wait.Seconds()
	if err != nil {
		m.migrationLockFailures++
	}
	m.mu.Unlock()
}

func NewRuntimeOperationalMetrics(backupLastSuccess, restoreDrillLastSuccess string) *RuntimeOperationalMetrics {
	return &RuntimeOperationalMetrics{
		migrationDurationBuckets: make([]uint64, len(migrationDurationBuckets)),
		backupLastSuccess:        parseOperationalTimestamp(backupLastSuccess),
		restoreDrillLastSuccess:  parseOperationalTimestamp(restoreDrillLastSuccess),
	}
}

func (m *RuntimeOperationalMetrics) ObserveMigration(duration time.Duration, err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.migrationCount++
	m.migrationDurationSum += duration.Seconds()
	for index, bound := range migrationDurationBuckets {
		if duration.Seconds() <= bound {
			m.migrationDurationBuckets[index]++
		}
	}
	if err == nil {
		m.migrationLastSuccess = time.Now().UTC()
	} else {
		m.migrationFailures++
	}
}

func (m *RuntimeOperationalMetrics) ObserveBackupSuccess(at time.Time) {
	if m == nil || at.IsZero() {
		return
	}
	m.mu.Lock()
	m.backupLastSuccess = at.UTC()
	m.mu.Unlock()
}

func (m *RuntimeOperationalMetrics) OpenMetrics(_ context.Context, now time.Time) string {
	if m == nil {
		return ""
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.RLock()
	count, failures, sum := m.migrationCount, m.migrationFailures, m.migrationDurationSum
	lockAttempts, lockFailures, lockWait := m.migrationLockAttempts, m.migrationLockFailures, m.migrationLockWaitSeconds
	buckets := append([]uint64(nil), m.migrationDurationBuckets...)
	migrationSuccess, backupSuccess, restoreSuccess := m.migrationLastSuccess, m.backupLastSuccess, m.restoreDrillLastSuccess
	m.mu.RUnlock()
	var output strings.Builder
	output.WriteString("# HELP domainry_runtime_migration_duration_seconds Runtime migration or verification duration.\n# TYPE domainry_runtime_migration_duration_seconds histogram\n")
	for index, bound := range migrationDurationBuckets {
		fmt.Fprintf(&output, "domainry_runtime_migration_duration_seconds_bucket{le=%q} %d\n", strconv.FormatFloat(bound, 'f', -1, 64), buckets[index])
	}
	fmt.Fprintf(&output, "domainry_runtime_migration_duration_seconds_bucket{le=\"+Inf\"} %d\n", count)
	fmt.Fprintf(&output, "domainry_runtime_migration_duration_seconds_sum %.9f\n", sum)
	fmt.Fprintf(&output, "domainry_runtime_migration_duration_seconds_count %d\n", count)
	output.WriteString("# HELP domainry_runtime_migration_failures_total Runtime migration or verification failures.\n# TYPE domainry_runtime_migration_failures_total counter\n")
	fmt.Fprintf(&output, "domainry_runtime_migration_failures_total %d\n", failures)
	output.WriteString("# HELP domainry_runtime_migration_lock_attempts_total Runtime migration advisory-lock acquisition attempts.\n# TYPE domainry_runtime_migration_lock_attempts_total counter\n")
	fmt.Fprintf(&output, "domainry_runtime_migration_lock_attempts_total %d\n", lockAttempts)
	output.WriteString("# HELP domainry_runtime_migration_lock_failures_total Runtime migration advisory-lock acquisition failures.\n# TYPE domainry_runtime_migration_lock_failures_total counter\n")
	fmt.Fprintf(&output, "domainry_runtime_migration_lock_failures_total %d\n", lockFailures)
	output.WriteString("# HELP domainry_runtime_migration_lock_wait_seconds_total Cumulative Runtime migration advisory-lock wait.\n# TYPE domainry_runtime_migration_lock_wait_seconds_total counter\n")
	fmt.Fprintf(&output, "domainry_runtime_migration_lock_wait_seconds_total %.9f\n", lockWait)
	writeOperationalTimestamp(&output, "domainry_runtime_migration_last_success_timestamp_seconds", "Last successful Runtime migration or verification Unix timestamp.", migrationSuccess)
	writeOperationalAge(&output, "domainry_runtime_backup_age_seconds", "Age of the last verified backup; -1 means unknown.", backupSuccess, now)
	writeOperationalAge(&output, "domainry_runtime_restore_drill_age_seconds", "Age of the last successful restore drill; -1 means unknown.", restoreSuccess, now)
	return output.String()
}

func writeOperationalTimestamp(output *strings.Builder, name, help string, value time.Time) {
	output.WriteString("# HELP " + name + " " + help + "\n# TYPE " + name + " gauge\n")
	seconds := float64(0)
	if !value.IsZero() {
		seconds = float64(value.Unix())
	}
	fmt.Fprintf(output, "%s %.0f\n", name, seconds)
}

func writeOperationalAge(output *strings.Builder, name, help string, value, now time.Time) {
	output.WriteString("# HELP " + name + " " + help + "\n# TYPE " + name + " gauge\n")
	age := -1.0
	if !value.IsZero() {
		age = now.Sub(value).Seconds()
		if age < 0 {
			age = 0
		}
	}
	fmt.Fprintf(output, "%s %.9f\n", name, age)
}

func parseOperationalTimestamp(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
