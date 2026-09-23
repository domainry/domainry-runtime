package policy

import (
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func TestDatabaseRetirementFullOrderedLifecycle(t *testing.T) {
	baseTime := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	current := databaseRetirementFixture(baseTime)
	states := []operationsmodel.DatabaseRetirementState{
		operationsmodel.DatabaseRetirementReplacementReady,
		operationsmodel.DatabaseRetirementBackfilled,
		operationsmodel.DatabaseRetirementReadsSwitched,
		operationsmodel.DatabaseRetirementWritesDisabled,
		operationsmodel.DatabaseRetirementObservationComplete,
		operationsmodel.DatabaseRetirementQuarantined,
		operationsmodel.DatabaseRetirementDropped,
		operationsmodel.DatabaseRetirementCodeRemoved,
	}
	quarantineUntil := baseTime.Add(10 * time.Hour)
	for index, state := range states {
		now := baseTime.Add(time.Duration(index+1) * time.Hour)
		next := current
		next.State, next.UpdatedAt = state, now
		if state == operationsmodel.DatabaseRetirementQuarantined {
			next.Evidence.QuarantineUntil = &quarantineUntil
		}
		if state == operationsmodel.DatabaseRetirementDropped {
			now = quarantineUntil
			next.UpdatedAt = now
		}
		if err := OperationsValidateDatabaseRetirementTransition(current, next, now); err != nil {
			t.Fatalf("transition %s -> %s: %v", current.State, state, err)
		}
		current = next
	}

	blocked := databaseRetirementFixture(baseTime)
	blocked.State, blocked.BlockedReason, blocked.UpdatedAt = operationsmodel.DatabaseRetirementBlocked, "dependency remains", baseTime
	if err := OperationsValidateDatabaseRetirementTransition(databaseRetirementFixture(baseTime), blocked, baseTime); err != nil {
		t.Fatalf("blocked transition: %v", err)
	}
	blocked.BlockedReason = ""
	if err := OperationsValidateDatabaseRetirementTransition(databaseRetirementFixture(baseTime), blocked, baseTime); err == nil {
		t.Fatal("blocked transition without reason accepted")
	}
	blocked.BlockedReason, blocked.Evidence.AuditEventID = "reason", ""
	if err := OperationsValidateDatabaseRetirementTransition(databaseRetirementFixture(baseTime), blocked, baseTime); err != nil {
		t.Fatalf("blocked transition incorrectly requires terminal Audit evidence: %v", err)
	}
}

func TestDatabaseRetirementTransitionIdentityOrderAndEvidenceFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	current := databaseRetirementFixture(now)
	next := current
	next.State, next.UpdatedAt = operationsmodel.DatabaseRetirementReplacementReady, now
	for _, mutate := range []func(*operationsmodel.DatabaseRetirement){
		func(value *operationsmodel.DatabaseRetirement) { value.ID = "" },
		func(value *operationsmodel.DatabaseRetirement) { value.Object.Engine = "" },
		func(value *operationsmodel.DatabaseRetirement) { value.Object.Database = "" },
		func(value *operationsmodel.DatabaseRetirement) { value.Object.Kind = "" },
		func(value *operationsmodel.DatabaseRetirement) { value.Object.Name = "" },
		func(value *operationsmodel.DatabaseRetirement) { value.Evidence.Owner = "" },
		func(value *operationsmodel.DatabaseRetirement) { value.UpdatedAt = time.Time{} },
	} {
		invalid := next
		mutate(&invalid)
		if err := OperationsValidateDatabaseRetirementTransition(current, invalid, now); err == nil {
			t.Fatal("invalid retirement identity accepted")
		}
	}
	for _, mutate := range []func(*operationsmodel.DatabaseRetirement){
		func(value *operationsmodel.DatabaseRetirement) { value.ID = "other" },
		func(value *operationsmodel.DatabaseRetirement) { value.Object.Name = "other" },
		func(value *operationsmodel.DatabaseRetirement) { value.UpdatedAt = now.Add(time.Second) },
	} {
		invalid := next
		mutate(&invalid)
		if err := OperationsValidateDatabaseRetirementTransition(current, invalid, now); err == nil {
			t.Fatal("changed identity/time accepted")
		}
	}
	skipped := next
	skipped.State = operationsmodel.DatabaseRetirementBackfilled
	if err := OperationsValidateDatabaseRetirementTransition(current, skipped, now); err == nil {
		t.Fatal("skipped state accepted")
	}
	unknown := next
	unknown.State = "unknown"
	if err := OperationsValidateDatabaseRetirementTransition(current, unknown, now); err == nil {
		t.Fatal("unknown state accepted")
	}

	for _, mutate := range []func(*operationsmodel.DatabaseRetirement){
		func(value *operationsmodel.DatabaseRetirement) { value.Evidence.Replacement = "" },
		func(value *operationsmodel.DatabaseRetirement) { value.Evidence.ExpectedSchemaVersion = "" },
		func(value *operationsmodel.DatabaseRetirement) { value.Evidence.ExpectedDataVersion = "" },
	} {
		invalid := next
		mutate(&invalid)
		if err := OperationsValidateDatabaseRetirementTransition(current, invalid, now); err == nil {
			t.Fatal("incomplete replacement evidence accepted")
		}
	}

	current = next
	backfilled := current
	backfilled.State = operationsmodel.DatabaseRetirementBackfilled
	for _, mutate := range []func(*operationsmodel.DatabaseRetirement){
		func(value *operationsmodel.DatabaseRetirement) { value.Evidence.BackfillComplete = false },
		func(value *operationsmodel.DatabaseRetirement) { value.Evidence.BackfillCheckpoint = "" },
		func(value *operationsmodel.DatabaseRetirement) { value.Evidence.Comparison.SourceRows++ },
	} {
		invalid := backfilled
		mutate(&invalid)
		if err := OperationsValidateDatabaseRetirementTransition(current, invalid, now); err == nil {
			t.Fatal("incomplete backfill evidence accepted")
		}
	}
	if err := OperationsValidateDatabaseRetirementTransition(current, backfilled, now); err != nil {
		t.Fatal(err)
	}
	current = backfilled
	reads := current
	reads.State = operationsmodel.DatabaseRetirementReadsSwitched
	reads.Evidence.ReadsSwitchVersion = ""
	if err := OperationsValidateDatabaseRetirementTransition(current, reads, now); err == nil {
		t.Fatal("read switch without version accepted")
	}
	reads.Evidence.ReadsSwitchVersion = "2"
	if err := OperationsValidateDatabaseRetirementTransition(current, reads, now); err != nil {
		t.Fatal(err)
	}
	current = reads
	writes := current
	writes.State = operationsmodel.DatabaseRetirementWritesDisabled
	for _, mutate := range []func(*operationsmodel.DatabaseRetirement){
		func(value *operationsmodel.DatabaseRetirement) { value.Evidence.WritesDisableVersion = "" },
		func(value *operationsmodel.DatabaseRetirement) { value.Evidence.WriteProtection = "" },
	} {
		invalid := writes
		mutate(&invalid)
		if err := OperationsValidateDatabaseRetirementTransition(current, invalid, now); err == nil {
			t.Fatal("write protection evidence gap accepted")
		}
	}

	quarantined := databaseRetirementFixture(now)
	quarantined.State = operationsmodel.DatabaseRetirementQuarantined
	quarantined.Evidence.QuarantineObjectName = "retired_table"
	restored := quarantined
	restored.State = operationsmodel.DatabaseRetirementObservationComplete
	for _, mutateCurrentNext := range []func(*operationsmodel.DatabaseRetirement, *operationsmodel.DatabaseRetirement){
		func(current, _ *operationsmodel.DatabaseRetirement) { current.Evidence.QuarantineObjectName = "" },
		func(_, next *operationsmodel.DatabaseRetirement) { next.Evidence.Rollback = "" },
	} {
		invalidCurrent, invalidNext := quarantined, restored
		mutateCurrentNext(&invalidCurrent, &invalidNext)
		if err := OperationsValidateDatabaseRetirementTransition(invalidCurrent, invalidNext, now); err == nil {
			t.Fatal("restore evidence gap accepted")
		}
	}
}

