package policy

import (
	"encoding/json"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func publishedPolicyVersion(now time.Time) lifecyclemodel.PolicyVersion {
	policy := validRetentionPolicy()
	policy.WorkspaceMayReduce = false
	return lifecyclemodel.PolicyVersion{
		Policy: policy, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1,
		PublishedBy: "admin", PublishedAt: now,
	}
}

func TestValidatePolicyPublicationMatrix(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	base := publishedPolicyVersion(now)
	if err := ValidatePolicyPublication(nil, base); err != nil {
		t.Fatal(err)
	}
	invalidPolicy := base
	invalidPolicy.Policy.Key = ""
	if err := ValidatePolicyPublication(nil, invalidPolicy); err == nil {
		t.Fatal("invalid retention policy must fail publication")
	}
	for _, test := range []struct {
		name   string
		mutate func(*lifecyclemodel.PolicyVersion)
	}{
		{name: "status", mutate: func(version *lifecyclemodel.PolicyVersion) { version.Status = lifecyclemodel.PolicyStatusDraft }},
		{name: "revision", mutate: func(version *lifecyclemodel.PolicyVersion) { version.Revision = 0 }},
		{name: "publisher", mutate: func(version *lifecyclemodel.PolicyVersion) { version.PublishedBy = " " }},
		{name: "time", mutate: func(version *lifecyclemodel.PolicyVersion) { version.PublishedAt = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			version := base
			test.mutate(&version)
			if err := ValidatePolicyPublication(nil, version); err == nil {
				t.Fatal("expected invalid publication")
			}
		})
	}
	next := base
	next.Revision = 2
	next.Policy.Version = "2"
	next.PublishedAt = now.Add(time.Hour)
	if err := ValidatePolicyPublication(&base, next); err != nil {
		t.Fatalf("non-shortening publication: %v", err)
	}
	wrongRevision := next
	wrongRevision.Revision = 3
	if err := ValidatePolicyPublication(&base, wrongRevision); err == nil {
		t.Fatal("revision gap must fail")
	}
	shortenMutations := []struct {
		name   string
		mutate func(*lifecyclemodel.PolicyVersion)
	}{
		{name: "default", mutate: func(version *lifecyclemodel.PolicyVersion) { version.Policy.DefaultRetention -= time.Hour }},
		{name: "minimum", mutate: func(version *lifecyclemodel.PolicyVersion) { version.Policy.MinimumRetention -= time.Hour }},
		{name: "replay", mutate: func(version *lifecyclemodel.PolicyVersion) { version.Policy.ReplayWindow -= time.Hour }},
		{name: "status missing", mutate: func(version *lifecyclemodel.PolicyVersion) {
			version.Policy.StatusRetention = map[string]time.Duration{}
		}},
		{name: "status shorter", mutate: func(version *lifecyclemodel.PolicyVersion) {
			version.Policy.StatusRetention = map[string]time.Duration{"closed": 6 * 24 * time.Hour}
		}},
	}
	for _, test := range shortenMutations {
		t.Run(test.name, func(t *testing.T) {
			version := next
			test.mutate(&version)
			if err := ValidatePolicyPublication(&base, version); err == nil {
				t.Fatal("shortening without governance evidence must fail")
			}
			version.ApprovalRef = "approval-1"
			if err := ValidatePolicyPublication(&base, version); err == nil {
				t.Fatal("shortening without change plan must fail")
			}
			version.ApprovalRef = ""
			version.ChangePlanRef = "change-1"
			if err := ValidatePolicyPublication(&base, version); err == nil {
				t.Fatal("shortening without approval must fail")
			}
			version.ApprovalRef = "approval-1"
			if err := ValidatePolicyPublication(&base, version); err != nil {
				t.Fatalf("governed shortening: %v", err)
			}
		})
	}
}

func TestValidateCleanupJobMatrix(t *testing.T) {
	now := time.Now().UTC()
	base := lifecyclemodel.CleanupJob{
		ID: "job", WorkspaceID: "workspace", PolicyKey: "policy", PolicyVersion: "1",
		RequestedBy: "operator", Reason: "retention", Operation: lifecyclemodel.OperationArchive,
		Status: lifecyclemodel.CleanupStatusPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := ValidateCleanupJob(base); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*lifecyclemodel.CleanupJob)
	}{
		{name: "id", mutate: func(job *lifecyclemodel.CleanupJob) { job.ID = "" }},
		{name: "workspace", mutate: func(job *lifecyclemodel.CleanupJob) { job.WorkspaceID = "" }},
		{name: "policy", mutate: func(job *lifecyclemodel.CleanupJob) { job.PolicyKey = "" }},
		{name: "version", mutate: func(job *lifecyclemodel.CleanupJob) { job.PolicyVersion = "" }},
		{name: "requester", mutate: func(job *lifecyclemodel.CleanupJob) { job.RequestedBy = "" }},
		{name: "reason", mutate: func(job *lifecyclemodel.CleanupJob) { job.Reason = "" }},
		{name: "operation", mutate: func(job *lifecyclemodel.CleanupJob) { job.Operation = lifecyclemodel.OperationErase }},
		{name: "status", mutate: func(job *lifecyclemodel.CleanupJob) { job.Status = "" }},
		{name: "created", mutate: func(job *lifecyclemodel.CleanupJob) { job.CreatedAt = time.Time{} }},
		{name: "updated", mutate: func(job *lifecyclemodel.CleanupJob) { job.UpdatedAt = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			job := base
			test.mutate(&job)
			if err := ValidateCleanupJob(job); err == nil {
				t.Fatal("expected invalid cleanup job")
			}
		})
	}
	base.Operation = lifecyclemodel.OperationPurge
	if err := ValidateCleanupJob(base); err != nil {
		t.Fatalf("purge cleanup: %v", err)
	}
}

