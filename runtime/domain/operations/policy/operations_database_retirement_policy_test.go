package policy

import (
	"strings"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func TestDatabaseRetirementRequiresOrderedEvidenceBackedTransitions(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	current := databaseRetirementFixture(now)
	next := current
	next.State = operationsmodel.DatabaseRetirementReplacementReady
	next.UpdatedAt = now
	if err := OperationsValidateDatabaseRetirementTransition(current, next, now); err != nil {
		t.Fatal(err)
	}
	next.State = operationsmodel.DatabaseRetirementBackfilled
	if err := OperationsValidateDatabaseRetirementTransition(current, next, now); err == nil {
		t.Fatal("skipped replacement-ready state")
	}
}

func TestDatabaseRetirementBlocksDropOnAccessWindowOrStaleRecoveryEvidence(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	retirement := databaseRetirementFixture(now)
	evidence := retirement.Evidence
	if err := OperationsValidateDatabaseDropEvidence(evidence, now); err != nil {
		t.Fatalf("valid evidence rejected: %v", err)
	}
	evidence.Observation.ReadCount = 1
	if err := OperationsValidateDatabaseDropEvidence(evidence, now); err == nil || !strings.Contains(err.Error(), "access_observed") {
		t.Fatalf("observed read did not block drop: %v", err)
	}
	evidence = retirement.Evidence
	stale := now.Add(-MaximumRetirementEvidenceAge - time.Second)
	evidence.RestoreDrillAt = &stale
	if err := OperationsValidateDatabaseDropEvidence(evidence, now); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("stale restore drill did not block drop: %v", err)
	}
}

func TestDatabaseDropPlanRejectsCascadeAndUnscopedExternalTarget(t *testing.T) {
	plan := operationsmodel.DatabaseDropPlan{RetirementID: "retire-1", Object: operationsmodel.DatabaseObjectIdentity{Engine: "postgres", Database: "runtime", Schema: "public", Kind: "table", Name: "old_table"}, Statements: []string{`DROP TABLE "old_table"`}, EstimatedLock: time.Second, LockTimeout: 5 * time.Second, Rollback: "restore backup-1", ApprovalID: "approval-1"}
	if err := OperationsValidateDatabaseDropPlan(plan); err != nil {
		t.Fatal(err)
	}
	plan.Statements[0] += " CASCADE"
	if err := OperationsValidateDatabaseDropPlan(plan); err == nil {
		t.Fatal("DROP CASCADE accepted")
	}
	plan.Statements[0] = `DROP TABLE "old_table"`
	plan.ExternalTarget = true
	if err := OperationsValidateDatabaseDropPlan(plan); err == nil {
		t.Fatal("external target without deployment scope accepted")
	}
}

func TestDatabaseRetirementAllowsAuditedRestoreFromQuarantine(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	current := databaseRetirementFixture(now)
	current.State = operationsmodel.DatabaseRetirementQuarantined
	current.Evidence.QuarantineObjectName = "retired_old_table"
	next := current
	next.State, next.UpdatedAt = operationsmodel.DatabaseRetirementObservationComplete, now
	if err := OperationsValidateDatabaseRetirementTransition(current, next, now); err != nil {
		t.Fatal(err)
	}
}

func databaseRetirementFixture(now time.Time) operationsmodel.DatabaseRetirement {
	verified, drill := now.Add(-time.Hour), now.Add(-2*time.Hour)
	return operationsmodel.DatabaseRetirement{
		ID: "retire-1", Object: operationsmodel.DatabaseObjectIdentity{Engine: "postgres", Database: "runtime", Schema: "public", Kind: "table", Name: "old_table"}, State: operationsmodel.DatabaseRetirementDiscovered, UpdatedAt: now,
		Evidence: operationsmodel.DatabaseRetirementEvidence{
			Owner: "record", Replacement: "new_table", ExpectedSchemaVersion: "2", ExpectedDataVersion: "2", BackfillCheckpoint: "checkpoint-1", BackfillComplete: true,
			ReadsSwitchVersion: "2.1", WritesDisableVersion: "2.2", WriteProtection: "rejecting trigger old_table_no_write",
			Observation: operationsmodel.DatabaseAccessObservation{WindowStarted: now.Add(-48 * time.Hour), WindowEnds: now.Add(-24 * time.Hour), SourceCounts: map[string]uint64{"runtime": 0}},
			Comparison:  operationsmodel.DatabaseDataComparison{SourceRows: 10, ReplacementRows: 10, SourceKeys: 10, ReplacementKeys: 10, SourceHash: "same", ReplacementHash: "same", BusinessChecks: []string{"order_totals"}, ComparedAt: now.Add(-25 * time.Hour)},
			Disposition: "migrate", BackupID: "backup-1", BackupChecksum: "checksum", BackupVerifiedAt: &verified, RestoreDrillAt: &drill,
			MaintenanceEvidence: "maintenance-1", DrainEvidence: "drain-1", ChangePlanID: "change-1", ApprovalID: "approval-1", Rollback: "restore backup-1", AuditEventID: "audit-1",
		},
	}
}