func TestDatabaseRetirementTransitionStageSpecificFailures(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	writesDisabled := databaseRetirementFixture(now)
	writesDisabled.State = operationsmodel.DatabaseRetirementWritesDisabled
	observationComplete := writesDisabled
	observationComplete.State = operationsmodel.DatabaseRetirementObservationComplete
	observationComplete.Evidence.Observation.ReadCount = 1
	if err := OperationsValidateDatabaseRetirementTransition(writesDisabled, observationComplete, now); err == nil {
		t.Fatal("observation-complete transition accepted observed access")
	}
	observationComplete.Evidence.Observation.ReadCount = 0
	if err := OperationsValidateDatabaseRetirementTransition(writesDisabled, observationComplete, now); err != nil {
		t.Fatal(err)
	}

	quarantined := observationComplete
	quarantined.State = operationsmodel.DatabaseRetirementQuarantined
	quarantined.Evidence.QuarantineUntil = nil
	if err := OperationsValidateDatabaseRetirementTransition(observationComplete, quarantined, now); err == nil {
		t.Fatal("quarantine without deadline accepted")
	}
	past := now.Add(-time.Second)
	quarantined.Evidence.QuarantineUntil = &past
	if err := OperationsValidateDatabaseRetirementTransition(observationComplete, quarantined, now); err == nil {
		t.Fatal("expired quarantine accepted")
	}
	future := now.Add(time.Hour)
	quarantined.Evidence.QuarantineUntil = &future
	quarantined.Evidence.Rollback = ""
	if err := OperationsValidateDatabaseRetirementTransition(observationComplete, quarantined, now); err == nil {
		t.Fatal("quarantine without rollback accepted")
	}
	quarantined.Evidence.Rollback = "restore"
	if err := OperationsValidateDatabaseRetirementTransition(observationComplete, quarantined, now); err != nil {
		t.Fatal(err)
	}

	dropped := quarantined
	dropped.State = operationsmodel.DatabaseRetirementDropped
	dropped.Evidence.QuarantineUntil = nil
	if err := OperationsValidateDatabaseRetirementTransition(quarantined, dropped, now); err == nil {
		t.Fatal("drop without quarantine deadline accepted")
	}
	dropped.Evidence.QuarantineUntil = &future
	dropped.Evidence.BackupID = ""
	if err := OperationsValidateDatabaseRetirementTransition(quarantined, dropped, now); err == nil {
		t.Fatal("drop with invalid evidence accepted")
	}
	dropped.Evidence.BackupID = "backup-1"
	if err := OperationsValidateDatabaseRetirementTransition(quarantined, dropped, now); err == nil {
		t.Fatal("drop before quarantine deadline accepted")
	}
	dropped.UpdatedAt = future
	dropped.Evidence.AuditEventID = ""
	if err := OperationsValidateDatabaseRetirementTransition(quarantined, dropped, future); err == nil {
		t.Fatal("drop without verified terminal Audit reference accepted")
	}
	dropped.Evidence.AuditEventID = "audit-1"
	if err := OperationsValidateDatabaseRetirementTransition(quarantined, dropped, future); err != nil {
		t.Fatal(err)
	}

	codeRemoved := dropped
	codeRemoved.State = operationsmodel.DatabaseRetirementCodeRemoved
	codeRemoved.Evidence.AuditEventID = ""
	if err := OperationsValidateDatabaseRetirementTransition(dropped, codeRemoved, future); err == nil {
		t.Fatal("code removal without audit accepted")
	}
}