func subjectRequest(status lifecyclemodel.SubjectRequestStatus, now time.Time) lifecyclemodel.SubjectRequest {
	return lifecyclemodel.SubjectRequest{
		ID: "request", WorkspaceID: "workspace", Kind: lifecyclemodel.SubjectRequestErase,
		Status: status, RequestedBy: "requester", UpdatedAt: now,
	}
}

func assertSubjectTransition(t *testing.T, current, next lifecyclemodel.SubjectRequest, wantValid bool) {
	t.Helper()
	err := TransitionSubjectRequest(current, next)
	if wantValid && err != nil {
		t.Fatalf("expected valid transition %s -> %s: %v", current.Status, next.Status, err)
	}
	if !wantValid && err == nil {
		t.Fatalf("expected invalid transition %s -> %s", current.Status, next.Status)
	}
}

func TestTransitionSubjectRequestIdentityAndLifecycleMatrix(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	current := subjectRequest(lifecyclemodel.SubjectRequestPendingVerification, now)
	validVerified := current
	validVerified.Status = lifecyclemodel.SubjectRequestVerified
	validVerified.ResolvedIdentity, validVerified.VerifiedBy, validVerified.SecondFactorRef = "user", "verifier", "mfa"
	validVerified.UpdatedAt = now.Add(time.Minute)
	for _, mutate := range []func(*lifecyclemodel.SubjectRequest, *lifecyclemodel.SubjectRequest){
		func(current, _ *lifecyclemodel.SubjectRequest) { current.ID = "" },
		func(_, next *lifecyclemodel.SubjectRequest) { next.ID = "other" },
		func(current, _ *lifecyclemodel.SubjectRequest) { current.WorkspaceID = "" },
		func(_, next *lifecyclemodel.SubjectRequest) { next.WorkspaceID = "other" },
		func(_, next *lifecyclemodel.SubjectRequest) { next.UpdatedAt = time.Time{} },
		func(_, next *lifecyclemodel.SubjectRequest) { next.UpdatedAt = now.Add(-time.Minute) },
	} {
		invalidCurrent, invalidNext := current, validVerified
		mutate(&invalidCurrent, &invalidNext)
		assertSubjectTransition(t, invalidCurrent, invalidNext, false)
	}
	for _, mutate := range []func(*lifecyclemodel.SubjectRequest){
		func(next *lifecyclemodel.SubjectRequest) { next.ResolvedIdentity = "" },
		func(next *lifecyclemodel.SubjectRequest) { next.VerifiedBy = "" },
		func(next *lifecyclemodel.SubjectRequest) { next.SecondFactorRef = "" },
	} {
		next := validVerified
		mutate(&next)
		assertSubjectTransition(t, current, next, false)
	}
	assertSubjectTransition(t, current, validVerified, true)

	previewed := validVerified
	previewed.Status, previewed.ImpactPreview, previewed.UpdatedAt = lifecyclemodel.SubjectRequestPreviewed, json.RawMessage(`{"records":1}`), now.Add(2*time.Minute)
	emptyPreview := previewed
	emptyPreview.ImpactPreview = nil
	assertSubjectTransition(t, validVerified, emptyPreview, false)
	assertSubjectTransition(t, validVerified, previewed, true)
	verifiedWrongNext := validVerified
	verifiedWrongNext.Status, verifiedWrongNext.UpdatedAt = lifecyclemodel.SubjectRequestApproved, now.Add(2*time.Minute)
	assertSubjectTransition(t, validVerified, verifiedWrongNext, false)

	approved := previewed
	approved.Status, approved.ApprovedBy, approved.UpdatedAt = lifecyclemodel.SubjectRequestApproved, "approver", now.Add(3*time.Minute)
	missingApprover := approved
	missingApprover.ApprovedBy = ""
	assertSubjectTransition(t, previewed, missingApprover, false)
	selfApproved := approved
	selfApproved.ApprovedBy = selfApproved.RequestedBy
	assertSubjectTransition(t, previewed, selfApproved, false)
	assertSubjectTransition(t, previewed, approved, true)
	previewWrongNext := previewed
	previewWrongNext.Status, previewWrongNext.UpdatedAt = lifecyclemodel.SubjectRequestExecuting, now.Add(3*time.Minute)
	assertSubjectTransition(t, previewed, previewWrongNext, false)

	executing := approved
	executing.Status, executing.ExecutionAttempt, executing.UpdatedAt, executing.ExecutionLeaseEnd = lifecyclemodel.SubjectRequestExecuting, 1, now.Add(4*time.Minute), now.Add(10*time.Minute)
	for _, mutate := range []func(*lifecyclemodel.SubjectRequest){
		func(next *lifecyclemodel.SubjectRequest) { next.ExecutionAttempt = 2 },
		func(next *lifecyclemodel.SubjectRequest) { next.ExecutionLeaseEnd = next.UpdatedAt },
		func(next *lifecyclemodel.SubjectRequest) { next.LastError = "old" },
	} {
		next := executing
		mutate(&next)
		assertSubjectTransition(t, approved, next, false)
	}
	assertSubjectTransition(t, approved, executing, true)
	approvedWrongNext := approved
	approvedWrongNext.Status, approvedWrongNext.UpdatedAt = lifecyclemodel.SubjectRequestSucceeded, now.Add(4*time.Minute)
	assertSubjectTransition(t, approved, approvedWrongNext, false)
	failedCurrent := approved
	failedCurrent.Status, failedCurrent.ExecutionAttempt = lifecyclemodel.SubjectRequestFailed, 2
	retry := executing
	retry.ExecutionAttempt = 3
	assertSubjectTransition(t, failedCurrent, retry, true)

	reclaimed := executing
	reclaimed.UpdatedAt, reclaimed.ExecutionAttempt, reclaimed.ExecutionLeaseEnd = executing.ExecutionLeaseEnd, 2, executing.ExecutionLeaseEnd.Add(time.Minute)
	for _, mutateCurrentNext := range []func(*lifecyclemodel.SubjectRequest, *lifecyclemodel.SubjectRequest){
		func(current, _ *lifecyclemodel.SubjectRequest) { current.ExecutionLeaseEnd = time.Time{} },
		func(_, next *lifecyclemodel.SubjectRequest) {
			next.UpdatedAt = executing.ExecutionLeaseEnd.Add(-time.Nanosecond)
		},
		func(_, next *lifecyclemodel.SubjectRequest) { next.ExecutionAttempt = 3 },
		func(_, next *lifecyclemodel.SubjectRequest) { next.ExecutionLeaseEnd = next.UpdatedAt },
		func(_, next *lifecyclemodel.SubjectRequest) { next.LastError = "old" },
	} {
		invalidCurrent, invalidNext := executing, reclaimed
		mutateCurrentNext(&invalidCurrent, &invalidNext)
		assertSubjectTransition(t, invalidCurrent, invalidNext, false)
	}
	assertSubjectTransition(t, executing, reclaimed, true)

	succeeded := executing
	succeeded.Status, succeeded.ResultReference, succeeded.UpdatedAt, succeeded.ExecutionLeaseEnd = lifecyclemodel.SubjectRequestSucceeded, "result", now.Add(5*time.Minute), time.Time{}
	missingResult := succeeded
	missingResult.ResultReference = ""
	assertSubjectTransition(t, executing, missingResult, false)
	leasedSuccess := succeeded
	leasedSuccess.ExecutionLeaseEnd = now.Add(time.Hour)
	assertSubjectTransition(t, executing, leasedSuccess, false)
	assertSubjectTransition(t, executing, succeeded, true)
	exportSuccess := succeeded
	exportSuccess.Kind = lifecyclemodel.SubjectRequestExport
	exportSuccess.DownloadExpiresAt = exportSuccess.UpdatedAt.Add(time.Hour)
	assertSubjectTransition(t, executing, exportSuccess, true)
	for _, expiry := range []time.Time{{}, exportSuccess.UpdatedAt, exportSuccess.UpdatedAt.Add(-time.Second)} {
		invalidExport := exportSuccess
		invalidExport.DownloadExpiresAt = expiry
		assertSubjectTransition(t, executing, invalidExport, false)
	}

	failed := executing
	failed.Status, failed.LastError, failed.UpdatedAt, failed.ExecutionLeaseEnd = lifecyclemodel.SubjectRequestFailed, "provider failed", now.Add(5*time.Minute), time.Time{}
	missingError := failed
	missingError.LastError = ""
	assertSubjectTransition(t, executing, missingError, false)
	leasedFailure := failed
	leasedFailure.ExecutionLeaseEnd = now.Add(time.Hour)
	assertSubjectTransition(t, executing, leasedFailure, false)
	assertSubjectTransition(t, executing, failed, true)
	executingWrongNext := executing
	executingWrongNext.Status, executingWrongNext.UpdatedAt = lifecyclemodel.SubjectRequestPendingVerification, now.Add(5*time.Minute)
	assertSubjectTransition(t, executing, executingWrongNext, false)

	invalidNext := current
	invalidNext.Status, invalidNext.UpdatedAt = lifecyclemodel.SubjectRequestSucceeded, now.Add(time.Minute)
	assertSubjectTransition(t, current, invalidNext, false)
}

func TestLegalHoldActiveBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if LegalHoldActive(lifecyclemodel.LegalHold{StartsAt: now.Add(time.Second)}, now) {
		t.Fatal("future hold must be inactive")
	}
	if !LegalHoldActive(lifecyclemodel.LegalHold{StartsAt: now}, now) {
		t.Fatal("open-ended hold must be active at start")
	}
	end := now.Add(time.Hour)
	if !LegalHoldActive(lifecyclemodel.LegalHold{StartsAt: now.Add(-time.Hour), EndsAt: &end}, now) {
		t.Fatal("hold before end must be active")
	}
	if LegalHoldActive(lifecyclemodel.LegalHold{StartsAt: now.Add(-time.Hour), EndsAt: &end}, end) {
		t.Fatal("hold at end must be inactive")
	}
}
