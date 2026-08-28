package policy

import (
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func TestLegalHoldBlocksArchivePurgeAndEraseOnlyInsideItsScope(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	hold := lifecyclemodel.LegalHold{ID: "hold-1", WorkspaceID: "workspace-a", Owner: "record", ResourceType: "customer", ResourceID: "customer-1", Reason: "litigation", Authority: "legal", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(30 * 24 * time.Hour), AuditEvidence: "audit-1"}
	for _, operation := range []lifecyclemodel.Operation{lifecyclemodel.OperationArchive, lifecyclemodel.OperationPurge, lifecyclemodel.OperationErase} {
		decision, err := EvaluateEligibility(lifecyclemodel.EligibilityInput{Operation: operation, Target: lifecyclemodel.ResourceTarget{WorkspaceID: "workspace-a", Owner: "record", ResourceType: "customer", ResourceID: "customer-1"}, OwnerEligible: true, LegalHolds: []lifecyclemodel.LegalHold{hold}, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if decision.Eligible || len(decision.Blockers) != 1 || decision.Blockers[0] != "legal_hold:hold-1" {
			t.Fatalf("%s decision = %#v", operation, decision)
		}
	}
	decision, err := EvaluateEligibility(lifecyclemodel.EligibilityInput{Operation: lifecyclemodel.OperationPurge, Target: lifecyclemodel.ResourceTarget{WorkspaceID: "workspace-b", Owner: "record", ResourceType: "customer", ResourceID: "customer-1"}, OwnerEligible: true, LegalHolds: []lifecyclemodel.LegalHold{hold}, Now: now})
	if err != nil || !decision.Eligible {
		t.Fatalf("hold crossed workspace boundary: decision=%#v err=%v", decision, err)
	}
}

func TestPurgeRequiresOwnerEligibilityAndAllReferenceSafetyChecks(t *testing.T) {
	decision, err := EvaluateEligibility(lifecyclemodel.EligibilityInput{
		Operation:     lifecyclemodel.OperationPurge,
		Target:        lifecyclemodel.ResourceTarget{WorkspaceID: "workspace-a", Owner: "integration", ResourceType: "outbox", ResourceID: "message-1"},
		ActiveProcess: true, PendingOutbox: true, Referenced: true, BackupPolicyBlocked: true,
		Now: time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"active_process", "backup_policy", "owner_policy", "pending_outbox", "reference"}
	if len(decision.Blockers) != len(want) {
		t.Fatalf("blockers = %#v", decision.Blockers)
	}
	for index := range want {
		if decision.Blockers[index] != want[index] {
			t.Fatalf("blockers = %#v, want %#v", decision.Blockers, want)
		}
	}
}

func TestEvaluateEligibilityRejectsInvalidInputAndInvalidHolds(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	validTarget := lifecyclemodel.ResourceTarget{WorkspaceID: "workspace-a", Owner: "record", ResourceType: "customer", ResourceID: "customer-1"}
	for _, input := range []lifecyclemodel.EligibilityInput{
		{Operation: lifecyclemodel.Operation("unknown"), Target: validTarget, Now: now},
		{Operation: lifecyclemodel.OperationPurge, Target: lifecyclemodel.ResourceTarget{}, Now: now},
		{Operation: lifecyclemodel.OperationPurge, Target: lifecyclemodel.ResourceTarget{WorkspaceID: "workspace-a"}, Now: now},
		{Operation: lifecyclemodel.OperationPurge, Target: lifecyclemodel.ResourceTarget{WorkspaceID: "workspace-a", Owner: "record"}, Now: now},
		{Operation: lifecyclemodel.OperationPurge, Target: lifecyclemodel.ResourceTarget{WorkspaceID: "workspace-a", Owner: "record", ResourceType: "customer"}, Now: now},
		{Operation: lifecyclemodel.OperationPurge, Target: validTarget},
	} {
		if _, err := EvaluateEligibility(input); err == nil {
			t.Fatalf("invalid input accepted: %#v", input)
		}
	}
	validHold := lifecyclemodel.LegalHold{ID: "hold-1", WorkspaceID: "workspace-a", Reason: "legal", Authority: "court", AuditEvidence: "audit-1", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour)}
	invalidHolds := []lifecyclemodel.LegalHold{
		{},
		{ID: "hold-1"},
		{ID: "hold-1", WorkspaceID: "workspace-a"},
		{ID: "hold-1", WorkspaceID: "workspace-a", Reason: "legal"},
		{ID: "hold-1", WorkspaceID: "workspace-a", Reason: "legal", Authority: "court"},
		{ID: "hold-1", WorkspaceID: "workspace-a", Reason: "legal", Authority: "court", AuditEvidence: "audit-1"},
		{ID: "hold-1", WorkspaceID: "workspace-a", Reason: "legal", Authority: "court", AuditEvidence: "audit-1", StartsAt: now},
	}
	end := validHold.StartsAt
	invalidEnd := validHold
	invalidEnd.EndsAt = &end
	invalidHolds = append(invalidHolds, invalidEnd)
	for _, hold := range invalidHolds {
		if _, err := EvaluateEligibility(lifecyclemodel.EligibilityInput{Operation: lifecyclemodel.OperationPurge, Target: validTarget, OwnerEligible: true, LegalHolds: []lifecyclemodel.LegalHold{hold}, Now: now}); err == nil {
			t.Fatalf("invalid hold accepted: %#v", hold)
		}
	}
	decision, err := EvaluateEligibility(lifecyclemodel.EligibilityInput{Operation: lifecyclemodel.OperationPurge, Target: validTarget, OwnerEligible: true, Now: now})
	if err != nil || !decision.Eligible || len(decision.Blockers) != 0 {
		t.Fatalf("eligible decision = %#v, err=%v", decision, err)
	}
	validEnd := now.Add(time.Hour)
	ended := validHold
	ended.EndsAt = &validEnd
	if err := ValidateLegalHold(ended); err != nil {
		t.Fatalf("valid finite hold: %v", err)
	}
	future := validHold
	future.StartsAt, future.ReviewAt = now.Add(time.Hour), now.Add(2*time.Hour)
	if legalHoldApplies(future, validTarget, now) {
		t.Fatal("future hold applied")
	}
	if !legalHoldApplies(ended, validTarget, now) {
		t.Fatal("active finite hold did not apply")
	}
	expiredEnd := now.Add(-time.Minute)
	expired := validHold
	expired.StartsAt, expired.EndsAt = now.Add(-2*time.Hour), &expiredEnd
	if legalHoldApplies(expired, validTarget, now) {
		t.Fatal("expired hold applied")
	}
}
