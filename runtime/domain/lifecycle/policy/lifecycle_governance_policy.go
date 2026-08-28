package policy

import (
	"fmt"
	"strings"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func ValidatePolicyPublication(previous *lifecyclemodel.PolicyVersion, next lifecyclemodel.PolicyVersion) error {
	if err := ValidateRetentionPolicy(next.Policy); err != nil {
		return err
	}
	if next.Status != lifecyclemodel.PolicyStatusPublished || next.Revision <= 0 || strings.TrimSpace(next.PublishedBy) == "" || next.PublishedAt.IsZero() {
		return fmt.Errorf("published policy requires revision, publisher, and publication time")
	}
	if previous != nil {
		if next.Revision != previous.Revision+1 {
			return fmt.Errorf("policy revision must increase by one")
		}
		shortened := next.Policy.DefaultRetention < previous.Policy.DefaultRetention || next.Policy.MinimumRetention < previous.Policy.MinimumRetention || next.Policy.ReplayWindow < previous.Policy.ReplayWindow
		for status, retention := range previous.Policy.StatusRetention {
			if nextRetention, exists := next.Policy.StatusRetention[status]; !exists || nextRetention < retention {
				shortened = true
			}
		}
		if shortened && (strings.TrimSpace(next.ApprovalRef) == "" || strings.TrimSpace(next.ChangePlanRef) == "") {
			return fmt.Errorf("retention shortening requires approval and change plan")
		}
	}
	return nil
}

func ValidateCleanupJob(job lifecyclemodel.CleanupJob) error {
	if strings.TrimSpace(job.ID) == "" || strings.TrimSpace(job.WorkspaceID) == "" || strings.TrimSpace(job.PolicyKey) == "" || strings.TrimSpace(job.PolicyVersion) == "" || strings.TrimSpace(job.RequestedBy) == "" || strings.TrimSpace(job.Reason) == "" {
		return fmt.Errorf("cleanup identity, workspace, policy, requester, and reason are required")
	}
	if job.Operation != lifecyclemodel.OperationArchive && job.Operation != lifecyclemodel.OperationPurge {
		return fmt.Errorf("cleanup operation must be archive or purge")
	}
	if job.Status == "" || job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
		return fmt.Errorf("cleanup status and timestamps are required")
	}
	return nil
}

func TransitionSubjectRequest(current, next lifecyclemodel.SubjectRequest) error {
	if current.ID == "" || next.ID != current.ID || current.WorkspaceID == "" || next.WorkspaceID != current.WorkspaceID || next.UpdatedAt.IsZero() || next.UpdatedAt.Before(current.UpdatedAt) {
		return fmt.Errorf("subject request identity and monotonic update are required")
	}
	switch {
	case current.Status == lifecyclemodel.SubjectRequestPendingVerification && next.Status == lifecyclemodel.SubjectRequestVerified:
		if strings.TrimSpace(next.ResolvedIdentity) == "" || strings.TrimSpace(next.VerifiedBy) == "" || strings.TrimSpace(next.SecondFactorRef) == "" {
			return fmt.Errorf("subject verification requires resolved identity, verifier, and second factor")
		}
	case current.Status == lifecyclemodel.SubjectRequestVerified && next.Status == lifecyclemodel.SubjectRequestPreviewed:
		if len(next.ImpactPreview) == 0 {
			return fmt.Errorf("subject request preview evidence is required")
		}
	case current.Status == lifecyclemodel.SubjectRequestPreviewed && next.Status == lifecyclemodel.SubjectRequestApproved:
		if strings.TrimSpace(next.ApprovedBy) == "" || next.ApprovedBy == next.RequestedBy {
			return fmt.Errorf("subject request requires independent approval")
		}
	case (current.Status == lifecyclemodel.SubjectRequestApproved || current.Status == lifecyclemodel.SubjectRequestFailed) && next.Status == lifecyclemodel.SubjectRequestExecuting:
		if next.ExecutionAttempt != current.ExecutionAttempt+1 || !next.ExecutionLeaseEnd.After(next.UpdatedAt) || next.LastError != "" {
			return fmt.Errorf("subject execution requires a new leased attempt")
		}
	case current.Status == lifecyclemodel.SubjectRequestExecuting && next.Status == lifecyclemodel.SubjectRequestExecuting:
		if current.ExecutionLeaseEnd.IsZero() || next.UpdatedAt.Before(current.ExecutionLeaseEnd) || next.ExecutionAttempt != current.ExecutionAttempt+1 || !next.ExecutionLeaseEnd.After(next.UpdatedAt) || next.LastError != "" {
			return fmt.Errorf("subject execution lease is still active")
		}
	case current.Status == lifecyclemodel.SubjectRequestExecuting && next.Status == lifecyclemodel.SubjectRequestSucceeded:
		if strings.TrimSpace(next.ResultReference) == "" {
			return fmt.Errorf("successful subject request requires result evidence")
		}
		if next.Kind == lifecyclemodel.SubjectRequestExport && (next.DownloadExpiresAt.IsZero() || !next.DownloadExpiresAt.After(next.UpdatedAt)) {
			return fmt.Errorf("subject export requires expiring download")
		}
		if !next.ExecutionLeaseEnd.IsZero() {
			return fmt.Errorf("successful subject request must release its execution lease")
		}
	case current.Status == lifecyclemodel.SubjectRequestExecuting && next.Status == lifecyclemodel.SubjectRequestFailed:
		if strings.TrimSpace(next.LastError) == "" {
			return fmt.Errorf("failed subject request requires error evidence")
		}
		if !next.ExecutionLeaseEnd.IsZero() {
			return fmt.Errorf("failed subject request must release its execution lease")
		}
	default:
		return fmt.Errorf("invalid subject request transition %s -> %s", current.Status, next.Status)
	}
	return nil
}

func LegalHoldActive(hold lifecyclemodel.LegalHold, now time.Time) bool {
	return !now.Before(hold.StartsAt) && (hold.EndsAt == nil || now.Before(*hold.EndsAt))
}
