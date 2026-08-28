package policy

import (
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func DefaultPolicyCatalog(workspaceID, publisher string, now time.Time) []lifecyclemodel.PolicyVersion {
	day, year := 24*time.Hour, 365*24*time.Hour
	standard, delayed, locked := lifecyclemodel.BackupBehaviorStandard, lifecyclemodel.BackupBehaviorDelayedErase, lifecyclemodel.BackupBehaviorComplianceLocked
	deleteBehavior, anonymize, protected := lifecyclemodel.EraseBehaviorDelete, lifecyclemodel.EraseBehaviorAnonymize, lifecyclemodel.EraseBehaviorNotEligible
	entries := []struct {
		key, owner         string
		class              lifecyclemodel.RetentionClass
		retention, minimum time.Duration
		sensitivity        []lifecyclemodel.Sensitivity
		backup             lifecyclemodel.BackupBehavior
		erase              lifecyclemodel.EraseBehavior
	}{
		{"record.object.default.v1", "record", lifecyclemodel.RetentionClassProduct, year, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySensitive}, delayed, anonymize},
		{"audit.evidence.v1", "audit", lifecyclemodel.RetentionClassLegalAudit, 7 * year, year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, locked, anonymize},
		{"workflow.definition.v1", "workflow", lifecyclemodel.RetentionClassProduct, 2 * year, year, nil, standard, protected},
		{"workflow.execution.v1", "workflow", lifecyclemodel.RetentionClassLegalAudit, 7 * year, year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, locked, anonymize},
		{"workflow.receipt.v1", "workflow", lifecyclemodel.RetentionClassTechnical, 30 * day, 7 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}, standard, deleteBehavior},
		{"automation.execution.v1", "automation", lifecyclemodel.RetentionClassProduct, year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySensitive}, standard, anonymize},
		{"scheduler.execution.v1", "scheduler", lifecyclemodel.RetentionClassProduct, year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, standard, anonymize},
		{"execution.idempotency_receipt.v1", "action", lifecyclemodel.RetentionClassTechnical, 30 * day, 7 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}, standard, deleteBehavior},
		{"operations.receipt.v1", "operations", lifecyclemodel.RetentionClassLegalAudit, 7 * year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit, lifecyclemodel.SensitivitySecurity}, locked, protected},
		{"operations.control.v1", "operations", lifecyclemodel.RetentionClassLegalAudit, 7 * year, year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit, lifecyclemodel.SensitivitySecurity}, locked, protected},
		{"operations.break_glass.v1", "operations", lifecyclemodel.RetentionClassLegalAudit, 7 * year, year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit, lifecyclemodel.SensitivitySecurity}, locked, protected},
		{"record.batch_artifact.v1", "record", lifecyclemodel.RetentionClassProduct, 90 * day, day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, delayed, deleteBehavior},
		{"integration.configuration.v1", "integration", lifecyclemodel.RetentionClassProduct, year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySensitive}, delayed, anonymize},
		{"integration.secret.v1", "integration", lifecyclemodel.RetentionClassUserErase, 30 * day, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}, delayed, deleteBehavior},
		{"integration.identity_mapping.v1", "integration", lifecyclemodel.RetentionClassUserErase, 90 * day, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, delayed, anonymize},
		{"integration.event.v1", "integration", lifecyclemodel.RetentionClassProduct, 90 * day, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, delayed, anonymize},
		{"integration.delivery_evidence.v1", "integration", lifecyclemodel.RetentionClassLegalAudit, year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, locked, anonymize},
		{"integration.webhook_nonce.v1", "integration", lifecyclemodel.RetentionClassTechnical, day, 15 * time.Minute, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}, standard, deleteBehavior},
		{"technical.lease_checkpoint.v1", "integration", lifecyclemodel.RetentionClassTechnical, 7 * day, day, nil, standard, deleteBehavior},
		{"metadata.definition_history.v1", "metadata", lifecyclemodel.RetentionClassProduct, 7 * year, year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, locked, protected},
		{"notification.history.v1", "notification", lifecyclemodel.RetentionClassProduct, 180 * day, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, standard, anonymize},
		{"notification.publication_history.v1", "notification", lifecyclemodel.RetentionClassProduct, 2 * year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, standard, protected},
		{"localization.text.v1", "localization", lifecyclemodel.RetentionClassProduct, year, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, standard, anonymize},
		{"runtime.configuration.v1", "capability", lifecyclemodel.RetentionClassProduct, year, 90 * day, nil, standard, protected},
		{"persistence.migration_evidence.v1", "deployment", lifecyclemodel.RetentionClassLegalAudit, 100 * year, 100 * year, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit}, locked, protected},
		{"ratelimit.bucket.v1", "runtime_security", lifecyclemodel.RetentionClassTechnical, day, time.Hour, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}, standard, deleteBehavior},
		{"agent.dialog.v1", "agent", lifecyclemodel.RetentionClassProduct, year, 30 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, delayed, anonymize},
		{"report.download.v1", "report", lifecyclemodel.RetentionClassTechnical, 7 * day, day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, standard, deleteBehavior},
		{"report.export.v1", "report", lifecyclemodel.RetentionClassLegalAudit, year, 90 * day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII, lifecyclemodel.SensitivityAudit}, delayed, anonymize},
		{"file.upload.v1", "upload", lifecyclemodel.RetentionClassProduct, year, day, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityPII}, delayed, deleteBehavior},
		{"cache.dictionary.v1", "record", lifecyclemodel.RetentionClassTechnical, 15 * time.Minute, time.Minute, nil, standard, deleteBehavior},
		{"cache.runtime_projection.v1", "metadata", lifecyclemodel.RetentionClassTechnical, 15 * time.Minute, time.Minute, []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity}, standard, deleteBehavior},
	}
	result := make([]lifecyclemodel.PolicyVersion, 0, len(entries))
	for _, entry := range entries {
		statusRetention := map[string]time.Duration{}
		replayWindow := time.Duration(0)
		switch entry.key {
		case "workflow.receipt.v1", "execution.idempotency_receipt.v1":
			replayWindow = 7 * day
		case "integration.webhook_nonce.v1":
			replayWindow = 15 * time.Minute
		case "integration.event.v1":
			statusRetention = map[string]time.Duration{"succeeded": 90 * day, "failed": year}
		case "workflow.execution.v1":
			statusRetention = map[string]time.Duration{"succeeded": 7 * year, "failed": 7 * year}
		case "automation.execution.v1":
			statusRetention = map[string]time.Duration{"succeeded": year, "failed": year}
		case "scheduler.execution.v1":
			statusRetention = map[string]time.Duration{"succeeded": 180 * day, "failed": year, "dead_letter": year}
		case "report.export.v1":
			statusRetention = map[string]time.Duration{"succeeded": year, "failed": year}
		}
		result = append(result, lifecyclemodel.PolicyVersion{WorkspaceID: workspaceID, Policy: lifecyclemodel.RetentionPolicy{Key: entry.key, Version: "1", Owner: entry.owner, Class: entry.class, Sensitivity: entry.sensitivity, DefaultRetention: entry.retention, MinimumRetention: entry.minimum, StatusRetention: statusRetention, ReplayWindow: replayWindow, WorkspaceMayExtend: true, LegalHoldEligible: entry.class != lifecyclemodel.RetentionClassTechnical, BackupBehavior: entry.backup, EraseBehavior: entry.erase}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedBy: publisher, PublishedAt: now})
	}
	return result
}
