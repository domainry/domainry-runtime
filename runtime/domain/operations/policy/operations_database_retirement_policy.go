package policy

import (
	"fmt"
	"strings"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

const MaximumRetirementEvidenceAge = 30 * 24 * time.Hour

var databaseRetirementOrder = []operationsmodel.DatabaseRetirementState{
	operationsmodel.DatabaseRetirementDiscovered,
	operationsmodel.DatabaseRetirementReplacementReady,
	operationsmodel.DatabaseRetirementBackfilled,
	operationsmodel.DatabaseRetirementReadsSwitched,
	operationsmodel.DatabaseRetirementWritesDisabled,
	operationsmodel.DatabaseRetirementObservationComplete,
	operationsmodel.DatabaseRetirementQuarantined,
	operationsmodel.DatabaseRetirementDropped,
	operationsmodel.DatabaseRetirementCodeRemoved,
}

func OperationsValidateDatabaseRetirementTransition(current, next operationsmodel.DatabaseRetirement, now time.Time) error {
	if err := operationsValidateDatabaseRetirementIdentity(next); err != nil {
		return err
	}
	if current.ID != next.ID || current.Object != next.Object || !next.UpdatedAt.Equal(now) {
		return fmt.Errorf("operations.database_retirement_identity_or_time_changed")
	}
	if next.State == operationsmodel.DatabaseRetirementBlocked {
		if strings.TrimSpace(next.BlockedReason) == "" {
			return fmt.Errorf("operations.database_retirement_blocked_evidence_required")
		}
		return nil
	}
	restoreFromQuarantine := current.State == operationsmodel.DatabaseRetirementQuarantined && next.State == operationsmodel.DatabaseRetirementObservationComplete
	if !restoreFromQuarantine && databaseRetirementStateIndex(next.State) != databaseRetirementStateIndex(current.State)+1 {
		return fmt.Errorf("operations.database_retirement_transition_invalid")
	}
	if restoreFromQuarantine && (strings.TrimSpace(current.Evidence.QuarantineObjectName) == "" || strings.TrimSpace(next.Evidence.Rollback) == "") {
		return fmt.Errorf("operations.database_retirement_restore_evidence_required")
	}
	switch next.State {
	case operationsmodel.DatabaseRetirementReplacementReady:
		if strings.TrimSpace(next.Evidence.Replacement) == "" || strings.TrimSpace(next.Evidence.ExpectedSchemaVersion) == "" || strings.TrimSpace(next.Evidence.ExpectedDataVersion) == "" {
			return fmt.Errorf("operations.database_retirement_replacement_required")
		}
	case operationsmodel.DatabaseRetirementBackfilled:
		if !next.Evidence.BackfillComplete || strings.TrimSpace(next.Evidence.BackfillCheckpoint) == "" || !databaseComparisonMatches(next.Evidence.Comparison) {
			return fmt.Errorf("operations.database_retirement_backfill_evidence_required")
		}
	case operationsmodel.DatabaseRetirementReadsSwitched:
		if strings.TrimSpace(next.Evidence.ReadsSwitchVersion) == "" {
			return fmt.Errorf("operations.database_retirement_read_switch_required")
		}
	case operationsmodel.DatabaseRetirementWritesDisabled:
		if strings.TrimSpace(next.Evidence.WritesDisableVersion) == "" || strings.TrimSpace(next.Evidence.WriteProtection) == "" {
			return fmt.Errorf("operations.database_retirement_write_protection_required")
		}
	case operationsmodel.DatabaseRetirementObservationComplete:
		if err := OperationsValidateDatabaseObservation(next.Evidence.Observation, now); err != nil {
			return err
		}
	case operationsmodel.DatabaseRetirementQuarantined:
		if next.Evidence.QuarantineUntil == nil || !next.Evidence.QuarantineUntil.After(now) || strings.TrimSpace(next.Evidence.Rollback) == "" {
			return fmt.Errorf("operations.database_retirement_quarantine_required")
		}
	case operationsmodel.DatabaseRetirementDropped:
		if err := OperationsValidateDatabaseRetirementDropReadiness(next, now); err != nil {
			return err
		}
		if strings.TrimSpace(next.Evidence.AuditEventID) == "" {
			return fmt.Errorf("operations.database_retirement_terminal_audit_required")
		}
	case operationsmodel.DatabaseRetirementCodeRemoved:
		if strings.TrimSpace(next.Evidence.AuditEventID) == "" {
			return fmt.Errorf("operations.database_retirement_code_removal_audit_required")
		}
	}
	return nil
}

// OperationsValidateDatabaseRetirementDropReadiness validates the destructive
// preconditions that must exist before execution. The terminal Audit event does
// not exist yet and is deliberately excluded; transition to dropped requires
// that immutable event after execution.
func OperationsValidateDatabaseRetirementDropReadiness(retirement operationsmodel.DatabaseRetirement, now time.Time) error {
	if retirement.State != operationsmodel.DatabaseRetirementQuarantined && retirement.State != operationsmodel.DatabaseRetirementDropped {
		return fmt.Errorf("operations.database_retirement_not_quarantined")
	}
	if err := OperationsValidateDatabaseDropEvidence(retirement.Evidence, now); err != nil {
		return err
	}
	if retirement.Evidence.QuarantineUntil == nil || now.Before(*retirement.Evidence.QuarantineUntil) {
		return fmt.Errorf("operations.database_retirement_quarantine_window_open")
	}
	return nil
}

func OperationsValidateDatabaseObservation(observation operationsmodel.DatabaseAccessObservation, now time.Time) error {
	if observation.WindowStarted.IsZero() || !observation.WindowEnds.After(observation.WindowStarted) || now.Before(observation.WindowEnds) {
		return fmt.Errorf("operations.database_retirement_observation_window_incomplete")
	}
	if observation.ReadCount != 0 || observation.WriteCount != 0 || observation.LastReadAt != nil || observation.LastWriteAt != nil {
		return fmt.Errorf("operations.database_retirement_access_observed")
	}
	for source, count := range observation.SourceCounts {
		if strings.TrimSpace(source) == "" || count != 0 {
			return fmt.Errorf("operations.database_retirement_source_access_observed")
		}
	}
	return nil
}

func OperationsValidateDatabaseDropEvidence(evidence operationsmodel.DatabaseRetirementEvidence, now time.Time) error {
	if strings.TrimSpace(evidence.Owner) == "" || strings.TrimSpace(evidence.ChangePlanID) == "" || strings.TrimSpace(evidence.ApprovalID) == "" || strings.TrimSpace(evidence.BackupID) == "" || strings.TrimSpace(evidence.BackupChecksum) == "" || strings.TrimSpace(evidence.MaintenanceEvidence) == "" || strings.TrimSpace(evidence.DrainEvidence) == "" || strings.TrimSpace(evidence.Rollback) == "" {
		return fmt.Errorf("operations.database_retirement_drop_evidence_required")
	}
	if evidence.BackupVerifiedAt == nil || evidence.RestoreDrillAt == nil || evidence.BackupVerifiedAt.After(now) || evidence.RestoreDrillAt.After(now) || now.Sub(*evidence.BackupVerifiedAt) > MaximumRetirementEvidenceAge || now.Sub(*evidence.RestoreDrillAt) > MaximumRetirementEvidenceAge {
		return fmt.Errorf("operations.database_retirement_backup_or_drill_expired")
	}
	if !databaseDispositionValid(evidence.Disposition) || ((evidence.Disposition == "discard" || evidence.Disposition == "anonymize") && strings.TrimSpace(evidence.DispositionApproval) == "") {
		return fmt.Errorf("operations.database_retirement_disposition_invalid")
	}
	if !databaseComparisonMatches(evidence.Comparison) {
		return fmt.Errorf("operations.database_retirement_comparison_failed")
	}
	return OperationsValidateDatabaseObservation(evidence.Observation, now)
}

func OperationsValidateDatabaseDropPlan(plan operationsmodel.DatabaseDropPlan) error {
	if strings.TrimSpace(plan.RetirementID) == "" || strings.TrimSpace(plan.Object.Engine) == "" || strings.TrimSpace(plan.Object.Kind) == "" || strings.TrimSpace(plan.Object.Name) == "" || len(plan.Statements) == 0 || plan.EstimatedLock <= 0 || plan.LockTimeout <= 0 || plan.EstimatedLock > plan.LockTimeout || plan.EstimatedReclaimBytes < 0 || strings.TrimSpace(plan.Rollback) == "" || strings.TrimSpace(plan.ApprovalID) == "" {
		return fmt.Errorf("operations.database_retirement_drop_plan_invalid")
	}
	for _, statement := range plan.Statements {
		normalized := strings.ToUpper(strings.TrimSpace(statement))
		if normalized == "" || strings.Contains(normalized, " CASCADE") {
			return fmt.Errorf("operations.database_retirement_implicit_cascade_forbidden")
		}
	}
	if plan.ExternalTarget && strings.TrimSpace(plan.DeploymentScope) == "" {
		return fmt.Errorf("operations.database_retirement_external_scope_required")
	}
	return nil
}

func operationsValidateDatabaseRetirementIdentity(retirement operationsmodel.DatabaseRetirement) error {
	object := retirement.Object
	if strings.TrimSpace(retirement.ID) == "" || strings.TrimSpace(object.Engine) == "" || strings.TrimSpace(object.Database) == "" || strings.TrimSpace(object.Kind) == "" || strings.TrimSpace(object.Name) == "" || strings.TrimSpace(retirement.Evidence.Owner) == "" || retirement.UpdatedAt.IsZero() {
		return fmt.Errorf("operations.database_retirement_identity_required")
	}
	return nil
}

func databaseRetirementStateIndex(state operationsmodel.DatabaseRetirementState) int {
	for index, candidate := range databaseRetirementOrder {
		if candidate == state {
			return index
		}
	}
	return -100
}

func databaseComparisonMatches(comparison operationsmodel.DatabaseDataComparison) bool {
	return !comparison.ComparedAt.IsZero() && comparison.SourceRows == comparison.ReplacementRows && comparison.SourceKeys == comparison.ReplacementKeys && strings.TrimSpace(comparison.SourceHash) != "" && comparison.SourceHash == comparison.ReplacementHash && len(comparison.BusinessChecks) > 0 && comparison.OrphanRows == 0 && comparison.DuplicateRows == 0 && comparison.InvalidWorkspaces == 0 && comparison.InvalidReferences == 0
}

func databaseDispositionValid(disposition string) bool {
	switch strings.TrimSpace(disposition) {
	case "migrate", "archive", "anonymize", "retain", "discard":
		return true
	default:
		return false
	}
}