func TestDatabaseObservationCompleteMatrix(t *testing.T) {
	now := time.Now().UTC()
	base := operationsmodel.DatabaseAccessObservation{WindowStarted: now.Add(-2 * time.Hour), WindowEnds: now.Add(-time.Hour), SourceCounts: map[string]uint64{"runtime": 0}}
	if err := OperationsValidateDatabaseObservation(base, now); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*operationsmodel.DatabaseAccessObservation){
		func(value *operationsmodel.DatabaseAccessObservation) { value.WindowStarted = time.Time{} },
		func(value *operationsmodel.DatabaseAccessObservation) { value.WindowEnds = value.WindowStarted },
		func(value *operationsmodel.DatabaseAccessObservation) { value.WindowEnds = now.Add(time.Second) },
		func(value *operationsmodel.DatabaseAccessObservation) { value.ReadCount = 1 },
		func(value *operationsmodel.DatabaseAccessObservation) { value.WriteCount = 1 },
		func(value *operationsmodel.DatabaseAccessObservation) { value.LastReadAt = &now },
		func(value *operationsmodel.DatabaseAccessObservation) { value.LastWriteAt = &now },
		func(value *operationsmodel.DatabaseAccessObservation) { value.SourceCounts = map[string]uint64{"": 0} },
		func(value *operationsmodel.DatabaseAccessObservation) {
			value.SourceCounts = map[string]uint64{"runtime": 1}
		},
	} {
		invalid := base
		mutate(&invalid)
		if err := OperationsValidateDatabaseObservation(invalid, now); err == nil {
			t.Fatal("invalid observation accepted")
		}
	}
}

