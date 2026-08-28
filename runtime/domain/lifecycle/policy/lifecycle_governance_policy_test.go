package policy

import (
	"encoding/json"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func TestRetentionShorteningRequiresApprovalAndChangePlan(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	base := lifecyclemodel.RetentionPolicy{Key: "record.default", Version: "1", Owner: "record", Class: lifecyclemodel.RetentionClassProduct, DefaultRetention: 365 * 24 * time.Hour, MinimumRetention: 30 * 24 * time.Hour, WorkspaceMayExtend: true, LegalHoldEligible: true, BackupBehavior: lifecyclemodel.BackupBehaviorDelayedErase, EraseBehavior: lifecyclemodel.EraseBehaviorAnonymize}
	previous := lifecyclemodel.PolicyVersion{Policy: base, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedBy: "admin", PublishedAt: now}
	next := previous
	next.Revision, next.Policy.Version, next.Policy.DefaultRetention, next.PublishedAt = 2, "2", 180*24*time.Hour, now.Add(time.Hour)
	if err := ValidatePolicyPublication(&previous, next); err == nil {
		t.Fatal("retention shortening without approval accepted")
	}
	next.ApprovalRef, next.ChangePlanRef = "approval-1", "change-plan-1"
	if err := ValidatePolicyPublication(&previous, next); err != nil {
		t.Fatal(err)
	}
}

func TestSubjectRequestRequiresVerifyPreviewIndependentApprovalAndEvidence(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	current := lifecyclemodel.SubjectRequest{ID: "request-1", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport, Status: lifecyclemodel.SubjectRequestPendingVerification, RequestedBy: "requester", UpdatedAt: now}
	next := current
	next.Status, next.ResolvedIdentity, next.VerifiedBy, next.SecondFactorRef, next.UpdatedAt = lifecyclemodel.SubjectRequestVerified, "identity-1", "verifier", "mfa-1", now.Add(time.Minute)
	if err := TransitionSubjectRequest(current, next); err != nil {
		t.Fatal(err)
	}
	current = next
	next.Status, next.ImpactPreview, next.UpdatedAt = lifecyclemodel.SubjectRequestPreviewed, json.RawMessage(`{"owners":["record","identity"]}`), now.Add(2*time.Minute)
	if err := TransitionSubjectRequest(current, next); err != nil {
		t.Fatal(err)
	}
	current = next
	next.Status, next.ApprovedBy, next.UpdatedAt = lifecyclemodel.SubjectRequestApproved, "requester", now.Add(3*time.Minute)
	if err := TransitionSubjectRequest(current, next); err == nil {
		t.Fatal("self approval accepted")
	}
	next.ApprovedBy = "approver"
	if err := TransitionSubjectRequest(current, next); err != nil {
		t.Fatal(err)
	}
}

func TestSubjectExecutionRequiresLeaseAndOnlyExpiredExecutionCanBeReclaimed(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	approved := lifecyclemodel.SubjectRequest{ID: "request-1", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestErase, Status: lifecyclemodel.SubjectRequestApproved, RequestedBy: "requester", VerifiedBy: "verifier", ApprovedBy: "approver", SecondFactorRef: "mfa", ResolvedIdentity: "user-1", ImpactPreview: json.RawMessage(`{"owners":["record"]}`), UpdatedAt: now}
	executing := approved
	executing.Status, executing.ExecutionAttempt, executing.UpdatedAt, executing.ExecutionLeaseEnd = lifecyclemodel.SubjectRequestExecuting, 1, now.Add(time.Minute), now.Add(6*time.Minute)
	if err := TransitionSubjectRequest(approved, executing); err != nil {
		t.Fatal(err)
	}
	reclaim := executing
	reclaim.ExecutionAttempt, reclaim.UpdatedAt, reclaim.ExecutionLeaseEnd = 2, now.Add(2*time.Minute), now.Add(7*time.Minute)
	if err := TransitionSubjectRequest(executing, reclaim); err == nil {
		t.Fatal("active subject execution lease was reclaimed")
	}
	reclaim.UpdatedAt, reclaim.ExecutionLeaseEnd = now.Add(7*time.Minute), now.Add(12*time.Minute)
	if err := TransitionSubjectRequest(executing, reclaim); err != nil {
		t.Fatal(err)
	}
}

func TestRetentionCannotUndercutReplayWindow(t *testing.T) {
	policy := lifecyclemodel.RetentionPolicy{Key: "execution.idempotency_receipt.v1", Version: "1", Owner: "action", Class: lifecyclemodel.RetentionClassTechnical, DefaultRetention: 24 * time.Hour, MinimumRetention: time.Hour, ReplayWindow: 48 * time.Hour, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete}
	if err := ValidateRetentionPolicy(policy); err == nil {
		t.Fatal("retention shorter than replay window accepted")
	}
}
