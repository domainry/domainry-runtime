package policy

import (
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func TestRetentionPolicyRejectsWorkspaceReductionForAuditEvidence(t *testing.T) {
	policy := lifecyclemodel.RetentionPolicy{
		Key: "audit.evidence.v1", Version: "1", Owner: "audit",
		Class: lifecyclemodel.RetentionClassLegalAudit, Sensitivity: []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivityAudit},
		DefaultRetention: 7 * 365 * 24 * time.Hour, MinimumRetention: 7 * 365 * 24 * time.Hour,
		WorkspaceMayExtend: true, WorkspaceMayReduce: true, LegalHoldEligible: true,
		BackupBehavior: lifecyclemodel.BackupBehaviorComplianceLocked, EraseBehavior: lifecyclemodel.EraseBehaviorAnonymize,
	}
	if err := ValidateRetentionPolicy(policy); err == nil {
		t.Fatal("audit retention must not allow workspace reduction")
	}
	policy.WorkspaceMayReduce = false
	if err := ValidateRetentionPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if _, err := EffectiveWorkspaceRetention(policy, lifecyclemodel.WorkspaceRetentionOverride{WorkspaceID: "workspace-a", Retention: 365 * 24 * time.Hour}); err == nil {
		t.Fatal("workspace override reduced the non-reducible audit retention")
	}
}

func TestRetentionPolicyAllowsBoundedTechnicalTTLOverride(t *testing.T) {
	policy := lifecyclemodel.RetentionPolicy{
		Key: "integration.webhook_nonce.v1", Version: "1", Owner: "integration",
		Class: lifecyclemodel.RetentionClassTechnical, Sensitivity: []lifecyclemodel.Sensitivity{lifecyclemodel.SensitivitySecurity},
		DefaultRetention: 24 * time.Hour, MinimumRetention: 15 * time.Minute,
		WorkspaceMayExtend: true, WorkspaceMayReduce: false,
		BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete,
	}
	retention, err := EffectiveWorkspaceRetention(policy, lifecyclemodel.WorkspaceRetentionOverride{WorkspaceID: "workspace-a", Retention: 48 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if retention != 48*time.Hour {
		t.Fatalf("effective retention = %s", retention)
	}
}