func TestDatabaseDropEvidenceCompleteMatrix(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	base := databaseRetirementFixture(now).Evidence
	for _, mutate := range []func(*operationsmodel.DatabaseRetirementEvidence){
		func(value *operationsmodel.DatabaseRetirementEvidence) { value.Owner = "" },
		func(value *operationsmodel.DatabaseRetirementEvidence) { value.ChangePlanID = "" },
		func(value *operationsmodel.DatabaseRetirementEvidence) { value.ApprovalID = "" },
		func(value *operationsmodel.DatabaseRetirementEvidence) { value.BackupID = "" },
		func(value *operationsmodel.DatabaseRetirementEvidence) { value.BackupChecksum = "" },
		func(value *operationsmodel.DatabaseRetirementEvidence) { value.MaintenanceEvidence = "" },
		func(value *operationsmodel.DatabaseRetirementEvidence) { value.DrainEvidence = "" },
		func(value *operationsmodel.DatabaseRetirementEvidence) { value.Rollback = "" },
	} {
		invalid := base
		mutate(&invalid)
		if err := OperationsValidateDatabaseDropEvidence(invalid, now); err == nil {
			t.Fatal("missing drop evidence accepted")
		}
	}
	withoutTerminalAudit := base
	withoutTerminalAudit.AuditEventID = ""
	if err := OperationsValidateDatabaseDropEvidence(withoutTerminalAudit, now); err != nil {
		t.Fatalf("pre-execution readiness incorrectly requires terminal Audit evidence: %v", err)
	}
	for _, mutate := range []func(*operationsmodel.DatabaseRetirementEvidence){
		func(value *operationsmodel.DatabaseRetirementEvidence) { value.BackupVerifiedAt = nil },
		func(value *operationsmodel.DatabaseRetirementEvidence) { value.RestoreDrillAt = nil },
		func(value *operationsmodel.DatabaseRetirementEvidence) {
			future := now.Add(time.Second)
			value.BackupVerifiedAt = &future
		},
		func(value *operationsmodel.DatabaseRetirementEvidence) {
			future := now.Add(time.Second)
			value.RestoreDrillAt = &future
		},
		func(value *operationsmodel.DatabaseRetirementEvidence) {
			stale := now.Add(-MaximumRetirementEvidenceAge - time.Second)
			value.BackupVerifiedAt = &stale
		},
		func(value *operationsmodel.DatabaseRetirementEvidence) {
			stale := now.Add(-MaximumRetirementEvidenceAge - time.Second)
			value.RestoreDrillAt = &stale
		},
	} {
		invalid := base
		mutate(&invalid)
		if err := OperationsValidateDatabaseDropEvidence(invalid, now); err == nil {
			t.Fatal("invalid backup/drill evidence accepted")
		}
	}
	for _, disposition := range []string{"migrate", "archive", "retain", "discard", "anonymize"} {
		evidence := base
		evidence.Disposition = disposition
		if disposition == "discard" || disposition == "anonymize" {
			evidence.DispositionApproval = "approval"
		}
		if err := OperationsValidateDatabaseDropEvidence(evidence, now); err != nil {
			t.Fatalf("valid disposition %q: %v", disposition, err)
		}
	}
	for _, disposition := range []string{"unknown", "discard", "anonymize"} {
		invalid := base
		invalid.Disposition = disposition
		invalid.DispositionApproval = ""
		if err := OperationsValidateDatabaseDropEvidence(invalid, now); err == nil {
			t.Fatalf("invalid disposition %q accepted", disposition)
		}
	}
	comparisonFailure := base
	comparisonFailure.Comparison.SourceHash = "different"
	if err := OperationsValidateDatabaseDropEvidence(comparisonFailure, now); err == nil {
		t.Fatal("comparison failure accepted")
	}
	observationFailure := base
	observationFailure.Observation.WriteCount = 1
	if err := OperationsValidateDatabaseDropEvidence(observationFailure, now); err == nil {
		t.Fatal("observation failure accepted")
	}
}

