package policy

import (
	"fmt"
	"sort"
	"strings"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func EvaluateEligibility(input lifecyclemodel.EligibilityInput) (lifecyclemodel.EligibilityDecision, error) {
	if input.Operation != lifecyclemodel.OperationArchive && input.Operation != lifecyclemodel.OperationPurge && input.Operation != lifecyclemodel.OperationErase {
		return lifecyclemodel.EligibilityDecision{}, fmt.Errorf("unsupported lifecycle operation %q", input.Operation)
	}
	if strings.TrimSpace(input.Target.WorkspaceID) == "" || strings.TrimSpace(input.Target.Owner) == "" || strings.TrimSpace(input.Target.ResourceType) == "" || strings.TrimSpace(input.Target.ResourceID) == "" {
		return lifecyclemodel.EligibilityDecision{}, fmt.Errorf("workspace, owner, resource type, and resource id are required")
	}
	if input.Now.IsZero() {
		return lifecyclemodel.EligibilityDecision{}, fmt.Errorf("decision time is required")
	}
	blockers := make([]string, 0, 6)
	if !input.OwnerEligible {
		blockers = append(blockers, "owner_policy")
	}
	if input.ActiveProcess {
		blockers = append(blockers, "active_process")
	}
	if input.PendingOutbox {
		blockers = append(blockers, "pending_outbox")
	}
	if input.Referenced {
		blockers = append(blockers, "reference")
	}
	if input.BackupPolicyBlocked {
		blockers = append(blockers, "backup_policy")
	}
	for _, hold := range input.LegalHolds {
		if err := ValidateLegalHold(hold); err != nil {
			return lifecyclemodel.EligibilityDecision{}, err
		}
		if legalHoldApplies(hold, input.Target, input.Now) {
			blockers = append(blockers, "legal_hold:"+hold.ID)
		}
	}
	sort.Strings(blockers)
	return lifecyclemodel.EligibilityDecision{Eligible: len(blockers) == 0, Blockers: blockers}, nil
}

func ValidateLegalHold(hold lifecyclemodel.LegalHold) error {
	if strings.TrimSpace(hold.ID) == "" || strings.TrimSpace(hold.WorkspaceID) == "" || strings.TrimSpace(hold.Reason) == "" || strings.TrimSpace(hold.Authority) == "" || strings.TrimSpace(hold.AuditEvidence) == "" {
		return fmt.Errorf("legal hold id, workspace, reason, authority, and audit evidence are required")
	}
	if hold.StartsAt.IsZero() || hold.ReviewAt.IsZero() {
		return fmt.Errorf("legal hold start and review times are required")
	}
	if hold.EndsAt != nil && !hold.EndsAt.After(hold.StartsAt) {
		return fmt.Errorf("legal hold end must be after start")
	}
	return nil
}

func legalHoldApplies(hold lifecyclemodel.LegalHold, target lifecyclemodel.ResourceTarget, now time.Time) bool {
	if hold.WorkspaceID != target.WorkspaceID || now.Before(hold.StartsAt) || (hold.EndsAt != nil && !now.Before(*hold.EndsAt)) {
		return false
	}
	return (hold.Owner == "" || hold.Owner == target.Owner) &&
		(hold.ResourceType == "" || hold.ResourceType == target.ResourceType) &&
		(hold.ResourceID == "" || hold.ResourceID == target.ResourceID)
}