func TestDatabaseDropPlanIdentityAndStatementMatrix(t *testing.T) {
	base := operationsmodel.DatabaseDropPlan{
		RetirementID: "retirement", Object: operationsmodel.DatabaseObjectIdentity{Engine: "postgres", Kind: "table", Name: "old"},
		Statements: []string{"DROP TABLE old"}, EstimatedLock: time.Second, LockTimeout: 2 * time.Second,
		EstimatedReclaimBytes: 0, Rollback: "restore", ApprovalID: "approval",
	}
	for _, mutate := range []func(*operationsmodel.DatabaseDropPlan){
		func(value *operationsmodel.DatabaseDropPlan) { value.RetirementID = "" },
		func(value *operationsmodel.DatabaseDropPlan) { value.Object.Engine = "" },
		func(value *operationsmodel.DatabaseDropPlan) { value.Object.Kind = "" },
		func(value *operationsmodel.DatabaseDropPlan) { value.Object.Name = "" },
		func(value *operationsmodel.DatabaseDropPlan) { value.Statements = nil },
		func(value *operationsmodel.DatabaseDropPlan) { value.EstimatedLock = 0 },
		func(value *operationsmodel.DatabaseDropPlan) { value.LockTimeout = 0 },
		func(value *operationsmodel.DatabaseDropPlan) { value.EstimatedLock = 3 * time.Second },
		func(value *operationsmodel.DatabaseDropPlan) { value.EstimatedReclaimBytes = -1 },
		func(value *operationsmodel.DatabaseDropPlan) { value.Rollback = "" },
		func(value *operationsmodel.DatabaseDropPlan) { value.ApprovalID = "" },
	} {
		invalid := base
		mutate(&invalid)
		if err := OperationsValidateDatabaseDropPlan(invalid); err == nil {
			t.Fatal("invalid drop plan accepted")
		}
	}
	for _, statement := range []string{"", "   ", "DROP TABLE old CASCADE"} {
		invalid := base
		invalid.Statements = []string{statement}
		if err := OperationsValidateDatabaseDropPlan(invalid); err == nil {
			t.Fatalf("invalid statement %q accepted", statement)
		}
	}
	external := base
	external.ExternalTarget = true
	if err := OperationsValidateDatabaseDropPlan(external); err == nil {
		t.Fatal("external plan without scope accepted")
	}
	external.DeploymentScope = "production"
	if err := OperationsValidateDatabaseDropPlan(external); err != nil {
		t.Fatal(err)
	}
	for _, state := range databaseRetirementOrder {
		if databaseRetirementStateIndex(state) < 0 {
			t.Fatalf("state %q missing from order", state)
		}
	}
	if databaseRetirementStateIndex("unknown") != -100 {
		t.Fatal("unknown state index mismatch")
	}
}
